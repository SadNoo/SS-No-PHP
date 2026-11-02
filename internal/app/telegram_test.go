package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"

	"github.com/SadNoo/SS-No-PHP/internal/config"
	"github.com/SadNoo/SS-No-PHP/internal/telegram"
)

// fakeTG 模拟 Telegram Bot API，记录机器人发出的消息。
type fakeTG struct {
	mu    sync.Mutex
	sent  []map[string]any
	photo []byte
}

func (f *fakeTG) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/file/") {
		_, _ = w.Write(f.photo)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	f.mu.Lock()
	if method == "sendMessage" {
		f.sent = append(f.sent, body)
	}
	f.mu.Unlock()
	if method == "getFile" {
		_, _ = w.Write([]byte(`{"ok":true,"result":{"file_id":"x","file_path":"photos/a.png"}}`))
		return
	}
	_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
}

func (f *fakeTG) last(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("机器人没有回复")
	}
	m := f.sent[len(f.sent)-1]
	f.sent = nil
	return m
}

func qrPNG(t *testing.T, text string) []byte {
	t.Helper()
	m, err := qrcode.NewQRCodeWriter().Encode(text, gozxing.BarcodeFormat_QR_CODE, 300, 300, nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// tgUpdate 以 Telegram 的身份投递一条更新，返回状态码。
func (e *testEnv) tgUpdate(token string, update string) int {
	e.t.Helper()
	resp, err := http.Post(e.ts.URL+"/telegram_callback?token="+token, "application/json", strings.NewReader(update))
	if err != nil {
		e.t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func tgMsg(from, chat int64, extra string) string {
	return fmt.Sprintf(`{"update_id":1,"message":{"message_id":9,"from":{"id":%d,"first_name":"a","username":"tguser"},"chat":{"id":%d},%s}}`, from, chat, extra)
}

func TestTelegramBot(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.Telegram.Enable = true
		c.Telegram.Token = "T"
		c.Telegram.RequestToken = "req-tok"
		c.Telegram.Bot = "niuniu_bot"
	})
	f := &fakeTG{}
	api := httptest.NewServer(f)
	defer api.Close()
	e.app.TGBot = &telegram.Bot{Token: "T", BaseURL: api.URL, HTTP: api.Client()}
	ctx := context.Background()

	for _, tok := range []string{"", "wrong"} {
		if code := e.tgUpdate(tok, tgMsg(777, 777, `"text":"/ping"`)); code != http.StatusForbidden {
			t.Fatalf("令牌 %q 应被拒绝: %d", tok, code)
		}
	}
	e.tgUpdate("req-tok", tgMsg(777, 777, `"text":"/ping"`))
	if m := f.last(t); m["text"] != "Pong!您的 ID 是 777!" {
		t.Fatalf("ping: %v", m)
	}
	e.tgUpdate("req-tok", tgMsg(777, 777, `"text":"/traffic@niuniu_bot"`))
	if m := f.last(t); m["text"] != tgNotBound {
		t.Fatalf("未绑定: %v", m)
	}

	// 网页上生成绑定二维码，发送二维码图片给机器人完成绑定。
	uid := e.login("tg@example.com")
	e.page("/user/edit")
	var token string
	if err := e.app.DB.QueryRowContext(ctx, "SELECT session_content FROM telegram_session WHERE type = 0 AND user_id = ?", uid).Scan(&token); err != nil {
		t.Fatal(err)
	}
	f.photo = qrPNG(t, "mod://bind/"+token)
	e.tgUpdate("req-tok", tgMsg(777, 777, `"photo":[{"file_id":"s","file_size":1},{"file_id":"l","file_size":2}]`))
	if m := f.last(t); m["text"] != "绑定成功，邮箱：tg@example.com" {
		t.Fatalf("绑定: %v", m)
	}
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.TelegramID != 777 || u.ImType != 4 || u.ImValue != "tguser" {
		t.Fatalf("绑定未写入: %d %d %q", u.TelegramID, u.ImType, u.ImValue)
	}
	// 二维码只能使用一次。
	e.tgUpdate("req-tok", tgMsg(888, 888, `"text":"mod://bind/`+token+`"`))
	if m := f.last(t); !strings.HasPrefix(m["text"].(string), "绑定失败") {
		t.Fatalf("重复使用二维码: %v", m)
	}

	e.tgUpdate("req-tok", tgMsg(777, 777, `"text":"/traffic"`))
	if m := f.last(t); !strings.HasPrefix(m["text"].(string), "您当前的流量状况") {
		t.Fatalf("traffic: %v", m)
	}
	e.tgUpdate("req-tok", tgMsg(777, -100, `"text":"/checkin"`))
	if m := f.last(t); !strings.HasPrefix(m["text"].(string), "签到成功") {
		t.Fatalf("checkin: %v", m)
	}
	e.tgUpdate("req-tok", tgMsg(777, 777, `"text":"/checkin"`))
	if m := f.last(t); m["text"] != "您今天已经签过到了！" {
		t.Fatalf("重复签到: %v", m)
	}
	e.tgUpdate("req-tok", tgMsg(777, -100, `"text":"/rss"`))
	if m := f.last(t); m["text"] != "请私聊机器人使用该命令" {
		t.Fatalf("群组 rss: %v", m)
	}
	e.tgUpdate("req-tok", tgMsg(777, 777, `"text":"/rss"`))
	if m := f.last(t); m["reply_markup"] == nil {
		t.Fatalf("rss 应带按钮: %v", m)
	}
	e.tgUpdate("req-tok", `{"update_id":2,"callback_query":{"id":"c","from":{"id":777},"data":"?mu=2","message":{"message_id":3,"chat":{"id":777}}}}`)
	if m := f.last(t); !strings.HasPrefix(m["text"].(string), "V2ray订阅: "+e.app.Cfg.App.SubURL) || !strings.HasSuffix(m["text"].(string), "?mu=2") {
		t.Fatalf("订阅按钮: %v", m)
	}

	// 网页 Telegram 登录：在机器人中发送 6 位数字确认。
	e.get("/user/logout")
	e.page("/auth/login")
	var content string
	if err := e.app.DB.QueryRowContext(ctx, "SELECT session_content FROM telegram_session WHERE type = 1 ORDER BY id DESC LIMIT 1").Scan(&content); err != nil {
		t.Fatal(err)
	}
	_, number, _ := strings.Cut(content, "|")
	e.tgUpdate("req-tok", tgMsg(777, 777, `"text":"`+number+`"`))
	if m := f.last(t); m["text"] != "登录验证成功，邮箱：tg@example.com" {
		t.Fatalf("登录确认: %v", m)
	}
	var confirmed uint32
	_ = e.app.DB.QueryRowContext(ctx, "SELECT user_id FROM telegram_session WHERE session_content = ?", content).Scan(&confirmed)
	if confirmed != uid {
		t.Fatalf("登录会话未确认: %d", confirmed)
	}

	// 群组安静模式下不回复。
	e.app.Cfg.Telegram.GroupQuiet = true
	e.tgUpdate("req-tok", tgMsg(777, -100, `"text":"/help"`))
	f.mu.Lock()
	n := len(f.sent)
	f.mu.Unlock()
	if n != 0 {
		t.Fatal("安静模式下不应回复群组消息")
	}
}
