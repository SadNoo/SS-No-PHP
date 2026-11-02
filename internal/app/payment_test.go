package app

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

func paySign(t *testing.T, k *rsa.PrivateKey, s string) string {
	t.Helper()
	h := sha256.Sum256([]byte(s))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

// signedNotify 构造一条由“支付宝”私钥签名的异步通知。
func signedNotify(t *testing.T, k *rsa.PrivateKey, tradeNo, amount string) url.Values {
	t.Helper()
	f := url.Values{"app_id": {"2021"}, "out_trade_no": {tradeNo}, "total_amount": {amount}, "trade_status": {"TRADE_SUCCESS"}, "notify_type": {"trade_status_sync"}}
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+f.Get(k))
	}
	f.Set("sign", paySign(t, k, strings.Join(parts, "&")))
	f.Set("sign_type", "RSA2")
	return f
}

// notify 以支付宝服务器的身份（无 Cookie、无 Origin）投递通知。
func (e *testEnv) notify(f url.Values) string {
	e.t.Helper()
	resp, err := http.PostForm(e.ts.URL+"/payment/notify", f)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestAlipayF2F(t *testing.T) {
	merchant, _ := rsa.GenerateKey(rand.Reader, 2048)
	ali, _ := rsa.GenerateKey(rand.Reader, 2048)
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		resp := `{"code":"10000","msg":"Success","qr_code":"https://qr.alipay.com/test"}`
		_, _ = fmt.Fprintf(w, `{"alipay_trade_precreate_response":%s,"sign":%q}`, resp, paySign(t, ali, resp))
	}))
	defer gw.Close()
	pub, _ := x509.MarshalPKIXPublicKey(&ali.PublicKey)
	e := newEnv(t, func(c *config.Config) {
		c.Payment.System = "f2fpay"
		c.Payment.F2FAppID = "2021"
		c.Payment.F2FMerchantPrivateKey = base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(merchant))
		c.Payment.F2FAlipayPublicKey = base64.StdEncoding.EncodeToString(pub)
		c.Payment.F2FGateway = gw.URL
		c.Users.CodePayback = 20
	})
	if e.app.F2F == nil {
		t.Fatal("当面付未初始化")
	}
	ctx := context.Background()
	ref := e.login("p0@example.com")
	e.get("/user/logout")
	uid := e.login("p1@example.com")
	e.exec("UPDATE user SET ref_by = ? WHERE id = ?", ref, uid)

	if !strings.Contains(e.page("/user/code"), "支付宝在线充值") {
		t.Fatal("充值页面没有显示当面付")
	}
	for _, bad := range []string{"", "-5", "abc", "1.234", "0"} {
		if r := e.post("/user/payment/purchase", url.Values{"amount": {bad}}); r["ret"] != float64(0) {
			t.Errorf("金额 %q 应被拒绝: %v", bad, r)
		}
	}
	r := e.post("/user/payment/purchase", url.Values{"amount": {"12.5"}})
	if r["ret"] != float64(1) || r["qrcode"] != "https://qr.alipay.com/test" || r["amount"] != 12.5 {
		t.Fatalf("下单失败: %v", r)
	}
	pid := r["pid"].(string)
	if s := e.post("/payment/status", url.Values{"pid": {pid}}); s["result"] != float64(0) {
		t.Fatalf("未支付状态错误: %v", s)
	}

	// 伪造签名、金额不符都不入账。
	forged := signedNotify(t, merchant, pid, "12.50")
	if got := e.notify(forged); got != "fail" {
		t.Errorf("伪造签名应返回 fail: %q", got)
	}
	if got := e.notify(signedNotify(t, ali, pid, "0.01")); got != "fail" {
		t.Errorf("金额不符应返回 fail: %q", got)
	}

	// 重复通知并发到达只入账一次（K45）。
	good := signedNotify(t, ali, pid, "12.50")
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := e.notify(good); got != "success" {
				t.Errorf("通知应返回 success: %q", got)
			}
		}()
	}
	wg.Wait()
	u, _ := e.app.Q.GetUser(ctx, uid)
	rf, _ := e.app.Q.GetUser(ctx, ref)
	if u.Money.String() != "12.50" || rf.Money.String() != "2.50" {
		t.Fatalf("应只入账一次: 用户 %s，邀请人 %s", u.Money, rf.Money)
	}
	if s := e.post("/payment/status", url.Values{"pid": {pid}}); s["result"] != float64(1) {
		t.Fatalf("已支付状态错误: %v", s)
	}
	if !strings.Contains(e.page("/user/code"), "支付宝当面付 "+pid) {
		t.Error("充值记录缺少当面付订单")
	}

	// 其他用户不能查询该订单（K46）。
	e.get("/user/logout")
	e.login("p2@example.com")
	if s := e.post("/payment/status", url.Values{"pid": {pid}}); s["result"] != float64(0) {
		t.Fatalf("不应能查询他人订单: %v", s)
	}
}

// TestPurchaseNotice 不开放在线支付时，在充值、套餐页面显示购买说明（逐行显示并转义）。
func TestPurchaseNotice(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Payment.Notice = "添加微信 abc<b> 好友购买套餐\n第二行" })
	e.login("n@example.com")
	for _, p := range []string{"/user/shop", "/user/code"} {
		body := e.page(p)
		if !strings.Contains(body, "<p>添加微信 abc&lt;b&gt; 好友购买套餐</p>") || !strings.Contains(body, "<p>第二行</p>") {
			t.Errorf("%s 缺少购买说明", p)
		}
		if strings.Contains(body, "支付宝在线充值") {
			t.Errorf("%s 未开启在线支付时不应显示支付组件", p)
		}
	}
	e2 := newEnv(t)
	e2.login("m@example.com")
	if strings.Contains(e2.page("/user/shop"), "购买说明") {
		t.Error("未设置时不应显示购买说明")
	}
}
