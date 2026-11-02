// Package alipay 实现支付宝当面付（alipay.trade.precreate）与异步通知验签，对应原版 Omnipay Alipay_AopF2F。
//
// 签名规则（RSA2）：除 sign 外的参数按键名升序排列，拼接为 k=v&k=v（值不做 URL 编码），
// 用 SHA256WithRSA（PKCS#1 v1.5）签名后 Base64 编码。异步通知验签时还需去掉 sign_type。
package alipay

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Client 是当面付客户端。
type Client struct {
	AppID     string
	Gateway   string
	NotifyURL string
	priv      *rsa.PrivateKey
	alipayPub *rsa.PublicKey
	HTTP      *http.Client
	loc       *time.Location
}

// New 创建客户端。密钥可以是 PEM，也可以是去掉头尾的 Base64（支付宝开放平台下载的格式）。
func New(appID, privateKey, alipayPublicKey, gateway, notifyURL string) (*Client, error) {
	priv, err := parsePrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("应用私钥无效: %w", err)
	}
	pub, err := parsePublicKey(alipayPublicKey)
	if err != nil {
		return nil, fmt.Errorf("支付宝公钥无效: %w", err)
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return &Client{AppID: appID, Gateway: gateway, NotifyURL: notifyURL, priv: priv, alipayPub: pub,
		HTTP: &http.Client{Timeout: 15 * time.Second}, loc: loc}, nil
}

func pemBlock(s, kind string) []byte {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "-----BEGIN") {
		var b strings.Builder
		b.WriteString("-----BEGIN " + kind + "-----\n")
		for len(s) > 64 {
			b.WriteString(s[:64] + "\n")
			s = s[64:]
		}
		b.WriteString(s + "\n-----END " + kind + "-----\n")
		s = b.String()
	}
	if blk, _ := pem.Decode([]byte(s)); blk != nil {
		return blk.Bytes
	}
	return nil
}

func parsePrivateKey(s string) (*rsa.PrivateKey, error) {
	der := pemBlock(s, "PRIVATE KEY")
	if der == nil {
		return nil, errors.New("无法解析 PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("不是 RSA 私钥")
	}
	return rk, nil
}

func parsePublicKey(s string) (*rsa.PublicKey, error) {
	der := pemBlock(s, "PUBLIC KEY")
	if der == nil {
		return nil, errors.New("无法解析 PEM")
	}
	if k, err := x509.ParsePKIXPublicKey(der); err == nil {
		if rk, ok := k.(*rsa.PublicKey); ok {
			return rk, nil
		}
		return nil, errors.New("不是 RSA 公钥")
	}
	return x509.ParsePKCS1PublicKey(der)
}

// signContent 生成待签名字符串。
func signContent(params url.Values, skip ...string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		skipped := false
		for _, s := range skip {
			skipped = skipped || k == s
		}
		if !skipped && params.Get(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params.Get(k))
	}
	return strings.Join(parts, "&")
}

func (c *Client) sign(content string) (string, error) {
	h := sha256.Sum256([]byte(content))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.priv, crypto.SHA256, h[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func (c *Client) verify(content, sig string) bool {
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	h := sha256.Sum256([]byte(content))
	return rsa.VerifyPKCS1v15(c.alipayPub, crypto.SHA256, h[:], raw) == nil
}

// Precreate 调用 alipay.trade.precreate 创建订单，返回收款二维码内容。
func (c *Client) Precreate(ctx context.Context, outTradeNo, subject, totalAmount string) (string, error) {
	biz, err := json.Marshal(map[string]string{"subject": subject, "out_trade_no": outTradeNo, "total_amount": totalAmount})
	if err != nil {
		return "", err
	}
	params := url.Values{
		"app_id": {c.AppID}, "method": {"alipay.trade.precreate"}, "format": {"JSON"}, "charset": {"utf-8"},
		"sign_type": {"RSA2"}, "timestamp": {time.Now().In(c.loc).Format("2006-01-02 15:04:05")}, "version": {"1.0"},
		"notify_url": {c.NotifyURL}, "biz_content": {string(biz)},
	}
	sig, err := c.sign(signContent(params, "sign"))
	if err != nil {
		return "", err
	}
	params.Set("sign", sig)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Gateway, strings.NewReader(params.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求支付宝失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return c.parsePrecreate(body)
}

// parsePrecreate 解析并验签支付宝的返回。签名针对 alipay_trade_precreate_response 的原始 JSON 文本。
func (c *Client) parsePrecreate(body []byte) (string, error) {
	var outer struct {
		Resp json.RawMessage `json:"alipay_trade_precreate_response"`
		Sign string          `json:"sign"`
	}
	if err := json.Unmarshal(body, &outer); err != nil {
		return "", fmt.Errorf("支付宝返回格式错误: %w", err)
	}
	var r struct {
		Code    string `json:"code"`
		Msg     string `json:"msg"`
		SubMsg  string `json:"sub_msg"`
		QRCode  string `json:"qr_code"`
		TradeNo string `json:"out_trade_no"`
	}
	if err := json.Unmarshal(outer.Resp, &r); err != nil {
		return "", fmt.Errorf("支付宝返回格式错误: %w", err)
	}
	if r.Code != "10000" {
		return "", fmt.Errorf("支付宝返回错误 %s %s %s", r.Code, r.Msg, r.SubMsg)
	}
	if outer.Sign == "" || !c.verify(string(outer.Resp), outer.Sign) {
		return "", errors.New("支付宝返回的签名无效")
	}
	return r.QRCode, nil
}

// Notification 是验签通过的异步通知。
type Notification struct {
	OutTradeNo  string
	TotalAmount string
	TradeStatus string
}

// Paid 是否已支付（TRADE_SUCCESS / TRADE_FINISHED，对应 Omnipay isPaid）。
func (n Notification) Paid() bool {
	return n.TradeStatus == "TRADE_SUCCESS" || n.TradeStatus == "TRADE_FINISHED"
}

// VerifyNotify 校验异步通知的签名与 app_id。
func (c *Client) VerifyNotify(form url.Values) (Notification, error) {
	if form.Get("app_id") != c.AppID {
		return Notification{}, errors.New("app_id 不匹配")
	}
	if !c.verify(signContent(form, "sign", "sign_type"), form.Get("sign")) {
		return Notification{}, errors.New("签名无效")
	}
	return Notification{OutTradeNo: form.Get("out_trade_no"), TotalAmount: form.Get("total_amount"), TradeStatus: form.Get("trade_status")}, nil
}
