package sub

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// viaJSON 把值统一转换成 JSON 解码后的形式（map[string]any、[]any、float64 等），用于比较 YAML 读回的结果与原始数据。
func viaJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestYAMLEncodeRoundTrip 用标准 YAML 解析器读回 yamlEncode 的输出，与原始数据完全相同：包括容易被误解析的字符串
// （数字、布尔值、null、冒号、井号、引号、反斜杠、换行、前导横线、emoji、中文）、空列表与空 map、嵌套列表。
func TestYAMLEncodeRoundTrip(t *testing.T) {
	tricky := []string{"123", "yes", "no", "true", "null", "~", "a: b", "#x", "- a", "\"q\"", `back\slash`, "多\n行", "🍃 Proxy", "台湾 直连 3", "", " lead", "&a", "*b", "!tag", "{x}", "[y]", "%p", "@q", "`r`"}
	data := object{
		"name": "n", "port": 443, "big": int64(1 << 40), "ratio": 1.5, "on": true, "off": false, "nothing": nil,
		"strings": tricky, "empty-list": []string{}, "empty-map": object{}, "Host": "h", "weird key: x": "v",
		"nested": []any{object{"a": 1, "b": []string{"x"}}, []any{"p", object{}}, object{}},
		"objs":   []object{{"name": "g", "proxies": []string{"DIRECT"}}},
	}
	out, err := yamlEncode(data)
	if err != nil {
		t.Fatal(err)
	}
	var back any
	if err := yaml.Unmarshal([]byte(out), &back); err != nil {
		t.Fatalf("YAML 无法解析: %v\n%s", err, out)
	}
	if got, want := viaJSON(t, back), viaJSON(t, data); !reflect.DeepEqual(got, want) {
		t.Fatalf("读回的数据不同:\n%v\n%v\n%s", got, want, out)
	}
	if _, err := yamlEncode(object{"bad": struct{}{}}); err == nil {
		t.Fatal("不支持的类型应返回错误")
	}
}

// TestClashYAMLProfile Clash 订阅是块状 YAML（不是 JSON）：读回后节点与 ClashProxy 完全相同；完整配置包含 fake-ip DNS、
// 关闭 IPv6、本机与局域网网段及本地域名直连、阿里 DoH 直连，策略组与原来相同；节点列表（list）只有 proxies。
func TestClashYAMLProfile(t *testing.T) {
	nodes := textProfileNodes()
	full, err := ModernClash(nodes, false, testProxyDomains)
	if err != nil {
		t.Fatal(err)
	}
	if json.Valid([]byte(full)) || strings.HasPrefix(strings.TrimSpace(full), "{") || !strings.HasPrefix(full, "mixed-port: 7890\n") {
		t.Fatalf("应为块状 YAML:\n%s", full)
	}
	var c map[string]any
	if err := yaml.Unmarshal([]byte(full), &c); err != nil {
		t.Fatal(err)
	}
	var want []any
	for _, p := range nodes {
		want = append(want, p.ClashProxy())
	}
	if !reflect.DeepEqual(viaJSON(t, c["proxies"]), viaJSON(t, want)) {
		t.Fatalf("节点与 ClashProxy 不同:\n%v", c["proxies"])
	}
	dns, _ := c["dns"].(map[string]any)
	wantDNS := object{"enable": true, "ipv6": false, "enhanced-mode": "fake-ip", "fake-ip-range": "198.18.0.1/16",
		"fake-ip-filter": clientLocalNames, "default-nameserver": []string{"223.5.5.5"}, "nameserver": []string{"https://dns.alidns.com/dns-query"}}
	if c["ipv6"] != false || !reflect.DeepEqual(viaJSON(t, dns), viaJSON(t, wantDNS)) {
		t.Fatalf("DNS 设置不正确: ipv6=%v dns=%v", c["ipv6"], dns)
	}
	rules := viaJSON(t, c["rules"]).([]any)
	has := map[string]bool{}
	for _, r := range rules {
		has[r.(string)] = true
	}
	for _, cidr := range clientBypass {
		if !has[cidrRule(cidr)+","+cidr+",DIRECT,no-resolve"] {
			t.Errorf("缺少直连网段 %s", cidr)
		}
	}
	for _, r := range []string{"DOMAIN,localhost,DIRECT", "DOMAIN-SUFFIX,local,DIRECT", "DOMAIN-SUFFIX,lan,DIRECT", "DOMAIN-SUFFIX,home.arpa,DIRECT", "DOMAIN,dns.alidns.com,DIRECT", "MATCH,☁️ Others"} {
		if !has[r] {
			t.Errorf("缺少规则 %s", r)
		}
	}
	if rules[len(rules)-1] != "MATCH,☁️ Others" {
		t.Error("MATCH 应是最后一条规则")
	}
	groups := viaJSON(t, c["proxy-groups"]).([]any)
	if len(groups) != 6 || groups[0].(map[string]any)["name"] != "🍃 Proxy" || len(groups[0].(map[string]any)["proxies"].([]any)) != len(nodes) {
		t.Fatalf("策略组不正确: %v", groups)
	}

	list, err := ModernClash(nodes, true, testProxyDomains)
	if err != nil {
		t.Fatal(err)
	}
	var l map[string]any
	if err := yaml.Unmarshal([]byte(list), &l); err != nil || len(l) != 1 || !reflect.DeepEqual(viaJSON(t, l["proxies"]), viaJSON(t, want)) {
		t.Fatalf("节点列表应只有 proxies: %v\n%s", err, list)
	}
}
