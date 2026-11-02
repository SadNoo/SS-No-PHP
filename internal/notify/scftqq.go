// Package notify 发送管理员通知（原版 Server 酱 ScFtqq 推送）。
package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ScFtqq 通过 Server 酱推送消息（原版 file_get_contents('https://sc.ftqq.com/' . SCKEY . '.send')）。
type ScFtqq struct {
	Key    string
	Client *http.Client
}

// Send 推送标题与内容。Key 为空时不发送。
func (s ScFtqq) Send(ctx context.Context, text, desp string) error {
	if s.Key == "" {
		return nil
	}
	c := s.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	form := url.Values{"text": {text}, "desp": {desp}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://sc.ftqq.com/"+url.PathEscape(s.Key)+".send", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("server 酱返回 %d", resp.StatusCode)
	}
	return nil
}
