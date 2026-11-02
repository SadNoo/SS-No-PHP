package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Telegram 向管理群发送消息（原版 Utils\Telegram::Send / SendMarkdown）。
type Telegram struct {
	Enable bool
	Token  string
	ChatID int64
	Client *http.Client
}

// Send 发送纯文本消息；未启用时不发送。
func (t Telegram) Send(ctx context.Context, text string) error { return t.send(ctx, text, "") }

// SendMarkdown 发送 Markdown 格式的消息。
func (t Telegram) SendMarkdown(ctx context.Context, text string) error {
	return t.send(ctx, text, "Markdown")
}

func (t Telegram) send(ctx context.Context, text, mode string) error {
	if !t.Enable || t.Token == "" {
		return nil
	}
	c := t.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	form := url.Values{"chat_id": {strconv.FormatInt(t.ChatID, 10)}, "text": {text}}
	if mode != "" {
		form.Set("parse_mode", mode)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+t.Token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		// 错误信息中含有机器人令牌（URL），不原样返回。
		return fmt.Errorf("telegram 请求失败")
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram 返回 %d", resp.StatusCode)
	}
	return nil
}
