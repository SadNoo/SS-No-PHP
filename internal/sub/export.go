package sub

import (
	"errors"
	"net"
	"strconv"
	"strings"
)

// ModernClash 输出 Mihomo（Clash Meta）配置：块状 YAML。list 为 true 时只输出 proxies（节点列表），
// 否则为完整配置：通用设置、DNS（fake-ip，与 Surge / Loon 的通用设置一致）、策略组与规则。
// proxyDomains 是强制走「🍃 Proxy」的域名后缀（已校验），为空时不输出。
func ModernClash(proxies []Proxy, list bool, proxyDomains []string) (string, error) {
	nodes := []object{}
	names := []string{}
	for _, p := range proxies {
		if p.Kind == "vmess" && !contains([]string{"", "tcp", "tls", "ws", "grpc", "h2", "http"}, p.Transport) {
			continue
		}
		nodes = append(nodes, p.ClashProxy())
		names = append(names, p.Name)
	}
	if len(nodes) == 0 {
		return "", errors.New("没有兼容的 Mihomo 节点")
	}
	c := object{"proxies": nodes}
	if !list {
		c["mixed-port"] = 7890
		c["allow-lan"] = false
		c["mode"] = "rule"
		c["log-level"] = "warning"
		c["ipv6"] = false
		// 与 Surge / Loon 相同：直连解析只用阿里 DoH（223.5.5.5 只用于解析 DoH 的域名），关闭 IPv6；
		// fake-ip 下本地域名使用真实 IP（fake-ip-filter）。
		c["dns"] = object{
			"enable": true, "ipv6": false, "enhanced-mode": "fake-ip", "fake-ip-range": "198.18.0.1/16",
			"fake-ip-filter": clientLocalNames, "default-nameserver": []string{clientDNSPlain}, "nameserver": []string{clientDNSDoH},
		}
		c["proxy-groups"] = []object{{"name": "🍃 Proxy", "type": "select", "proxies": names}, {"name": "🍎 Only", "type": "select", "proxies": []string{"DIRECT", "🍃 Proxy"}}, {"name": "🫥SiriAI", "type": "select", "proxies": []string{"🍃 Proxy", "DIRECT"}}, {"name": "🏖 Netflix", "type": "select", "proxies": []string{"🍃 Proxy", "DIRECT"}}, {"name": "🍂 Domestic", "type": "select", "proxies": []string{"DIRECT", "🍃 Proxy"}}, {"name": "☁️ Others", "type": "select", "proxies": []string{"🍃 Proxy", "DIRECT"}}}
		c["geodata-mode"] = true
		// 本机、局域网网段与本地域名直连（与 Surge / Loon 模板的 client-local-rules 相同），阿里 DoH 直连。
		var rules []string
		for _, cidr := range clientBypass {
			rules = append(rules, cidrRule(cidr)+","+cidr+",DIRECT,no-resolve")
		}
		rules = append(rules, "DOMAIN,localhost,DIRECT", "DOMAIN-SUFFIX,localhost,DIRECT", "DOMAIN-SUFFIX,local,DIRECT", "DOMAIN-SUFFIX,lan,DIRECT",
			"DOMAIN-SUFFIX,home.arpa,DIRECT", "DOMAIN,dns.alidns.com,DIRECT")
		rules = append(rules, proxyDomainRules(proxyDomains)...)
		rules = append(rules, siriClientRules...)
		c["rules"] = append(rules, "GEOSITE,apple,🍎 Only", "GEOSITE,netflix,🏖 Netflix", "GEOSITE,geolocation-!cn,🍃 Proxy", "GEOSITE,cn,🍂 Domestic", "GEOIP,LAN,DIRECT,no-resolve", "GEOIP,CN,🍂 Domestic", "MATCH,☁️ Others")
	}
	return yamlEncode(c)
}

// textValue 是 Surge / Loon / Surfboard 配置行中的参数值：只有含逗号、引号、换行或首尾空白（以及空值）时才加双引号，
// 普通的值（密码、UUID、域名、路径等）原样输出，与客户端文档的写法一致。
func textValue(s string) string {
	if s == "" || strings.TrimSpace(s) != s || strings.ContainsAny(s, ",\"\r\n") {
		return strconv.Quote(s)
	}
	return s
}

// qxValue 是 Quantumult X 配置行中必填的参数值。Quantumult X 不识别引号（会把引号当作值的一部分，例如节点名称显示为 "名称"），
// 值原样输出；含逗号、引号、换行、首尾空白或为空时无法正确表示，返回 false，调用方跳过该节点。
// 允许为空的参数（REALITY short ID）不经过这里，见 qxLine。
func qxValue(s string) (string, bool) {
	if s == "" || strings.TrimSpace(s) != s || strings.ContainsAny(s, ",\"\r\n") {
		return "", false
	}
	return s, true
}

// TextLine exports only protocols and transports supported by the selected client.
func (p Proxy) TextLine(client string) string {
	// Surge / Loon / Surfboard 的地址与端口是两个逗号分隔的字段，IPv6 地址原样写（不加方括号，加了客户端报配置错误）；
	// Quantumult X 是 host:port 写法，IPv6 需要方括号。
	host := strings.Trim(p.Server, "[]")
	endpoint := net.JoinHostPort(host, strconv.Itoa(p.Port))
	prefix := p.Name + " = "
	base := host + ", " + strconv.Itoa(p.Port)
	if client == "quantumultx" {
		return p.qxLine(endpoint)
	}
	switch p.Kind {
	case "ss":
		if p.Plugin != "" && p.Plugin != "obfs-local" {
			return ""
		}
		line := prefix + "ss, " + base + ", encrypt-method=" + p.Method + ", password=" + textValue(p.Password)
		if client == "loon" {
			line = prefix + "Shadowsocks, " + base + ", " + p.Method + ", " + textValue(p.Password)
		}
		if p.Plugin == "obfs-local" {
			for _, v := range strings.Split(p.PluginOpts, ";") {
				k, val, ok := strings.Cut(v, "=")
				if ok && (k == "obfs" || k == "obfs-host") {
					if client == "loon" && k == "obfs" {
						k = "obfs-name"
					}
					line += ", " + k + "=" + textValue(val)
				}
			}
		}
		if client == "loon" {
			return line + ", udp=true"
		}
		return line + ", udp-relay=true"
	case "anytls":
		if client == "loon" {
			return prefix + "AnyTLS, " + base + ", " + textValue(p.Password) + ", sni=" + p.SNI + ", skip-cert-verify=false, udp=true, block-quic=false"
		}
		return prefix + "anytls, " + base + ", password=" + textValue(p.Password) + ", sni=" + p.SNI + ", skip-cert-verify=false"
	case "vless":
		if client != "loon" {
			return ""
		}
		return prefix + "VLESS, " + base + ", " + textValue(p.UUID) + ", transport=tcp, over-tls=true, sni=" + p.SNI + ", public-key=" + p.PublicKey + ", short-id=" + p.ShortID + ", flow=xtls-rprx-vision, skip-cert-verify=false, udp=true"
	case "vmess":
		if !contains([]string{"", "tcp", "tls", "ws"}, p.Transport) {
			return ""
		}
		line := prefix + "vmess, " + base + ", username=" + p.UUID + ", vmess-aead=" + strconv.FormatBool(p.AlterID == 0)
		if client == "loon" {
			transport := p.Transport
			if transport == "tls" {
				transport = "tcp"
			}
			line = prefix + "vmess, " + base + ", aes-128-gcm, " + textValue(p.UUID) + ", alterId=" + strconv.Itoa(p.AlterID) + ", transport=" + firstNonempty(transport, "tcp") + ", over-tls=" + strconv.FormatBool(p.TLS)
		} else {
			line += ", tls=" + strconv.FormatBool(p.TLS)
		}
		if p.TLS {
			line += ", sni=" + textValue(firstNonempty(p.SNI, p.Host, p.origin())) + ", skip-cert-verify=false"
		}
		if p.Transport == "ws" {
			if client == "surge" {
				line += ", ws=true, ws-path=" + textValue(firstNonempty(p.Path, "/"))
				if h := firstNonempty(p.Host, p.Origin); h != "" { // 中转后 Host 仍为原地址
					line += ", ws-headers=" + textValue("Host:"+h)
				}
			} else {
				line += ", path=" + textValue(firstNonempty(p.Path, "/"))
				if h := firstNonempty(p.Host, p.Origin); h != "" { // 中转后 Host 仍为原地址
					line += ", host=" + textValue(h)
				}
			}
		}
		return line
	}
	return ""
}

func TextClient(proxies []Proxy, client string, list bool, proxyDomains []string) (string, error) {
	surgeVersion := "5"
	if client == "surge" || client == "surge4" || client == "surge5" {
		version := client
		if version == "surge4" {
			surgeVersion = "4"
		}
		client = "surge"
		filtered := make([]Proxy, 0, len(proxies))
		for _, p := range proxies {
			// Surge 4：VMess、SS2022；Surge 5 另加 AnyTLS。两者都不支持 VLESS。
			if p.Kind == "vmess" || p.Method == SS2022Method || (version != "surge4" && p.Kind == "anytls") {
				filtered = append(filtered, p)
			}
		}
		proxies = filtered
	}
	var lines, names []string
	for _, p := range proxies {
		if line := p.TextLine(client); line != "" {
			lines = append(lines, line)
			names = append(names, p.Name)
		}
	}
	if len(lines) == 0 {
		return "", errors.New("没有兼容该客户端的节点")
	}
	if list || client == "quantumultx" {
		return strings.Join(lines, "\n") + "\n", nil
	}
	return renderTextProfile(client, surgeVersion, lines, names, proxyDomains)
}

// qxLine 是 Quantumult X 的节点行（参数值不加引号）；有无法表示的值或不支持的协议、传输方式时返回空字符串。
func (p Proxy) qxLine(endpoint string) string {
	ok := true
	v := func(s string) string {
		out, good := qxValue(s)
		ok = ok && good
		return out
	}
	// optional 是允许为空的参数：空值原样输出为空（与修改前相同，例如 reality-hex-shortid=），非空时与必填参数一样校验。
	optional := func(s string) string {
		if s == "" {
			return ""
		}
		return v(s)
	}
	suffix := ", udp-relay=true, tag=" + v(p.Name)
	line := ""
	switch p.Kind {
	case "ss":
		if p.Plugin != "" && p.Plugin != "obfs-local" {
			return ""
		}
		line = "shadowsocks=" + endpoint + ", method=" + p.Method + ", password=" + v(p.Password)
		if p.Plugin == "obfs-local" {
			for _, opt := range strings.Split(p.PluginOpts, ";") {
				k, val, found := strings.Cut(opt, "=")
				if found && (k == "obfs" || k == "obfs-host") {
					line += ", " + k + "=" + v(val)
				}
			}
		}
	case "anytls":
		line = "anytls=" + endpoint + ", password=" + v(p.Password) + ", over-tls=true, tls-host=" + v(firstNonempty(p.SNI, p.origin())) + ", tls-verification=true"
	case "vless":
		line = "vless=" + endpoint + ", method=none, password=" + v(p.UUID) + ", obfs=over-tls, obfs-host=" + v(p.SNI) + ", reality-base64-pubkey=" + v(p.PublicKey) + ", reality-hex-shortid=" + optional(p.ShortID) + ", vless-flow=xtls-rprx-vision, tls-verification=true"
	case "vmess":
		if !contains([]string{"", "tcp", "tls", "ws"}, p.Transport) {
			return ""
		}
		line = "vmess=" + endpoint + ", method=aes-128-gcm, password=" + v(p.UUID) + ", aead=" + strconv.FormatBool(p.AlterID == 0)
		if p.Transport == "ws" {
			obfs := "ws"
			if p.TLS {
				obfs = "wss"
			}
			line += ", obfs=" + obfs + ", obfs-host=" + v(firstNonempty(p.Host, p.SNI, p.origin())) + ", obfs-uri=" + v(firstNonempty(p.Path, "/"))
		} else if p.TLS {
			line += ", obfs=over-tls, obfs-host=" + v(firstNonempty(p.SNI, p.Host, p.origin()))
		}
	default:
		return ""
	}
	if !ok {
		return ""
	}
	return line + suffix
}
