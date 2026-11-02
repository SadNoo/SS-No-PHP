package sub

import (
	"strings"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

const testPBK = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
const testKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

func TestSplitRelay(t *testing.T) {
	for _, c := range []struct {
		in, base, host string
		port           int
	}{
		{"h.example.com;8443;" + testKey + ";||1.2.3.4;30001", "h.example.com;8443;" + testKey, "1.2.3.4", 30001},
		{"h.example.com;8443;" + testKey + "||relay.example.net;30001", "h.example.com;8443;" + testKey, "relay.example.net", 30001}, // 兼容不带分号的写法
		// VLESS 选项用单个 | 分隔，short ID 为空：两种写法都只拆最后的后缀。
		{"v.example.com;443;0;tcp;reality;sni=s.co|pbk=" + testPBK + "|sid=;||5.6.7.8;443", "v.example.com;443;0;tcp;reality;sni=s.co|pbk=" + testPBK + "|sid=", "5.6.7.8", 443},
		{"v.example.com;443;0;tcp;reality;sni=s.co|pbk=" + testPBK + "|sid=||5.6.7.8;443", "v.example.com;443;0;tcp;reality;sni=s.co|pbk=" + testPBK + "|sid=", "5.6.7.8", 443},
		// VMess 末尾的空字段原样保留。
		{"m.example.com;443;0;ws;;;||2001:db8::1;20000", "m.example.com;443;0;ws;;", "2001:db8::1", 20000},
	} {
		base, r, err := SplitRelay(c.in, true)
		if err != nil || r == nil || base != c.base || r.Host != c.host || r.Port != c.port {
			t.Errorf("SplitRelay(%q) = %q %+v %v", c.in, base, r, err)
		}
		// 保存时统一写成 ;|| 形式，读回结果不变。
		if b2, r2, _ := SplitRelay(JoinRelay(base, r), true); b2 != base || *r2 != *r {
			t.Errorf("JoinRelay 往返不一致: %q", JoinRelay(base, r))
		}
	}
	for _, bad := range []string{"h;1;k;||1.2.3.4", "h;1;k;||1.2.3.4;0", "h;1;k;||1.2.3.4;70000", "h;1;k;||bad host;443", "h;1;k;||1.2.3.4;443;x", ";||1.2.3.4;443"} {
		if _, _, err := SplitRelay(bad, true); err == nil {
			t.Errorf("严格模式应拒绝 %q", bad)
		}
		if base, r, _ := SplitRelay(bad, false); r != nil || base != bad {
			t.Errorf("宽松模式应原样保留 %q", bad)
		}
	}
	if base, r, _ := SplitRelay("plain.example.com;443;0;ws;tls;path=/a|host=b", true); r != nil || base != "plain.example.com;443;0;ws;tls;path=/a|host=b" {
		t.Error("无中转的节点地址应原样返回")
	}
	if _, err := ParseRelayInput("1.2.3.4", ""); err == nil {
		t.Error("只填一项应报错")
	}
	if r, err := ParseRelayInput("", ""); r != nil || err != nil {
		t.Error("两项留空表示无中转")
	}
}

func TestProtocolRelay(t *testing.T) {
	u := store.User{ID: 7, Passwd: "test-only-password"}
	for _, c := range []struct {
		sort         int32
		base         string
		realHost     string
		realPort     int
		wantSNI      string
		credentialFn func(Proxy) string
	}{
		{SortSS2022, "real-ss.example.com;8443;" + testKey, "real-ss.example.com", 8443, "", func(p Proxy) string { return p.Password }},
		{SortVLESS, "real-vl.example.com;443;0;tcp;reality;sni=www.microsoft.com|pbk=" + testPBK + "|sid=", "real-vl.example.com", 443, "www.microsoft.com", func(p Proxy) string { return p.UUID }},
		{SortAnyTLS, "real-at.example.com;8443;0;tcp;anytls;sni=real-at.example.com", "real-at.example.com", 8443, "real-at.example.com", func(p Proxy) string { return p.Password }},
	} {
		plain, err := ProtocolProxy(u, store.SsNode{ID: 5, Sort: c.sort, Server: c.base})
		if err != nil {
			t.Fatal(err)
		}
		withRelay := store.SsNode{ID: 5, Sort: c.sort, Server: JoinRelay(c.base, &Relay{Host: "relay.example.net", Port: 30001})}
		cfg, err := ParseProtocol(withRelay.Sort, withRelay.Server)
		if err != nil || cfg.Server != c.realHost || cfg.Port != c.realPort || cfg.Relay == nil {
			t.Fatalf("sort %d: 节点后端应使用真实地址: %+v %v", c.sort, cfg, err)
		}
		p, err := ProtocolProxy(u, withRelay)
		if err != nil || p.Server != "relay.example.net" || p.Port != 30001 {
			t.Fatalf("sort %d: 客户端应连接中转: %+v %v", c.sort, p, err)
		}
		if p.SNI != c.wantSNI || c.credentialFn(p) != c.credentialFn(plain) || p.PublicKey != plain.PublicKey || p.ShortID != plain.ShortID {
			t.Errorf("sort %d: SNI、凭据、REALITY 参数不应因中转改变", c.sort)
		}
		if strings.Contains(p.URI(), c.realHost+":") {
			t.Errorf("sort %d: 链接中不应出现真实地址作为连接地址: %s", c.sort, p.URI())
		}
	}
}

func TestVMessRelay(t *testing.T) {
	base := "real-vm.example.com;443;0;ws;tls;path=/v2"
	v := VMessConfig(base + ";||203.0.113.5;30003")
	if v.Str("add") != "203.0.113.5" || v.Int("port") != 30003 || v.Str("sni") != "real-vm.example.com" || v.Str("host") != "real-vm.example.com" || v.Str("tls") != "tls" {
		t.Fatalf("VMess 中转应换连接地址并固定 SNI/Host: %v", v.Values)
	}
	// 附加参数 server= 覆盖连接地址时，中转仍然优先；显式 host / sni 保持不变。
	v = V2Array("real-vm.example.com;443;0;ws;tls;path=/v2|host=cdn.example.com|server=edge.example.com;||relay.example.net;30004")
	if v.Str("add") != "relay.example.net" || v.Int("port") != 30004 || v.Str("host") != "cdn.example.com" {
		t.Fatalf("server= 参数下中转应优先: %v", v.Values)
	}
	// 无中转时输出不变。
	if a, b := EncodeJSON(V2Array(base), 0), EncodeJSON(v2ArrayBase(base), 0); a != b {
		t.Fatalf("无中转时 VMess 解析结果应不变: %s %s", a, b)
	}
}

// longHost 生成由合法标签（每段 50 个字符）组成、总长约 n 的域名。
func longHost(n int) string {
	var labels []string
	for len(strings.Join(labels, ".")) < n {
		labels = append(labels, strings.Repeat("a", 50))
	}
	return strings.Join(labels, ".") + ".example.com"
}

func TestServerLength(t *testing.T) {
	long := "v.example.com;443;0;tcp;reality;sni=s.co|pbk=" + testPBK + "|sid=ab|target=" + longHost(200) + ":443"
	if len(long) <= 128 {
		t.Fatal("测试数据应超过 128")
	}
	if _, err := ParseProtocol(SortVLESS, long); err != nil {
		t.Fatalf("超过 128 的 VLESS 配置应可用: %v", err)
	}
	if _, err := ParseProtocol(SortVLESS, long+";||relay.example.net;30001"); err != nil {
		t.Fatalf("长配置加中转: %v", err)
	}
	tooLong := "v.example.com;443;0;tcp;reality;sni=s.co|pbk=" + testPBK + "|sid=ab|target=" + longHost(400) + ":443"
	if _, err := ParseProtocol(SortVLESS, tooLong); err == nil {
		t.Fatal("原节点配置超过 448 应拒绝")
	}
	if over := long + ";||" + longHost(200) + ";30001"; len(over) <= MaxServerLen {
		t.Fatalf("测试数据应超过 512: %d", len(over))
	}
	if _, err := ParseProtocol(SortVLESS, long+";||"+longHost(200)+";30001"); err == nil || !strings.Contains(err.Error(), "过长") {
		t.Fatalf("总长超过 512 应拒绝: %v", err)
	}
	if _, err := ParseProtocol(SortSS2022, "中文.example.com;8443;"+testKey); err == nil {
		t.Fatal("非 ASCII 应拒绝")
	}
}
