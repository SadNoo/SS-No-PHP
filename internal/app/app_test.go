package app

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/auth"
	"github.com/SadNoo/SS-No-PHP/internal/captcha"
	"github.com/SadNoo/SS-No-PHP/internal/config"
	"github.com/SadNoo/SS-No-PHP/internal/server"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/testdb"
)

// fakeMail 记录发出的邮件。
type fakeMail struct {
	mu   sync.Mutex
	sent []map[string]any
}

func (f *fakeMail) Send(_ context.Context, to, subject, tmpl string, data map[string]any, _ ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, map[string]any{"to": to, "subject": subject, "tmpl": tmpl, "data": data})
	return nil
}

type testEnv struct {
	t    *testing.T
	ts   *httptest.Server
	c    *http.Client
	app  *App
	mail *fakeMail
}

func newEnv(t *testing.T, mutate ...func(*config.Config)) *testEnv {
	t.Helper()
	pool := testdb.New(t)
	cfg := config.Default()
	cfg.App.Key = "test-key-0123456789abcdef"
	cfg.Security.CookieSecure = false
	cfg.Security.LegacySalt = "salt"
	for _, m := range mutate {
		m(&cfg)
	}
	logOut := io.Discard
	if os.Getenv("NIUNIU_TEST_LOG") != "" { // 调试时输出日志
		logOut = os.Stderr
	}
	log := slog.New(slog.NewTextHandler(logOut, nil))
	srv, err := server.New(cfg, pool, log, server.Options{})
	if err != nil {
		t.Fatal(err)
	}
	am := &auth.Manager{SM: srv.Sessions, Q: store.New(pool), Loc: srv.Loc, Log: log}
	fm := &fakeMail{}
	a := New(srv, am, fm, captcha.New(cfg.Captcha, srv.Sessions, log))
	srv.Init(am.LoadUser)
	srv.Mount(a)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	srv.Cfg.App.BaseURL = ts.URL
	return &testEnv{t: t, ts: ts, c: c, app: a, mail: fm}
}

func (e *testEnv) post(path string, form url.Values) map[string]any {
	e.t.Helper()
	resp, err := e.c.PostForm(e.ts.URL+path, form)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		e.t.Fatalf("%s 返回的不是 JSON（%d）", path, resp.StatusCode)
	}
	return out
}

func (e *testEnv) get(path string) *http.Response {
	e.t.Helper()
	resp, err := e.c.Get(e.ts.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}

func regForm(email string) url.Values {
	return url.Values{"name": {"测试"}, "email": {email}, "passwd": {"Passw0rd!x"}, "repasswd": {"Passw0rd!x"}, "imtype": {"2"}, "wechat": {email}}
}

func TestRegisterLoginLogout(t *testing.T) {
	e := newEnv(t)
	if r := e.post("/auth/register", regForm("A@Example.com")); r["ret"] != float64(1) {
		t.Fatalf("注册失败: %v", r)
	}
	if r := e.post("/auth/register", regForm("a@example.com")); r["msg"] != "邮箱已经被注册了" {
		t.Fatalf("重复注册应被拒绝: %v", r)
	}
	// 不存在的邮箱与错误密码返回相同提示（K19）。
	r1 := e.post("/auth/login", url.Values{"email": {"none@example.com"}, "passwd": {"x"}})
	r2 := e.post("/auth/login", url.Values{"email": {"a@example.com"}, "passwd": {"wrong"}})
	if r1["msg"] != r2["msg"] || r1["ret"] != float64(0) {
		t.Fatalf("登录失败提示应一致: %v / %v", r1, r2)
	}
	if r := e.post("/auth/login", url.Values{"email": {"a@example.com"}, "passwd": {"Passw0rd!x"}}); r["ret"] != float64(1) {
		t.Fatalf("登录失败: %v", r)
	}
	if resp := e.get("/auth/login"); resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/user" {
		t.Fatalf("已登录访问登录页应跳转用户中心: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	e.get("/user/logout")
	if resp := e.get("/user/logout"); resp.Header.Get("Location") != "/auth/login" {
		t.Fatalf("退出后应视为未登录: %s", resp.Header.Get("Location"))
	}
}

func TestRegisterInviteMode(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Register.Mode = "invite" })
	if r := e.post("/auth/register", regForm("b@example.com")); r["msg"] != "邀请码无效" {
		t.Fatalf("邀请模式下无邀请码应被拒绝: %v", r)
	}
}

// TestLegacyPasswordUpgrade 验证原版 md5+salt 密码可以登录，登录后自动升级为 argon2id（K08）。
func TestLegacyPasswordUpgrade(t *testing.T) {
	e := newEnv(t)
	sum := md5.Sum([]byte("oldpass123" + "salt"))
	ctx := context.Background()
	_, err := e.app.DB.ExecContext(ctx, `INSERT INTO user (user_name,email,pass,passwd,reg_date,im_value,pac,remark,forbidden_ip,forbidden_port,disconnect_ip)
		VALUES ('old','old@example.com',?,'p',UTC_TIMESTAMP(),'','','','','','')`, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	if r := e.post("/auth/login", url.Values{"email": {"old@example.com"}, "passwd": {"oldpass123"}}); r["ret"] != float64(1) {
		t.Fatalf("旧密码登录失败: %v", r)
	}
	u, _ := e.app.Q.GetUserByEmail(ctx, "old@example.com")
	if !strings.HasPrefix(u.Pass, "$argon2id$") {
		t.Fatalf("密码未升级: %s", u.Pass)
	}
}

// TestPasswordResetSingleUse 验证重置链接只能使用一次（K18），且不存在的邮箱提示一致（K19）。
func TestPasswordResetSingleUse(t *testing.T) {
	e := newEnv(t)
	e.post("/auth/register", regForm("c@example.com"))
	r1 := e.post("/password/reset", url.Values{"email": {"nobody@example.com"}})
	r2 := e.post("/password/reset", url.Values{"email": {"c@example.com"}})
	if r1["msg"] != r2["msg"] {
		t.Fatalf("提示应一致: %v %v", r1, r2)
	}
	if len(e.mail.sent) != 1 {
		t.Fatalf("应只给已注册邮箱发信，实际 %d 封", len(e.mail.sent))
	}
	link := e.mail.sent[0]["data"].(map[string]any)["resetUrl"].(string)
	path := strings.TrimPrefix(link, e.ts.URL)
	form := url.Values{"password": {"NewPassw0rd"}, "repasswd": {"NewPassw0rd"}}
	if r := e.post(path, form); r["ret"] != float64(1) {
		t.Fatalf("重置失败: %v", r)
	}
	if r := e.post(path, form); r["ret"] != float64(0) {
		t.Fatalf("重置链接应只能使用一次: %v", r)
	}
	if r := e.post("/auth/login", url.Values{"email": {"c@example.com"}, "passwd": {"NewPassw0rd"}}); r["ret"] != float64(1) {
		t.Fatalf("新密码登录失败: %v", r)
	}
}

func TestTelegramOAuthCheck(t *testing.T) {
	now := time.Unix(1700000000, 0)
	q := url.Values{"id": {"42"}, "first_name": {"a"}, "auth_date": {"1700000000"}}
	data := "auth_date=1700000000\nfirst_name=a\nid=42"
	secret := sha256.Sum256([]byte("bot-token"))
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(data))
	q.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	if !telegramOAuthCheck(q, "bot-token", now) {
		t.Fatal("合法签名应通过")
	}
	if telegramOAuthCheck(q, "other", now) {
		t.Fatal("错误的机器人令牌不应通过")
	}
	if telegramOAuthCheck(q, "bot-token", now.Add(10*time.Minute)) {
		t.Fatal("超过 5 分钟不应通过")
	}
}
