package sub

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// extend never rewrites a complete JSON configuration or adds selectable fake
// nodes to its groups. JSON retains its native fields / response header.
// Legacy URI feeds can carry display-only entries, appended after real nodes.
func withSubscriptionInfo(body string, r Request, u store.User, _ time.Time, loc *time.Location) string {
	if body == "" || r.Filter.Extend == nil || !*r.Filter.Extend {
		return body
	}
	info := "剩余流量：" + util.FlowAutoShow(float64(u.TransferEnable-u.U-u.D)) + "；" + expiryText(u, loc)
	if r.Format == "sing-box" || r.Format == "clash" {
		return body
	}
	if r.Format == "surge" || r.Format == "surfboard" || r.Format == "loon" || r.Format == "quantumultx" || r.Format == "quantumult" && !r.List && (r.Version == "2" || r.Version == "3") {
		return "# " + info + "\n" + body
	}
	if r.Format == "vless" || r.Format == "anytls" {
		return body // Their metadata remains in Subscription-Userinfo, not a fake TLS endpoint.
	}
	plain, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
	if err != nil {
		plain, err = base64.RawURLEncoding.DecodeString(strings.TrimSpace(body))
	}
	if r.List && r.Format == "ss" {
		plain, err = []byte(body), nil
	}
	if err != nil {
		return body
	}
	p := Proxy{Name: info, Kind: "ss", Server: "127.0.0.1", Port: 1, Method: SS2022Method, Password: "subscription-info-only"}
	if r.Format == "ss2022" || r.Format == "ss" {
		p.Method = SS2022Method
		key := base64.StdEncoding.EncodeToString(make([]byte, 32))
		p.Password = key + ":" + key
	}
	if r.Format == "vmess" || r.Format == "v2ray" || r.Format == "quantumult" || r.Format == "kitsunebi" {
		p.Kind, p.Transport, p.UUID = "vmess", "tcp", "00000000-0000-4000-8000-000000000001"
		p.V2 = newV2()
		for _, field := range []struct {
			key string
			val any
		}{{"v", "2"}, {"ps", info}, {"add", p.Server}, {"port", p.Port}, {"id", p.UUID}, {"aid", 0}, {"net", "tcp"}, {"type", "none"}, {"host", ""}, {"path", ""}, {"tls", ""}} {
			p.V2.Set(field.key, field.val)
		}
	}
	line := p.URI()
	switch r.Format {
	case "kitsunebi":
		line = kitsunebiURI(p)
	case "quantumult":
		line = "vmess://" + base64.StdEncoding.EncodeToString([]byte(quantumultLine(p)))
	case "shadowrocket":
		line = "STATUS=" + info
	}
	plain = append([]byte(strings.TrimRight(string(plain), "\n")+"\n"), []byte(line+"\n")...)
	if r.List && r.Format == "ss" {
		return string(plain)
	}
	return base64.StdEncoding.EncodeToString(plain)
}
