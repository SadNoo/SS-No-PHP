// Package captcha 对应原版的 reCAPTCHA / 极验（Geetest v3）验证码。
package captcha

import (
	"context"
	"crypto/md5" //nolint:gosec // 极验协议规定使用 md5
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

// Scene 是使用验证码的场景。
type Scene int

const (
	Login Scene = iota
	Register
	Checkin
)

// Geetest 是渲染极验组件所需的数据（原版模板中的 $geetest_html）。
type Geetest struct {
	Success   int    `json:"success"`
	GT        string `json:"gt"`
	Challenge string `json:"challenge"`
}

// View 是页面渲染验证码所需的数据。两者最多一个非空。
type View struct {
	RecaptchaSitekey string
	Geetest          *Geetest
}

// Service 生成和校验验证码。
type Service struct {
	cfg  config.Captcha
	sm   *scs.SessionManager
	http *http.Client
	log  *slog.Logger
	// 便于测试替换的接口地址。
	recaptchaURL, geetestRegisterURL, geetestValidateURL string
}

// New 创建验证码服务。
func New(cfg config.Captcha, sm *scs.SessionManager, log *slog.Logger) *Service {
	return &Service{
		cfg: cfg, sm: sm, log: log,
		http:               &http.Client{Timeout: 6 * time.Second},
		recaptchaURL:       "https://recaptcha.net/recaptcha/api/siteverify",
		geetestRegisterURL: "https://api.geetest.com/register.php",
		geetestValidateURL: "https://api.geetest.com/validate.php",
	}
}

// Enabled 返回该场景是否需要验证码。
func (s *Service) Enabled(scene Scene) bool {
	switch scene {
	case Login:
		return s.cfg.EnableLogin
	case Register:
		return s.cfg.EnableReg
	case Checkin:
		return s.cfg.EnableCheckin
	}
	return false
}

// Prepare 为页面生成验证码数据；场景未启用时返回空 View。
func (s *Service) Prepare(ctx context.Context, scene Scene) View {
	if !s.Enabled(scene) {
		return View{}
	}
	return s.PrepareAlways(ctx)
}

// PrepareAlways 不论场景开关都生成验证码数据（原版 getCaptcha 接口的行为）。
func (s *Service) PrepareAlways(ctx context.Context) View {
	switch s.cfg.Provider {
	case "recaptcha":
		return View{RecaptchaSitekey: s.cfg.RecaptchaSitekey}
	case "geetest":
		g := s.geetestRegister(ctx)
		return View{Geetest: &g}
	}
	return View{}
}

// Verify 校验表单中的验证码字段；场景未启用时直接通过。
func (s *Service) Verify(ctx context.Context, scene Scene, form url.Values, remoteIP string) bool {
	if !s.Enabled(scene) {
		return true
	}
	switch s.cfg.Provider {
	case "recaptcha":
		return s.verifyRecaptcha(ctx, form.Get("recaptcha"), remoteIP)
	case "geetest":
		return s.verifyGeetest(ctx, form.Get("geetest_challenge"), form.Get("geetest_validate"), form.Get("geetest_seccode"))
	}
	return false
}

func (s *Service) verifyRecaptcha(ctx context.Context, response, remoteIP string) bool {
	if response == "" {
		return false
	}
	form := url.Values{"secret": {s.cfg.RecaptchaSecret}, "response": {response}, "remoteip": {remoteIP}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.recaptchaURL, strings.NewReader(form.Encode()))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		s.log.Warn("reCAPTCHA 校验请求失败", "err", err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return false
	}
	return out.Success
}

const (
	sessGeetestStatus = "gt_status"
	sessGeetestUser   = "gt_user"
)

func md5hex(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec // 极验协议规定的算法
	return hex.EncodeToString(sum[:])
}

// geetestRegister 对应 GeetestLib::pre_process。
func (s *Service) geetestRegister(ctx context.Context) Geetest {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	userID := hex.EncodeToString(b)
	u := s.geetestRegisterURL + "?gt=" + url.QueryEscape(s.cfg.GeetestID) + "&user_id=" + userID
	challenge := ""
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil); err == nil {
		if resp, err := s.http.Do(req); err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			_ = resp.Body.Close()
			challenge = strings.TrimSpace(string(body))
		} else {
			s.log.Warn("极验注册请求失败", "err", err)
		}
	}
	s.sm.Put(ctx, sessGeetestUser, userID)
	if len(challenge) != 32 {
		// 极验服务器不可用。原版此时进入“宕机模式”，其校验算法可在客户端计算、等于没有验证码；
		// 这里记录状态，校验时直接拒绝（K16）。
		s.sm.Put(ctx, sessGeetestStatus, 0)
		r := make([]byte, 17)
		_, _ = rand.Read(r)
		return Geetest{Success: 0, GT: s.cfg.GeetestID, Challenge: hex.EncodeToString(r)[:34]}
	}
	s.sm.Put(ctx, sessGeetestStatus, 1)
	return Geetest{Success: 1, GT: s.cfg.GeetestID, Challenge: md5hex(challenge + s.cfg.GeetestKey)}
}

// verifyGeetest 对应 GeetestLib::success_validate。
func (s *Service) verifyGeetest(ctx context.Context, challenge, validate, seccode string) bool {
	if s.sm.GetInt(ctx, sessGeetestStatus) != 1 {
		s.log.Warn("极验服务器不可用，拒绝本次验证")
		return false
	}
	if len(validate) != 32 || validate != md5hex(s.cfg.GeetestKey+"geetest"+challenge) {
		return false
	}
	form := url.Values{"seccode": {seccode}, "sdk": {"php_3.2.0"}, "user_id": {s.sm.GetString(ctx, sessGeetestUser)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.geetestValidateURL, strings.NewReader(form.Encode()))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		s.log.Warn("极验校验请求失败", "err", err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return strings.TrimSpace(string(body)) == md5hex(seccode)
}

// ScriptSources 返回页面需要加载的第三方脚本来源，用于 CSP 白名单。
func ScriptSources(cfg config.Captcha) []string {
	switch cfg.Provider {
	case "recaptcha":
		return []string{"https://recaptcha.net", "https://www.gstatic.com", "https://www.recaptcha.net"}
	case "geetest":
		return []string{"https://static.geetest.com", "https://api.geetest.com"}
	}
	return nil
}

// FrameSources 返回验证码组件使用的 iframe 来源。
func FrameSources(cfg config.Captcha) []string {
	if cfg.Provider == "recaptcha" {
		return []string{"https://recaptcha.net", "https://www.recaptcha.net", "https://www.google.com"}
	}
	return nil
}
