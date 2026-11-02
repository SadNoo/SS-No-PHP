package sub

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

var ErrLegacySSRetired = errors.New("传统 SS / SSD 及仅支持传统 SS 的旧格式已停用；SS 统一使用 SS2022")

var ErrSSRRetired = errors.New("SSR/ClashR 订阅已停用，请选择 SS、VMess 或其他现有客户端格式")

// Request keeps legacy mu subscriptions separate from the migrated client formats.
type Request struct {
	Format, Version, SurgeVersion string
	Tailscale, QUIC, List         bool
	Mu                            int
	Filter                        Filter
	Query                         url.Values
}

func ParseRequest(q url.Values) (Request, error) {
	r := Request{Format: "legacy", Version: "1.14", SurgeVersion: "5"}
	keys := map[string]bool{"type": true, "class": true, "noclass": true, "regex": true, "emoji": true, "extend": true, "source": true, "profiles": true, "mu": true, "kitsunebi": true, "surfboard": true, "quantumult": true, "sing-box": true, "sb_version": true, "tailscale": true, "quic": true, "ss2022": true, "vless": true, "anytls": true, "v2ray": true, "clash": true, "surge": true, "loon": true, "quantumultx": true, "shadowrocket": true, "sub": true, "ssd": true, "list": true}
	for key, values := range q {
		base := strings.Split(key, "[")[0]
		if keys[base] && (key != base || len(values) != 1) {
			return r, errors.New("订阅参数重复或格式无效")
		}
	}
	selected := 0
	for _, key := range []string{"mu", "kitsunebi", "surfboard", "quantumult", "sing-box", "ss2022", "vless", "anytls", "v2ray", "clash", "surge", "loon", "quantumultx", "shadowrocket", "sub", "ssd", "list"} {
		v, exists := q[key]
		if !exists {
			continue
		}
		selected++
		value := v[0]
		switch key {
		case "mu":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 4 {
				return r, errors.New("mu 无效")
			}
			if n == MuNormal || n == MuSingle {
				return r, ErrSSRRetired
			}
			if n == MuSSD {
				return r, ErrLegacySSRetired
			}
			r.Mu = n
		case "list":
			r.Format = value
			r.List = true
			if value == "clashr" || value == "ssr" {
				return r, ErrSSRRetired
			}
			if value == "ssd" || value == "ssa" || value == "quantumult-ss" {
				return r, ErrLegacySSRetired
			}
			if !contains([]string{"ss", "surge", "clash", "v2rayn", "v2ray", "vmess", "loon", "quantumult", "quantumultx", "shadowrocket", "ss2022", "vless", "anytls", "kitsunebi", "surfboard"}, value) {
				return r, errors.New("不支持的节点列表格式")
			}
			if value == "v2rayn" {
				r.Format = "v2ray"
			}
		case "surge":
			if contains([]string{"1", "2", "3"}, value) {
				return r, ErrLegacySSRetired
			}
			if !contains([]string{"4", "5"}, value) {
				return r, errors.New("surge 版本无效")
			}
			r.Format, r.SurgeVersion = key, value
		case "quantumult":
			if !contains([]string{"1", "2", "3"}, value) {
				return r, errors.New("quantumult 模式无效")
			}
			r.Format, r.Version = key, value
		case "clash":
			if value == "2" {
				return r, ErrSSRRetired
			}
			if value != "1" {
				return r, errors.New("Clash 模式无效")
			}
			r.Format = key
		case "ssd":
			return r, ErrLegacySSRetired
		case "sub":
			switch value {
			case "1":
				return r, ErrSSRRetired
			case "2":
				r.Format = "ss"
			case "3":
				r.Format = "vmess"
			default:
				return r, errors.New("sub 无效")
			}
		default:
			if value != "1" {
				return r, errors.New("订阅开关应为 1")
			}
			r.Format = key
		}
	}
	if selected == 0 {
		return r, ErrSSRRetired
	}
	if selected > 1 {
		return r, errors.New("请只选择一种订阅格式")
	}
	switch r.Format {
	case "legacy", "sing-box", "ss2022", "vless", "anytls", "v2ray", "clash", "surge", "loon", "quantumultx", "shadowrocket", "ss", "vmess", "quantumult", "kitsunebi", "surfboard":
	default:
		return r, errors.New("不支持的订阅格式")
	}
	for key, dst := range map[string]*bool{"quic": &r.QUIC, "tailscale": &r.Tailscale} {
		if v, ok := q[key]; ok {
			if r.Format != "sing-box" || (v[0] != "0" && v[0] != "1") {
				return r, errors.New("sing-box 模式参数无效")
			}
			*dst = v[0] == "1"
		}
	}
	if v, ok := q["sb_version"]; ok {
		if r.Format != "sing-box" || (v[0] != "1.14" && v[0] != "1.15") {
			return r, errors.New("sing-box 版本无效")
		}
		r.Version = v[0]
	}
	for _, key := range []string{"source", "profiles"} {
		if q.Get(key) != "" {
			return r, errors.New("旧远程模板/策略编号不受支持，请使用面板内置配置")
		}
	}
	var err error
	r.Filter, err = parseFilter(q)
	if err != nil {
		return r, err
	}
	r.Query = make(url.Values, len(q))
	for key, values := range q {
		r.Query[key] = append([]string(nil), values...)
	}
	return r, nil
}

func (s *Service) Render(ctx context.Context, u store.User, r Request, now time.Time) (string, error) {
	if !activeUser(u, now) {
		return "", nil
	}
	if r.Format == "legacy" {
		if r.Mu == MuNormal || r.Mu == MuSingle {
			return "", ErrSSRRetired
		}
		if r.Mu == MuV2Ray && !r.Filter.active() {
			return s.Content(ctx, u, r.Mu, now)
		}
		switch r.Mu {
		case MuV2Ray:
			body, err := s.allVMessURL(ctx, u, r.Filter)
			if err != nil {
				return "", err
			}
			r.Format = "vmess"
			return withSubscriptionInfo(B64(body), r, u, now, s.Loc), nil
		case MuClash:
			if r.Filter.Type != "" && r.Filter.Type != "all" && r.Filter.Type != "vmess" {
				return "", errors.New("旧 Clash 订阅仅支持当前保留的 VMess 节点")
			}
			r.Filter.Type = "vmess"
			return s.clash(ctx, u, now, r.Filter)
		default:
			return "", ErrSSRRetired
		}
	}
	proxies, err := s.proxies(ctx, u, now, r.Filter)
	if err != nil {
		return "", err
	}
	content, err := s.renderProxies(ctx, u, proxies, r, now)
	if err != nil {
		return "", err
	}
	return withSubscriptionInfo(content, r, u, now, s.Loc), nil
}

func (s *Service) renderProxies(ctx context.Context, u store.User, proxies []Proxy, r Request, now time.Time) (string, error) {
	switch r.Format {
	case "sing-box":
		return SingBox(proxies, r.Version, r.Tailscale, r.QUIC, s.ProxyDomains)
	case "clash":
		return ModernClash(proxies, r.List, s.ProxyDomains)
	case "surge":
		return TextClient(proxies, "surge"+r.SurgeVersion, r.List, s.ProxyDomains)
	case "kitsunebi", "surfboard", "quantumult":
		if r.Format == "quantumult" && !r.List && r.Version == "2" {
			return s.quantumultSources(ctx, u, r, proxies)
		}
		return renderLegacyClient(proxies, r, s.ProxyDomains)
	case "loon", "quantumultx":
		return TextClient(proxies, r.Format, r.List, s.ProxyDomains)
	default:
		var lines []string
		for _, p := range proxies {
			match := r.Format == "shadowrocket" || (r.Format == p.Kind && r.Format != "ss") || ((r.Format == "ss" || r.Format == "ss2022") && p.Method == SS2022Method) || (r.Format == "v2ray" && (p.Kind == "vmess" || p.Kind == "vless" || p.Method == SS2022Method))
			if match {
				if uri := p.URI(); uri != "" {
					lines = append(lines, uri)
				}
			}
		}
		if len(lines) == 0 {
			return "", nil
		}
		body := strings.Join(lines, "\n") + "\n"
		if r.List && r.Format == "ss" {
			return body, nil
		}
		return base64.StdEncoding.EncodeToString([]byte(body)), nil
	}
}

// ResponseFile 返回订阅下载的文件名（按客户端软件命名，扩展名对应内容格式）与 Content-Type：
// Clash 为 YAML，Surge / Loon / Surfboard / 旧版 Quantumult 的完整配置为 .conf，sing-box 为 JSON，
// 其他（节点链接列表、Quantumult X 节点列表、各客户端的 list= 节点列表）为 .txt。
func ResponseFile(r Request) (filename, contentType string) {
	const (
		yamlType = "text/yaml; charset=utf-8"
		textType = "text/plain; charset=utf-8"
		jsonType = "application/json; charset=utf-8"
	)
	full := !r.List
	switch r.Format {
	case "legacy":
		if r.Mu == MuClash {
			return "Clash.yaml", yamlType
		}
		return "VMess.txt", textType // mu=2：VMess 链接
	case "clash":
		return "Clash.yaml", yamlType
	case "sing-box":
		return "sing-box.json", jsonType
	case "surge":
		if full {
			return "Surge" + r.SurgeVersion + ".conf", textType
		}
		return "Surge.txt", textType
	case "loon", "surfboard":
		name := map[string]string{"loon": "Loon", "surfboard": "Surfboard"}[r.Format]
		if full {
			return name + ".conf", textType
		}
		return name + ".txt", textType
	case "quantumult":
		if full && (r.Version == "2" || r.Version == "3") {
			return "Quantumult.conf", textType
		}
		return "Quantumult.txt", textType
	}
	name := map[string]string{
		"quantumultx": "QuantumultX", "shadowrocket": "Shadowrocket", "v2ray": "v2rayN", "kitsunebi": "Kitsunebi",
		"ss": "SS2022", "ss2022": "SS2022", "vmess": "VMess", "vless": "VLESS", "anytls": "AnyTLS",
	}[r.Format]
	if name == "" {
		name = "subscription"
	}
	return name + ".txt", textType
}
