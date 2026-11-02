// Package mail 发送邮件，对应原版 App\Services\Mail。支持 none / smtp / mailgun / sendgrid。
package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"

	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

// Sender 是邮件发送接口，便于测试替换。
type Sender interface {
	Send(ctx context.Context, to, subject, tmpl string, data map[string]any, files ...string) error
}

// Service 渲染邮件模板并按配置的方式发送。
type Service struct {
	cfg  config.Config
	tpl  *template.Template
	log  *slog.Logger
	http *http.Client
}

// New 解析 templates 下的 email/*.html 模板。
func New(cfg config.Config, templates fs.FS, log *slog.Logger) (*Service, error) {
	t, err := template.New("").ParseFS(templates, "email/*.html")
	if err != nil {
		return nil, fmt.Errorf("解析邮件模板: %w", err)
	}
	return &Service{cfg: cfg, tpl: t, log: log, http: &http.Client{Timeout: 30 * time.Second}}, nil
}

// Render 渲染邮件正文。tmpl 为模板名（例如 "warn"），data 中的键在模板里用 .Data.<键> 引用。
func (s *Service) Render(tmpl string, data map[string]any) (string, error) {
	var buf bytes.Buffer
	err := s.tpl.ExecuteTemplate(&buf, "email/"+tmpl, map[string]any{
		"Site": map[string]any{"Cfg": s.cfg},
		"Data": data,
	})
	return buf.String(), err
}

// Send 渲染并发送邮件。files 为附件路径（原版用于发送备份）。
func (s *Service) Send(ctx context.Context, to, subject, tmpl string, data map[string]any, files ...string) error {
	body, err := s.Render(tmpl, data)
	if err != nil {
		return err
	}
	switch s.cfg.Mail.Driver {
	case "smtp":
		return s.sendSMTP(ctx, to, subject, body, files)
	case "mailgun":
		return s.sendMailgun(ctx, to, subject, body, files)
	case "sendgrid":
		return s.sendSendgrid(ctx, to, subject, body, files)
	default:
		s.log.Info("邮件发送方式为 none，未实际发送", "to", to, "subject", subject)
		return nil
	}
}

func (s *Service) sendSMTP(ctx context.Context, to, subject, body string, files []string) error {
	c := s.cfg.Mail
	m := gomail.NewMsg()
	if err := m.FromFormat(c.SMTPName, c.SMTPSender); err != nil {
		return fmt.Errorf("发件人地址无效: %w", err)
	}
	if err := m.To(to); err != nil {
		return fmt.Errorf("收件人地址无效: %w", err)
	}
	m.Subject(subject)
	m.SetBodyString(gomail.TypeTextHTML, body)
	for _, f := range files {
		m.AttachFile(f)
	}
	opts := []gomail.Option{
		gomail.WithPort(c.SMTPPort),
		gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
		gomail.WithUsername(c.SMTPUsername),
		gomail.WithPassword(c.SMTPPassword),
		gomail.WithTimeout(30 * time.Second),
	}
	if c.SMTPSSL {
		opts = append(opts, gomail.WithSSL())
	} else {
		// 不使用 SMTPS 时强制 STARTTLS，不允许明文发送密码。
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	client, err := gomail.NewClient(c.SMTPHost, opts...)
	if err != nil {
		return err
	}
	return client.DialAndSendWithContext(ctx, m)
}

func (s *Service) sendMailgun(ctx context.Context, to, subject, body string, files []string) error {
	c := s.cfg.Mail
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("from", c.MailgunSender)
	_ = w.WriteField("to", to)
	_ = w.WriteField("subject", subject)
	_ = w.WriteField("html", body)
	for _, f := range files {
		if err := attachMultipart(w, f); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	endpoint := "https://api.mailgun.net/v3/" + url.PathEscape(c.MailgunDomain) + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return err
	}
	req.SetBasicAuth("api", c.MailgunKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return s.do(req, "Mailgun")
}

func attachMultipart(w *multipart.Writer, path string) error {
	f, err := os.Open(path) //nolint:gosec // 附件路径由程序内部生成
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	part, err := w.CreateFormFile("attachment", filepath.Base(path))
	if err != nil {
		return err
	}
	_, err = io.Copy(part, f)
	return err
}

func (s *Service) sendSendgrid(ctx context.Context, to, subject, body string, files []string) error {
	c := s.cfg.Mail
	payload := map[string]any{
		"personalizations": []any{map[string]any{"to": []any{map[string]string{"email": to}}}},
		"from":             map[string]string{"email": c.SendgridSender},
		"subject":          subject,
		"content":          []any{map[string]string{"type": "text/html", "value": body}},
	}
	var atts []any
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // 附件路径由程序内部生成
		if err != nil {
			return err
		}
		atts = append(atts, map[string]string{"content": base64.StdEncoding.EncodeToString(b), "filename": filepath.Base(f)})
	}
	if len(atts) > 0 {
		payload["attachments"] = atts
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.sendgrid.com/v3/mail/send", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.SendgridKey)
	req.Header.Set("Content-Type", "application/json")
	return s.do(req, "SendGrid")
}

func (s *Service) do(req *http.Request, name string) error {
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s 请求失败: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s 返回 %d: %s", name, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
