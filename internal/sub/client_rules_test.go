package sub

import (
	"encoding/json"
	"strings"
	"testing"
)

// testProxyDomains 是测试中「强制走代理」的域名后缀（配置 app.sub_proxy_domains）。
var testProxyDomains = []string{"site1.example", "site2.example"}

// TestProxyDomainsConfigurable：强制走代理的域名来自配置，换成别的域名时各格式都跟随，为空时不输出规则。
func TestProxyDomainsConfigurable(t *testing.T) {
	nodes := textProfileNodes()
	render := map[string]func([]string) (string, error){
		"clash":     func(d []string) (string, error) { return ModernClash(nodes, false, d) },
		"surge4":    func(d []string) (string, error) { return TextClient(nodes, "surge4", false, d) },
		"surge5":    func(d []string) (string, error) { return TextClient(nodes, "surge5", false, d) },
		"loon":      func(d []string) (string, error) { return TextClient(nodes, "loon", false, d) },
		"surfboard": func(d []string) (string, error) { return legacyTextProfile(nodes, "surfboard", false, d) },
		"quantumult": func(d []string) (string, error) {
			return renderLegacyClient(nodes, Request{Format: "quantumult", Version: "3"}, d)
		},
	}
	for name, fn := range render {
		body, err := fn([]string{"panel.example", "sub.example.net"})
		if err != nil {
			t.Fatal(name, err)
		}
		a, b := strings.Index(body, "DOMAIN-SUFFIX,panel.example,🍃 Proxy"), strings.Index(body, "DOMAIN-SUFFIX,sub.example.net,🍃 Proxy")
		siri := strings.Index(body, "DOMAIN,guzzoni.apple.com,")
		if a < 0 || b < a || siri < b {
			t.Errorf("%s: 域名规则缺失或不在 SiriAI 规则之前 (%d %d %d)", name, a, b, siri)
		}
		for _, old := range testProxyDomains {
			if strings.Contains(body, old) {
				t.Errorf("%s: 仍输出了未配置的域名 %s", name, old)
			}
		}
		empty, err := fn(nil)
		if err != nil {
			t.Fatal(name, err)
		}
		if strings.Count(empty, "DOMAIN-SUFFIX,") != strings.Count(body, "DOMAIN-SUFFIX,")-2 || strings.Contains(empty, "panel.example") {
			t.Errorf("%s: 为空时应只少两条域名规则", name)
		}
	}
	for _, v := range []string{"1.14", "1.15"} {
		for _, tail := range []bool{false, true} {
			with, err := SingBox(nodes, v, tail, false, []string{"panel.example"})
			if err != nil {
				t.Fatal(err)
			}
			var c struct {
				DNS   struct{ Rules []map[string]any } `json:"dns"`
				Route struct{ Rules []map[string]any } `json:"route"`
			}
			if err := json.Unmarshal([]byte(with), &c); err != nil {
				t.Fatal(err)
			}
			check := func(kind string, rules []map[string]any, key, want string) {
				for i, r := range rules {
					if r["clash_mode"] == "global" {
						next := rules[i+1]
						if ds, _ := next["domain_suffix"].([]any); len(ds) != 1 || ds[0] != "panel.example" || next[key] != want {
							t.Errorf("sing-box %s tailscale=%v %s: global 之后应为域名规则，实际 %v", v, tail, kind, next)
						}
						return
					}
				}
				t.Errorf("sing-box %s %s: 没有 global 规则", v, kind)
			}
			check("dns", c.DNS.Rules, "server", "proxy-dns")
			check("route", c.Route.Rules, "outbound", "🍃 Proxy")
			without, err := SingBox(nodes, v, tail, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(without, `"domain_suffix"`) != strings.Count(with, `"domain_suffix"`)-2 || strings.Contains(without, "panel.example") {
				t.Errorf("sing-box %s: 为空时不应有 domain_suffix 规则", v)
			}
		}
	}
}
