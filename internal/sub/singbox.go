package sub

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

// profiles are data-only snapshots of the approved 2026-09-26 client policies.
// Node credentials and selectors are assembled in Go for each authenticated user.
//
//go:embed profiles/*.json
var singBoxProfiles embed.FS

type object = map[string]any

func (p Proxy) SingBoxOutbound() (object, error) {
	o := object{"type": p.Kind, "tag": p.Name, "server": p.Server, "server_port": p.Port}
	switch p.Kind {
	case "ss":
		o["type"] = "shadowsocks"
		o["method"] = p.Method
		o["password"] = p.Password
		if p.Plugin != "" {
			o["plugin"] = p.Plugin
			o["plugin_opts"] = p.PluginOpts
		}
	case "anytls":
		o["password"] = p.Password
		o["tls"] = object{"enabled": true, "server_name": p.SNI, "insecure": false}
	case "vless":
		o["uuid"] = p.UUID
		o["flow"] = "xtls-rprx-vision"
		o["packet_encoding"] = "xudp"
		o["tls"] = object{"enabled": true, "server_name": p.SNI, "utls": object{"enabled": true, "fingerprint": "chrome"}, "reality": object{"enabled": true, "public_key": p.PublicKey, "short_id": p.ShortID}}
	case "vmess":
		o["uuid"] = p.UUID
		o["security"] = "auto"
		if p.AlterID != 0 {
			o["alter_id"] = p.AlterID
		}
		if p.TLS {
			o["tls"] = object{"enabled": true, "server_name": firstNonempty(p.SNI, p.Host, p.origin())}
		}
		switch p.Transport {
		case "", "tcp", "tls":
		case "ws":
			t := object{"type": "ws", "path": firstNonempty(p.Path, "/")}
			if h := firstNonempty(p.Host, p.Origin); h != "" { // 中转后 Host 仍为原地址
				t["headers"] = object{"Host": h}
			}
			o["transport"] = t
		case "grpc":
			o["transport"] = object{"type": "grpc", "service_name": p.Path}
		case "h2", "http":
			o["transport"] = object{"type": "http", "host": []string{firstNonempty(p.Host, p.origin())}, "path": firstNonempty(p.Path, "/")}
		default:
			return nil, fmt.Errorf("sing-box 不支持 VMess transport %q", p.Transport)
		}
	default:
		return nil, fmt.Errorf("sing-box 不支持协议 %q", p.Kind)
	}
	return o, nil
}

func firstNonempty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// SingBox builds the two approved versions without consulting the host's DNS or network.
func SingBox(proxies []Proxy, version string, tailscale, quic bool, proxyDomains []string) (string, error) {
	if version != "1.14" && version != "1.15" {
		return "", errors.New("仅支持 sing-box 配置 1.14 / 1.15")
	}
	b, err := singBoxProfiles.ReadFile("profiles/" + version + ".json")
	if err != nil {
		return "", err
	}
	var c object
	if err = json.Unmarshal(b, &c); err != nil {
		return "", err
	}
	domestic := "local-dns"
	if version == "1.15" {
		domestic = "cn-dns"
	}
	var nodes []any
	var names []string
	for _, p := range proxies {
		o, err := p.SingBoxOutbound()
		if err != nil {
			continue
		}
		if net.ParseIP(p.Server) == nil {
			o["domain_resolver"] = object{"server": domestic, "strategy": "prefer_ipv4", "timeout": "5s"}
		}
		nodes = append(nodes, o)
		names = append(names, p.Name)
	}
	if len(nodes) == 0 {
		return "", errors.New("没有可用的 sing-box 节点")
	}
	selector := func(tag string, choices []string, def string) object {
		return object{"type": "selector", "tag": tag, "outbounds": choices, "default": def, "interrupt_exist_connections": false}
	}
	proxyChoices := append([]string{"🍃 Proxy"}, names...)
	proxyChoices = append(proxyChoices, "direct")
	directChoices := append([]string{"direct", "🍃 Proxy"}, names...)
	var out []any
	out = append(out, selector("🍃 Proxy", names, names[0]), selector("🫥SiriAI", proxyChoices, "🍃 Proxy"), selector("🍎 Only", directChoices, "direct"), selector("🏖 Netflix", proxyChoices, "🍃 Proxy"), selector("🍂 Domestic", directChoices, "direct"), selector("☁️ Others", proxyChoices, "🍃 Proxy"))
	out = append(out, nodes...)
	out = append(out, object{"type": "direct", "tag": "direct"})
	c["outbounds"] = out
	route := c["route"].(map[string]any)
	rules := route["rules"].([]any)
	if quic {
		filtered := make([]any, 0, len(rules))
		for _, r := range rules {
			if r.(map[string]any)["protocol"] != "quic" {
				filtered = append(filtered, r)
			}
		}
		rules = filtered
	}
	// 强制走代理的域名后缀：DNS 用代理 DNS、路由走「🍃 Proxy」，紧接在 global 模式规则之后。
	// 为空时不加规则（sing-box 中空的 domain_suffix 不能作为“不匹配”使用）。
	if len(proxyDomains) > 0 {
		suffixes := make([]any, 0, len(proxyDomains))
		for _, d := range proxyDomains {
			suffixes = append(suffixes, d)
		}
		dns := c["dns"].(map[string]any)
		dns["rules"] = insertAfterGlobal(dns["rules"].([]any), object{"domain_suffix": suffixes, "action": "route", "server": "proxy-dns"})
		rules = insertAfterGlobal(rules, object{"domain_suffix": suffixes, "action": "route", "outbound": "🍃 Proxy"})
	}
	if tailscale {
		ep := object{"type": "tailscale", "tag": "tailscale", "ephemeral": false, "accept_routes": true, "system_interface": false, "ssh_server": false}
		if version == "1.15" {
			ep["on_demand"] = true
		}
		c["endpoints"] = []any{ep}
		dns := c["dns"].(map[string]any)
		dns["servers"] = append(dns["servers"].([]any), object{"type": "tailscale", "tag": "tailscale-dns", "endpoint": "tailscale", "accept_default_resolvers": false, "accept_search_domain": true})
		dns["rules"] = append([]any{object{"preferred_by": "tailscale-dns", "action": "route", "server": "tailscale-dns"}}, dns["rules"].([]any)...)
		rules = append(rules[:2], append([]any{object{"preferred_by": "tailscale", "action": "route", "outbound": "tailscale"}}, rules[2:]...)...)
		removeExcludes := func(o map[string]any, key string) {
			var keep []any
			for _, address := range o[key].([]any) {
				if address != "100.64.0.0/10" && address != "fc00::/7" {
					keep = append(keep, address)
				}
			}
			o[key] = keep
		}
		removeExcludes(c["inbounds"].([]any)[0].(map[string]any), "route_exclude_address")
		for _, r := range rules {
			rule := r.(map[string]any)
			if _, ok := rule["ip_cidr"]; ok {
				removeExcludes(rule, "ip_cidr")
				break
			}
		}
	}
	route["rules"] = rules
	b, err = json.MarshalIndent(c, "", "    ")
	return string(b), err
}

func (p Proxy) ClashProxy() object {
	c := object{"name": p.Name, "type": p.Kind, "server": p.Server, "port": p.Port, "udp": true}
	switch p.Kind {
	case "ss":
		c["cipher"] = p.Method
		c["password"] = p.Password
		if p.Plugin == "obfs-local" {
			mode := "http"
			if strings.Contains(p.PluginOpts, "obfs=tls") {
				mode = "tls"
			}
			host := ""
			for _, part := range strings.Split(p.PluginOpts, ";") {
				if strings.HasPrefix(part, "obfs-host=") {
					host = strings.TrimPrefix(part, "obfs-host=")
				}
			}
			c["plugin"] = "obfs"
			c["plugin-opts"] = object{"mode": mode, "host": host}
		}
		if p.Plugin == "v2ray-plugin" {
			c["plugin"] = "v2ray-plugin"
			opts := object{"mode": "websocket"}
			for _, part := range strings.Split(p.PluginOpts, ";") {
				k, v, _ := strings.Cut(part, "=")
				switch k {
				case "host", "path":
					opts[k] = v
				case "tls":
					opts[k] = true
				case "security":
					if v == "tls" {
						opts["tls"] = true
					}
				}
			}
			c["plugin-opts"] = opts
		}
	case "vmess":
		c["uuid"] = p.UUID
		c["alterId"] = p.AlterID
		c["cipher"] = "auto"
		if p.TLS {
			c["tls"] = true
			c["servername"] = firstNonempty(p.SNI, p.Host, p.origin())
		}
		switch p.Transport {
		case "ws":
			c["network"] = "ws"
			c["ws-opts"] = object{"path": firstNonempty(p.Path, "/"), "headers": object{"Host": firstNonempty(p.Host, p.origin())}}
		case "grpc":
			c["network"] = "grpc"
			c["grpc-opts"] = object{"grpc-service-name": p.Path}
		case "h2", "http":
			c["network"] = "h2"
			c["h2-opts"] = object{"host": []string{firstNonempty(p.Host, p.origin())}, "path": firstNonempty(p.Path, "/")}
		}
	case "anytls":
		c["password"] = p.Password
		c["sni"] = p.SNI
		c["client-fingerprint"] = "chrome"
		c["skip-cert-verify"] = false
	case "vless":
		c["uuid"] = p.UUID
		c["network"] = "tcp"
		c["tls"] = true
		c["servername"] = p.SNI
		c["flow"] = "xtls-rprx-vision"
		c["packet-encoding"] = "xudp"
		c["client-fingerprint"] = "chrome"
		c["encryption"] = ""
		c["reality-opts"] = object{"public-key": p.PublicKey, "short-id": p.ShortID}
	}
	return c
}

// insertAfterGlobal 把规则插在 clash_mode 为 global 的规则之后（profiles 中各有一条）；找不到时放在最前。
func insertAfterGlobal(rules []any, rule object) []any {
	at := 0
	for i, r := range rules {
		if m, ok := r.(map[string]any); ok && m["clash_mode"] == "global" {
			at = i + 1
			break
		}
	}
	out := make([]any, 0, len(rules)+1)
	out = append(out, rules[:at]...)
	out = append(out, rule)
	return append(out, rules[at:]...)
}
