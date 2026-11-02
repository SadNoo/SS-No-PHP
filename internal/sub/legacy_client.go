package sub

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// Kitsunebi uses the original base64(auto:UUID@host:port) VMess URI,
// not v2rayN's base64 JSON or Quantumult's base64 native declaration.
func kitsunebiURI(p Proxy) string {
	if p.Kind != "vmess" || !contains([]string{"", "tcp", "tls", "ws", "h2", "kcp"}, p.Transport) {
		return ""
	}
	// This historical URI has no verified separate TLS-server-name field.
	// Do not emit a connection that would need disabled certificate checks.
	if p.TLS && firstNonempty(p.SNI, p.Host, p.origin()) != p.Server { // 包括配置了外部中转的 TLS 节点
		return ""
	}
	q := url.Values{"remark": {p.Name}, "network": {firstNonempty(p.Transport, "tcp")}, "aid": {strconv.Itoa(p.AlterID)}}
	if p.Transport == "tls" {
		q.Set("network", "tcp")
	}
	switch p.Transport {
	case "ws":
		q.Set("wspath", firstNonempty(p.Path, "/"))
		q.Set("wsHost", firstNonempty(p.Host, p.SNI, p.origin()))
	case "h2":
		q.Set("h2Path", firstNonempty(p.Path, "/"))
		q.Set("h2Host", firstNonempty(p.Host, p.SNI, p.origin()))
	case "kcp":
		if p.V2 != nil {
			q.Set("kcpheader", firstNonempty(p.V2.Str("type"), "none"))
		}
	}
	if p.TLS {
		q.Set("tls", "1")
		q.Set("allowInsecure", "0")
	}
	endpoint := net.JoinHostPort(p.Server, strconv.Itoa(p.Port))
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte("auto:"+p.UUID+"@"+endpoint)) + "?" + q.Encode()
}

func quantumultLine(p Proxy) string {
	base := p.Name + " = "
	host := p.Server
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	endpoint := host + ", " + strconv.Itoa(p.Port)
	if p.Kind != "vmess" || p.AlterID != 0 || !contains([]string{"", "tcp", "tls", "ws", "http"}, p.Transport) {
		return ""
	}
	line := base + "vmess, " + endpoint + ", chacha20-ietf-poly1305, " + strconv.Quote(p.UUID) + ", udp-relay=true, over-tls=" + strconv.FormatBool(p.TLS) + ", certificate=1"
	if p.TLS {
		line += ", tls-host=" + strconv.Quote(firstNonempty(p.SNI, p.Host, p.origin()))
	}
	if p.Transport == "ws" || p.Transport == "http" {
		line += ", obfs=" + p.Transport + ", obfs-path=" + strconv.Quote(firstNonempty(p.Path, "/")) + ", obfs-header=" + strconv.Quote("Host: "+firstNonempty(p.Host, p.SNI, p.origin()))
	}
	return line
}

func renderLegacyClient(proxies []Proxy, r Request, proxyDomains []string) (string, error) {
	if r.Format == "surfboard" {
		return legacyTextProfile(proxies, "surfboard", r.List, proxyDomains)
	}
	var lines, names []string
	for _, p := range proxies {
		line := ""
		if r.Format == "kitsunebi" {
			line = kitsunebiURI(p)
		} else if r.Version == "3" && !r.List {
			line = quantumultLine(p)
		} else if p.Kind == "vmess" {
			if native := quantumultLine(p); native != "" {
				line = "vmess://" + base64.StdEncoding.EncodeToString([]byte(native))
			}
		}
		if line != "" {
			lines, names = append(lines, line), append(names, p.Name)
		}
	}
	if len(lines) == 0 && r.Format == "quantumult" && r.Version == "3" && !r.List {
		return "", errors.New("没有兼容该旧客户端的节点")
	}
	if r.Format == "kitsunebi" || r.Version != "3" || r.List {
		return base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n") + "\n")), nil
	}
	var b strings.Builder
	b.WriteString("# niuniupanel: legacy Quantumult, not Quantumult X\n[SERVER]\n" + strings.Join(lines, "\n") + "\n\n[POLICY]\n")
	for _, group := range []struct{ name, def string }{{"🍃 Proxy", names[0]}, {"🫥SiriAI", "🍃 Proxy"}, {"🍎 Only", "DIRECT"}, {"🏖 Netflix", "🍃 Proxy"}, {"🍂 Domestic", "DIRECT"}, {"☁️ Others", "🍃 Proxy"}} {
		members := names
		if group.name != "🍃 Proxy" {
			members = append([]string{"DIRECT", "🍃 Proxy"}, names...)
		}
		b.WriteString(base64.StdEncoding.EncodeToString([]byte(group.name+" : static, "+group.def+"\n"+strings.Join(members, "\n")+"\n")) + "\n")
	}
	b.WriteString("\n[Rule]\n" + legacyRules(proxyDomains) + "\n[DNS]\n223.5.5.5\n\n[STATE]\nSTATE,AUTO\n")
	return b.String(), nil
}

// Legacy clients use an embedded flat rule snapshot rather than remote modern
// RULE-SET/domain-set directives which older parsers may not understand.
func legacyRules(proxyDomains []string) string {
	var lines []string
	for _, cidr := range []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10"} {
		lines = append(lines, cidrRule(cidr)+","+cidr+",DIRECT,no-resolve")
	}
	lines = append(lines, "DOMAIN-SUFFIX,local,DIRECT", "DOMAIN-SUFFIX,lan,DIRECT", "DOMAIN-SUFFIX,home.arpa,DIRECT", "DOMAIN,localhost,DIRECT")
	lines = append(lines, proxyDomainRules(proxyDomains)...)
	lines = append(lines, siriClientRules...)
	for _, name := range []string{"apple.com", "icloud.com", "icloud-content.com", "aaplimg.com", "cdn-apple.com", "mzstatic.com"} {
		lines = append(lines, "DOMAIN-SUFFIX,"+name+",🍎 Only")
	}
	for _, name := range []string{"netflix.com", "netflix.net", "nflxext.com", "nflximg.com", "nflximg.net", "nflxso.net", "nflxvideo.net"} {
		lines = append(lines, "DOMAIN-SUFFIX,"+name+",🏖 Netflix")
	}
	rename := strings.NewReplacer(",Proxy", ",🍃 Proxy", ",Domestic", ",🍂 Domestic", ",China_media", ",🍂 Domestic", ",Global_media", ",🍃 Proxy", ",Others", ",☁️ Others")
	for _, raw := range strings.Split(clashRules, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "- "))
		if line == "" || line == "Rule:" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.ReplaceAll(line, ", ", ",")
		if strings.HasPrefix(line, "MATCH,") {
			line = strings.Replace(line, "MATCH,", "FINAL,", 1)
		}
		lines = append(lines, rename.Replace(line))
	}
	return strings.Join(lines, "\n") + "\n"
}

func legacyTextProfile(proxies []Proxy, client string, list bool, proxyDomains []string) (string, error) {
	var lines, names []string
	for _, p := range proxies {
		line := ""
		if client == "surfboard" && (p.Kind == "vmess" || p.Kind == "ss" && p.Method == SS2022Method) {
			line = p.TextLine("surge")
		}
		if line != "" {
			lines, names = append(lines, line), append(names, p.Name)
		}
	}
	if len(lines) == 0 {
		return "", errors.New("没有兼容该旧客户端的节点")
	}
	if list {
		return strings.Join(lines, "\n") + "\n", nil
	}
	if textProfilesErr != nil {
		return "", textProfilesErr
	}
	data := struct {
		textProfileData
		Client, Rules string
	}{textProfileData: textProfileData{Proxies: lines, Names: names}, Client: client, Rules: legacyRules(proxyDomains)}
	var b bytes.Buffer
	if err := textProfiles.ExecuteTemplate(&b, "surfboard.conf.tmpl", data); err != nil {
		return "", err
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

func (s *Service) quantumultSources(ctx context.Context, u store.User, r Request, proxies []Proxy) (string, error) {
	tokenFn := s.SubToken
	if s.TokenFunc != nil {
		tokenFn = s.TokenFunc
	}
	token, err := tokenFn(ctx, u.ID, 0)
	if err != nil {
		return "", err
	}
	makeURL := func(key, value string) string {
		q := make(url.Values)
		for _, k := range []string{"type", "class", "noclass", "regex", "emoji", "extend"} {
			if v, ok := r.Query[k]; ok {
				q[k] = v
			}
		}
		q.Set(key, value)
		return s.SubURL + token + "?" + q.Encode()
	}
	name := strings.NewReplacer(",", " ", "\r", " ", "\n", " ").Replace(s.AppName)
	full, err := renderLegacyClient(proxies, Request{Format: "quantumult", Version: "3"}, s.ProxyDomains)
	if err != nil {
		return "", err
	}
	return full + "\n[SOURCE]\n" + name + "_VMess, server, " + makeURL("list", "quantumult") + ", false, true, false\n", nil
}
