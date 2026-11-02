package alipay

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func genKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func rsaSign(t *testing.T, k *rsa.PrivateKey, s string) string {
	t.Helper()
	h := sha256.Sum256([]byte(s))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func pubB64(t *testing.T, k *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

func TestKeyFormats(t *testing.T) {
	k := genKey(t)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	pkcs1 := x509.MarshalPKCS1PrivateKey(k)
	for name, s := range map[string]string{
		"PKCS1 PEM":    string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: pkcs1})),
		"PKCS8 PEM":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})),
		"PKCS1 Base64": base64.StdEncoding.EncodeToString(pkcs1),
		"PKCS8 Base64": base64.StdEncoding.EncodeToString(pkcs8),
	} {
		if _, err := parsePrivateKey(s); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := parsePublicKey(pubB64(t, k)); err != nil {
		t.Error(err)
	}
	if _, err := parsePrivateKey("not a key"); err == nil {
		t.Error("无效私钥应报错")
	}
}

func TestSignContent(t *testing.T) {
	v := url.Values{"b": {"2"}, "a": {"1"}, "sign": {"x"}, "sign_type": {"RSA2"}, "empty": {""}}
	if got := signContent(v, "sign", "sign_type"); got != "a=1&b=2" {
		t.Fatalf("got %q", got)
	}
}

func TestPrecreateAndNotify(t *testing.T) {
	merchant, ali := genKey(t), genKey(t)
	var gotBiz string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		h := sha256.Sum256([]byte(signContent(r.PostForm, "sign")))
		sig, _ := base64.StdEncoding.DecodeString(r.PostForm.Get("sign"))
		if rsa.VerifyPKCS1v15(&merchant.PublicKey, crypto.SHA256, h[:], sig) != nil {
			http.Error(w, "bad sign", 400)
			return
		}
		gotBiz = r.PostForm.Get("biz_content")
		resp := `{"code":"10000","msg":"Success","out_trade_no":"AB12CD34","qr_code":"https://qr.alipay.com/x"}`
		_, _ = fmt.Fprintf(w, `{"alipay_trade_precreate_response":%s,"sign":%q}`, resp, rsaSign(t, ali, resp))
	}))
	defer gw.Close()
	c, err := New("2021", base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(merchant)), pubB64(t, ali), gw.URL, "https://p/notify")
	if err != nil {
		t.Fatal(err)
	}
	qr, err := c.Precreate(context.Background(), "AB12CD34", "AB12CD34", "10.00")
	if err != nil || qr != "https://qr.alipay.com/x" {
		t.Fatalf("qr=%q err=%v", qr, err)
	}
	if !strings.Contains(gotBiz, `"total_amount":"10.00"`) {
		t.Errorf("biz_content=%s", gotBiz)
	}

	// 返回签名不对时拒绝。
	if _, err := c.parsePrecreate([]byte(`{"alipay_trade_precreate_response":{"code":"10000","qr_code":"evil"},"sign":"` + rsaSign(t, merchant, "x") + `"}`)); err == nil {
		t.Error("伪造的返回应被拒绝")
	}

	form := url.Values{"app_id": {"2021"}, "out_trade_no": {"AB12CD34"}, "total_amount": {"10.00"}, "trade_status": {"TRADE_SUCCESS"}, "sign_type": {"RSA2"}}
	form.Set("sign", rsaSign(t, ali, signContent(form, "sign", "sign_type")))
	n, err := c.VerifyNotify(form)
	if err != nil || !n.Paid() || n.OutTradeNo != "AB12CD34" {
		t.Fatalf("n=%+v err=%v", n, err)
	}
	form.Set("total_amount", "1000.00")
	if _, err := c.VerifyNotify(form); err == nil {
		t.Error("篡改后的通知应验签失败")
	}
	form.Set("app_id", "other")
	if _, err := c.VerifyNotify(form); err == nil {
		t.Error("app_id 不匹配应失败")
	}
}
