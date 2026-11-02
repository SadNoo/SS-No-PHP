package sub

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

func textProfileNodes() []Proxy {
	return []Proxy{
		{Name: "VMess WS", Kind: "vmess", Server: "node.example", Port: 443, UUID: "00000000-0000-4000-8000-000000000001", Transport: "ws", TLS: true, SNI: "tls.example", Host: "ws.example", Path: "/test"},
		{Name: "SS2022", Kind: "ss", Server: "192.0.2.1", Port: 443, Method: SS2022Method, Password: testServerKey + ":" + testServerKey},
		{Name: "VLESS", Kind: "vless", Server: "192.0.2.2", Port: 443, UUID: "00000000-0000-4000-8000-000000000001", SNI: "tls.example", PublicKey: testPublicKey, ShortID: "ab"},
		{Name: "AnyTLS", Kind: "anytls", Server: "node.example", Port: 443, Password: "test-only-password", SNI: "tls.example"},
		{Name: "LegacySS", Kind: "ss", Server: "192.0.2.3", Port: 8388, Method: "aes-128-gcm", Password: "test-only-password"},
	}
}

// profileSections checks actual rendered text, rather than the Go input structure.
func profileSections(t *testing.T, body string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	section := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			if _, ok := out[section]; ok {
				t.Fatalf("duplicate section %s", section)
			}
			out[section] = []string{}
			continue
		}
		if section == "" {
			t.Fatalf("content outside a section: %s", line)
		}
		out[section] = append(out[section], line)
	}
	return out
}

func profileSettings(t *testing.T, lines []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, line := range lines {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("not a setting: %s", line)
		}
		k = strings.TrimSpace(k)
		if _, exists := out[k]; exists {
			t.Fatalf("duplicate setting: %s", k)
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}

func TestCompleteTextProfiles(t *testing.T) {
	for _, client := range []string{"surge4", "surge5", "loon"} {
		t.Run(client, func(t *testing.T) {
			t.Parallel()
			body, err := TextClient(textProfileNodes(), client, false, testProxyDomains)
			if err != nil {
				t.Fatal(err)
			}
			sections := profileSections(t, body)
			for _, name := range []string{"General", "Proxy", "Proxy Group", "Rule", "Host"} {
				if len(sections[name]) == 0 {
					t.Fatalf("empty or missing section %s", name)
				}
			}
			if strings.Contains(body, "{{") || strings.Contains(body, "<no value>") {
				t.Fatal("unexpanded template")
			}
			settings := profileSettings(t, sections["General"])
			tun := "tun-excluded-routes"
			doh := "encrypted-dns-server"
			if client == "loon" {
				tun = "bypass-tun"
			}
			if client != "surge5" {
				doh = "doh-server"
			}
			for _, key := range []string{tun, "skip-proxy"} {
				for _, cidr := range []string{"127.0.0.0/8", "::1/128"} {
					if !contains(strings.Split(settings[key], ", "), cidr) {
						t.Errorf("%s missing bypass %s", key, cidr)
					}
				}
				if strings.Contains(settings[key], "198.18.") {
					t.Fatal("must not bypass the native FakeIP pool")
				}
			}
			if settings["dns-server"] != "223.5.5.5" || settings[doh] != "https://dns.alidns.com/dns-query" {
				t.Fatal("fixed DNS configuration changed")
			}
			if settings["allow-wifi-access"] != "false" {
				t.Fatal("must not expose a LAN proxy")
			}
			hosts := profileSettings(t, sections["Host"])
			if hosts["dns.alidns.com"] != "223.5.5.5" {
				t.Fatal("missing DoH bootstrap mapping")
			}
			// Groups must only reference declared nodes/policies, and retain their defaults.
			nodes := profileSettings(t, sections["Proxy"])
			groups := profileSettings(t, sections["Proxy Group"])
			if len(groups) != 6 {
				t.Fatal("wrong number of policy groups")
			}
			known := map[string]bool{"DIRECT": true, "REJECT": true}
			for name := range nodes {
				known[name] = true
			}
			for name := range groups {
				if known[name] {
					t.Fatalf("duplicate policy %s", name)
				}
				known[name] = true
			}
			defaults := map[string]string{"🍃 Proxy": "VMess WS", "🫥SiriAI": "🍃 Proxy", "🍎 Only": "DIRECT", "🏖 Netflix": "🍃 Proxy", "🍂 Domestic": "DIRECT", "☁️ Others": "🍃 Proxy"}
			for name, members := range groups {
				parts := strings.Split(members, ",")
				if parts[0] != "select" {
					t.Fatalf("invalid group %s", name)
				}
				if strings.TrimSpace(parts[1]) != defaults[name] {
					t.Fatalf("default changed for %s", name)
				}
				seen := map[string]bool{}
				for _, member := range parts[1:] {
					member = strings.TrimSpace(member)
					if !known[member] || seen[member] || member == name {
						t.Fatalf("dangling/duplicate/recursive policy %s in %s", member, name)
					}
					seen[member] = true
				}
				for node := range nodes {
					if !seen[node] {
						t.Errorf("%s cannot select node %s", name, node)
					}
				}
			}
			for _, rule := range sections["Rule"] {
				parts := strings.Split(rule, ",")
				var policy string
				if parts[0] == "FINAL" {
					policy = strings.TrimSpace(parts[1])
				} else {
					policy = strings.TrimSpace(parts[2])
				}
				if !known[policy] {
					t.Errorf("unknown rule policy %s", policy)
				}
			}
			// Compare node-only output to the complete profile's Proxy section: the template
			// must never re-encode credentials, change transport options, or add filtered nodes.
			list, err := TextClient(textProfileNodes(), client, true, testProxyDomains)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(sections["Proxy"], "\n")+"\n" != list {
				t.Fatal("node-only and full profile credentials differ")
			}
			if client == "surge4" && (len(nodes) != 2 || nodes["SS2022"] == "" || nodes["AnyTLS"] != "") {
				t.Fatal("Surge 4 must contain only VMess and SS2022")
			}
			if client == "surge5" && (len(nodes) != 3 || nodes["SS2022"] == "" || nodes["AnyTLS"] == "") {
				t.Fatal("Surge 5 protocol boundary changed")
			}
			if client == "loon" && (len(nodes) != 5 || nodes["VLESS"] == "") {
				t.Fatal("Loon protocol boundary changed")
			}
			// Override/SiriAI ordering must be checked in the rendered output.
			indices := []int{strings.Index(body, "DOMAIN-SUFFIX,site1.example,"), strings.Index(body, "DOMAIN,guzzoni.apple.com,"), strings.Index(body, "GEOIP,CN,")}
			if indices[0] < 0 || indices[0] >= indices[1] || indices[1] >= indices[2] {
				t.Fatal("override/SiriAI/IP fallback order changed")
			}
			lastRule := sections["Rule"][len(sections["Rule"])-1]
			wantFinal := "FINAL,☁️ Others,dns-failed"
			if client == "loon" {
				wantFinal = "FINAL,☁️ Others"
			}
			if lastRule != wantFinal {
				t.Fatal("wrong final fallback")
			}
			if client == "loon" {
				remote := sections["Remote Rule"]
				if len(remote) != 7 {
					t.Fatal("missing Loon native subscriptions")
				}
				for i, line := range remote {
					name := []string{"Apple", "Apple", "Netflix", "Global", "Global", "ChinaMax", "ChinaMax"}[i]
					if !strings.Contains(line, "/Loon/"+name+"/") || !strings.Contains(line, "enabled=true") {
						t.Fatalf("bad native resource %s", line)
					}
				}
				if strings.Contains(body, "RULE-SET,") {
					t.Fatal("Surge rule syntax leaked into Loon")
				}
			} else {
				var remote []string
				for _, line := range sections["Rule"] {
					if strings.HasPrefix(line, "RULE-SET,") {
						remote = append(remote, line)
					}
				}
				if len(remote) != 4 {
					t.Fatal("missing Surge subscriptions")
				}
				for i, line := range remote {
					name := []string{"Apple", "Netflix", "Global", "ChinaMax"}[i]
					if !strings.Contains(line, "/Surge/"+name+"/") || !strings.HasSuffix(line, ",no-resolve") {
						t.Fatalf("bad native resource %s", line)
					}
				}
				if client == "surge4" && settings["encrypted-dns-server"] != "" {
					t.Fatal("Surge 5 DNS key leaked into Surge 4")
				}
			}
			filename := filepath.Join("testdata", "text-profiles", client+".conf")
			if os.Getenv("UPDATE_TEXT_PROFILE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filename, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if body != string(want) {
				t.Fatalf("profile changed; review %s", filename)
			}
		})
	}
}

func TestTextProfileRejectsAmbiguousNames(t *testing.T) {
	for _, name := range []string{"", "DIRECT", "🍃 Proxy", "a,b", "a\n[Rule]", "a=b", "#comment", "[section]"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			p := textProfileNodes()[0]
			p.Name = name
			if _, err := TextClient([]Proxy{p}, "surge4", false, testProxyDomains); err == nil {
				t.Fatal("unsafe name accepted")
			}
		})
	}
	p := textProfileNodes()[0]
	if _, err := TextClient([]Proxy{p, p}, "loon", false, testProxyDomains); err == nil {
		t.Fatal("duplicate names accepted")
	}
}

func TestNativeRuleReferences(t *testing.T) {
	for _, client := range []string{"surge", "loon"} {
		sets := profileRuleSets(client)
		policies := []string{"🍎 Only", "🏖 Netflix", "🍃 Proxy", "🍂 Domestic"}
		if client == "loon" {
			policies = []string{"🍎 Only", "🍎 Only", "🏖 Netflix", "🍃 Proxy", "🍃 Proxy", "🍂 Domestic", "🍂 Domestic"}
		}
		for i, set := range sets {
			u, err := url.Parse(set.URL)
			if err != nil || u.Scheme != "https" || u.Host != "raw.githubusercontent.com" || u.RawQuery != "" || u.User != nil {
				t.Fatalf("unsafe rule URL %s", set.URL)
			}
			if set.Policy != policies[i] {
				t.Fatal("upstream policy order changed")
			}
		}
	}
}

func TestProfileRuleAuditCoversEveryResource(t *testing.T) {
	data, err := os.ReadFile("testdata/surge-loon-rule-audit.json")
	if err != nil {
		t.Fatal(err)
	}
	var audit struct {
		Rules []struct {
			URL, Format, SHA256 string
			RuleCount           int `json:"rule_count"`
		}
	}
	if err := json.Unmarshal(data, &audit); err != nil {
		t.Fatal(err)
	}
	byURL := map[string]string{}
	for _, r := range audit.Rules {
		if r.RuleCount <= 0 || len(r.SHA256) != 64 {
			t.Fatal("incomplete rule provenance")
		}
		if _, ok := byURL[r.URL]; ok {
			t.Fatal("duplicate audit URL")
		}
		byURL[r.URL] = r.Format
	}
	count := 0
	for _, client := range []string{"surge", "loon"} {
		for _, rule := range profileRuleSets(client) {
			format, ok := byURL[rule.URL]
			if !ok {
				t.Fatalf("unaudited remote rule %s", rule.URL)
			}
			want := "rule-set"
			if strings.HasSuffix(rule.URL, "_Domain.list") {
				want = "domain-set"
			}
			if format != want {
				t.Fatalf("wrong native rule format: %s", rule.URL)
			}
			count++
		}
	}
	if count != len(byURL) {
		t.Fatal("audit does not match referenced resources")
	}
}

// TestQuantumultXValuesUnquoted Quantumult X 不识别引号：节点名称（tag）、密码、UUID、域名、路径等都原样输出，不能带引号
// （否则名称显示为 "名称"、密码多出引号）；值中有逗号、引号、换行或首尾空白、无法正确表示时跳过该节点。
func TestQuantumultXValuesUnquoted(t *testing.T) {
	body, err := TextClient(textProfileNodes(), "quantumultx", false, testProxyDomains)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, `"`) {
		t.Fatalf("Quantumult X 输出不应含引号:\n%s", body)
	}
	for _, want := range []string{
		"vmess=node.example:443, method=aes-128-gcm, password=00000000-0000-4000-8000-000000000001, aead=true, obfs=wss, obfs-host=ws.example, obfs-uri=/test, udp-relay=true, tag=VMess WS",
		"shadowsocks=192.0.2.1:443, method=2022-blake3-aes-256-gcm, password=" + testServerKey + ":" + testServerKey + ", udp-relay=true, tag=SS2022",
		"vless=192.0.2.2:443, method=none, password=00000000-0000-4000-8000-000000000001, obfs=over-tls, obfs-host=tls.example, reality-base64-pubkey=" + testPublicKey + ", reality-hex-shortid=ab, vless-flow=xtls-rprx-vision, tls-verification=true, udp-relay=true, tag=VLESS",
		"anytls=node.example:443, password=test-only-password, over-tls=true, tls-host=tls.example, tls-verification=true, udp-relay=true, tag=AnyTLS",
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("缺少节点行:\n%s\n实际:\n%s", want, body)
		}
	}
	for _, bad := range []func(*Proxy){
		func(p *Proxy) { p.Path = "/a,b" },
		func(p *Proxy) { p.Host = `ws"example` },
		func(p *Proxy) { p.Name = " 前后空白 " },
		func(p *Proxy) { p.Host = "ws.example\nobfs=ws" },
	} {
		p := textProfileNodes()[0]
		bad(&p)
		if line := p.TextLine("quantumultx"); line != "" {
			t.Errorf("无法正确表示的值应跳过该节点: %q", line)
		}
	}
}

// TestTextValueQuotesOnlyWhenNeeded Surge / Loon / Surfboard 的参数值：普通值不加引号，含逗号、引号、换行、首尾空白或为空时加引号。
func TestTextValueQuotesOnlyWhenNeeded(t *testing.T) {
	for in, want := range map[string]string{
		"tls.example": "tls.example", "/test": "/test", "Host:ws.example": "Host:ws.example", testServerKey: testServerKey,
		"a,b": `"a,b"`, `a"b`: `"a\"b"`, " a": `" a"`, "a\nb": `"a\nb"`, "": `""`,
	} {
		if got := textValue(in); got != want {
			t.Errorf("textValue(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestQuantumultXEmptyRealityShortID REALITY short ID 允许为空（ParseProtocol 接受 sid=）：Quantumult X 订阅保留该节点，
// 与修改前一样输出空参数 reality-hex-shortid=；非空 short ID 原样输出；只有空 short ID 节点时订阅仍能生成；混合时都保留。
// 完整配置与节点列表（?list=quantumultx）两种输出都检查。（第三方 P2）
func TestQuantumultXEmptyRealityShortID(t *testing.T) {
	u := store.User{ID: 7, Passwd: "test-only-password"}
	node := func(id uint32, name, sid string) Proxy {
		p, err := ProtocolProxy(u, store.SsNode{ID: id, Sort: SortVLESS, Server: "node.example;443;0;tcp;reality;sni=tls.example|pbk=" + testPublicKey + "|sid=" + sid})
		if err != nil {
			t.Fatal(err)
		}
		if p.ShortID != sid {
			t.Fatalf("short ID 解析为 %q，应为 %q", p.ShortID, sid)
		}
		p.Name = name
		return p
	}
	empty, set := node(15, "空 sid", ""), node(16, "有 sid", "ab")
	if line := empty.TextLine("quantumultx"); !strings.Contains(line, ", reality-hex-shortid=, ") || !strings.HasSuffix(line, "tag=空 sid") {
		t.Fatalf("空 short ID 的节点应保留并输出空参数: %q", line)
	}
	if line := set.TextLine("quantumultx"); !strings.Contains(line, ", reality-hex-shortid=ab, ") {
		t.Fatalf("非空 short ID 应原样输出: %q", line)
	}
	for _, list := range []bool{false, true} {
		body, err := TextClient([]Proxy{empty}, "quantumultx", list, testProxyDomains)
		if err != nil || strings.Count(body, "vless=") != 1 {
			t.Fatalf("只有空 short ID 节点时应生成订阅（list=%v）: %v\n%s", list, err, body)
		}
		body, err = TextClient([]Proxy{empty, set}, "quantumultx", list, testProxyDomains)
		if err != nil || !strings.Contains(body, "tag=空 sid\n") || !strings.Contains(body, "tag=有 sid\n") {
			t.Fatalf("空与非空 short ID 应都保留（list=%v）: %v\n%s", list, err, body)
		}
	}
	// 非空时仍按必填值校验（short ID 已由 ParseProtocol 限制为十六进制，这里只确认没有放松校验）。
	bad := set
	bad.ShortID = "a,b"
	if bad.TextLine("quantumultx") != "" {
		t.Fatal("无法正确表示的 short ID 应跳过该节点")
	}
}
