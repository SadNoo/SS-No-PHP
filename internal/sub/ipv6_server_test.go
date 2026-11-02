package sub

import (
	"strings"
	"testing"
)

// TestIPv6ServerAddress 节点地址为 IPv6 时：Surge / Loon / Surfboard 的地址字段原样写（带方括号会报配置错误），
// Quantumult X、Clash 按各自写法；后台填写时带了方括号也一样处理。
func TestIPv6ServerAddress(t *testing.T) {
	for _, server := range []string{"2001:db8::1", "[2001:db8::1]"} {
		var proxies []Proxy
		for _, p := range textProfileNodes() {
			p.Server = server
			proxies = append(proxies, p)
		}
		for _, client := range []string{"surge4", "surge5", "loon", "surfboard"} {
			body, err := TextClient(proxies, client, true, testProxyDomains)
			if err != nil {
				t.Fatal(client, err)
			}
			for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
				if strings.Contains(line, "[") || !strings.Contains(line, ", 2001:db8::1, ") {
					t.Fatalf("%s 地址 %q 输出错误: %s", client, server, line)
				}
			}
		}
		body, err := TextClient(proxies, "quantumultx", true, testProxyDomains)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
			if !strings.Contains(line, "=[2001:db8::1]:") {
				t.Fatalf("quantumultx 地址 %q 输出错误: %s", server, line)
			}
		}
	}
}
