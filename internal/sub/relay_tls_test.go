package sub

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// effective 是某个客户端格式中实际生效的连接参数：缺省值按客户端的规则补全（未写 Host 时为连接地址等）。
type effective struct {
	TLS              bool
	TLSName, Host    string
	Server           string
	Port             int
	Excluded, NoHost bool
}

// textFields 解析配置行中的 key=value 参数：按引号外的逗号拆分，值可以带双引号（Surge / Loon 需要时才加）或不带。
func textFields(line string) map[string]string {
	out := map[string]string{}
	var parts []string
	start, quoted := 0, false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if quoted {
				i++
			}
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				parts, start = append(parts, line[start:i]), i+1
			}
		}
	}
	parts = append(parts, line[start:])
	for _, part := range parts {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || strings.ContainsAny(k, " \"") {
			continue
		}
		if u, err := strconv.Unquote(v); err == nil {
			v = u
		}
		out[k] = v
	}
	return out
}

// relayEffective 按格式解出实际生效的 TLS 校验名称与传输 Host。
var relayEffective = map[string]func(t *testing.T, p Proxy) effective{
	"sing-box": func(t *testing.T, p Proxy) effective {
		o, err := p.SingBoxOutbound()
		if err != nil {
			t.Fatal(err)
		}
		e := effective{Server: o["server"].(string), Port: o["server_port"].(int), NoHost: true}
		if tls, ok := o["tls"].(object); ok {
			e.TLS, e.TLSName = true, tls["server_name"].(string)
			if tls["insecure"] == true {
				t.Fatal("不应关闭证书校验")
			}
		}
		if tr, ok := o["transport"].(object); ok {
			e.NoHost = false
			switch tr["type"] {
			case "ws":
				e.Host = e.Server // 未写 Host 头时使用连接地址
				if h, ok := tr["headers"].(object); ok {
					e.Host = h["Host"].(string)
				}
			case "http":
				e.Host = tr["host"].([]string)[0]
			}
		}
		return e
	},
	"mihomo": func(_ *testing.T, p Proxy) effective {
		c := p.ClashProxy()
		e := effective{Server: c["server"].(string), Port: c["port"].(int), NoHost: true}
		if c["tls"] == true {
			e.TLS, e.TLSName = true, c["servername"].(string)
		}
		if ws, ok := c["ws-opts"].(object); ok {
			e.NoHost, e.Host = false, ws["headers"].(object)["Host"].(string)
		}
		if h2, ok := c["h2-opts"].(object); ok {
			e.NoHost, e.Host = false, h2["host"].([]string)[0]
		}
		return e
	},
	"surge": func(_ *testing.T, p Proxy) effective { return textEffective(p, "surge", "sni", "ws-headers") },
	"loon":  func(_ *testing.T, p Proxy) effective { return textEffective(p, "loon", "sni", "host") },
	"quantumultx": func(_ *testing.T, p Proxy) effective {
		line := p.TextLine("quantumultx")
		if line == "" {
			return effective{Excluded: true}
		}
		f := textFields(line)
		e := effective{NoHost: !strings.Contains(line, "obfs=ws")}
		e.Server, e.Port = endpointOf(strings.TrimPrefix(strings.SplitN(line, ",", 2)[0], "vmess="))
		if strings.Contains(line, "obfs=wss") || strings.Contains(line, "obfs=over-tls") {
			e.TLS, e.TLSName = true, f["obfs-host"]
		}
		if !e.NoHost {
			e.Host = f["obfs-host"]
		}
		return e
	},
	"quantumult": func(_ *testing.T, p Proxy) effective {
		line := quantumultLine(p)
		if line == "" {
			return effective{Excluded: true}
		}
		f := textFields(line)
		e := effective{Server: p.Server, Port: p.Port, NoHost: f["obfs-header"] == ""}
		if strings.Contains(line, "over-tls=true") {
			e.TLS, e.TLSName = true, f["tls-host"]
		}
		e.Host = strings.TrimPrefix(f["obfs-header"], "Host: ")
		return e
	},
	"kitsunebi": func(_ *testing.T, p Proxy) effective {
		uri := kitsunebiURI(p)
		if uri == "" {
			return effective{Excluded: true}
		}
		e := effective{Server: p.Server, Port: p.Port, TLS: strings.Contains(uri, "tls=1")}
		e.TLSName = p.Server // 该格式没有 SNI 字段，校验名称就是连接地址
		if !e.TLS {
			e.TLSName = ""
		}
		_, q, _ := strings.Cut(uri, "?")
		for _, kv := range strings.Split(q, "&") {
			if k, v, _ := strings.Cut(kv, "="); k == "wsHost" || k == "h2Host" {
				e.Host = v
			}
		}
		e.NoHost = e.Host == ""
		return e
	},
	"v2rayn": func(t *testing.T, p Proxy) effective {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(p.URI(), "vmess://"))
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		str := func(k string) string { s, _ := v[k].(string); return s }
		e := effective{Server: str("add"), NoHost: true}
		if port, ok := v["port"].(float64); ok {
			e.Port = int(port)
		}
		// v2rayN：SNI 为空时用 host，再用连接地址；ws / h2 的 Host 为空时用连接地址。
		if str("tls") == "tls" {
			e.TLS, e.TLSName = true, firstNonempty(str("sni"), str("host"), str("add"))
		}
		if n := str("net"); n == "ws" || n == "h2" || n == "http" {
			e.NoHost, e.Host = false, firstNonempty(str("host"), str("add"))
		}
		return e
	},
}

func textEffective(p Proxy, client, tlsKey, hostKey string) effective {
	line := p.TextLine(client)
	if line == "" {
		return effective{Excluded: true}
	}
	f := textFields(line)
	e := effective{Server: p.Server, Port: p.Port, NoHost: p.Transport != "ws"}
	if strings.Contains(line, "tls=true") || strings.Contains(line, "over-tls=true") {
		e.TLS, e.TLSName = true, f[tlsKey]
	}
	if !e.NoHost {
		e.Host = firstNonempty(strings.TrimPrefix(f[hostKey], "Host:"), p.Server) // 未写 Host 时使用连接地址
	}
	return e
}

func endpointOf(s string) (string, int) {
	i := strings.LastIndex(s, ":")
	port := 0
	for _, c := range s[i+1:] {
		port = port*10 + int(c-'0')
	}
	return strings.Trim(s[:i], "[]"), port
}

// TestVMessRelayKeepsTLSName 验证：加外部中转后，每种格式实际生效的 TLS 校验名称与传输 Host 与中转前完全相同，
// 只有连接地址和端口换成中转入口；纯 TCP 节点不会被开启 TLS。
func TestVMessRelayKeepsTLSName(t *testing.T) {
	const relay = ";||relay.example.net;30001"
	cases := []struct{ name, server, wantTLSName string }{
		{"显式 host 作为校验名称（验收示例）", "real.example.com;443;0;ws;tls;host=cert.example.com|path=/ws", "cert.example.com"},
		{"无 SNI 无 host", "real.example.com;443;0;ws;tls;path=/ws", "real.example.com"},
		{"显式 SNI、无 host", "real.example.com;443;0;ws;tls;sni=sni.example.com|path=/ws", "sni.example.com"},
		{"显式 SNI 与 host", "real.example.com;443;0;ws;tls;sni=sni.example.com|host=cdn.example.com|path=/ws", "sni.example.com"},
		{"原地址为 IP、TCP+TLS", "203.0.113.9;443;0;tls", "203.0.113.9"},
		{"原地址为 IP、ws+TLS", "203.0.113.9;443;0;ws;tls;path=/ws", "203.0.113.9"},
		{"TCP+TLS 带 host", "real.example.com;443;0;tls;;host=cert.example.com", "cert.example.com"},
		{"h2+TLS", "real.example.com;443;0;h2;tls;path=/h2", "real.example.com"},
		{"纯 TCP", "real.example.com;10086;0;tcp", ""},
		{"ws 不带 TLS", "real.example.com;80;0;ws;;path=/ws", ""},
		{"ws 不带 TLS、带 host", "real.example.com;80;0;ws;;host=cdn.example.com|path=/ws", ""},
	}
	for _, c := range cases {
		plain, ok := vmessProxy(store.SsNode{Name: "n", Server: c.server}, "b831381d-6324-4d53-ad4f-8cda48b30811")
		if !ok {
			t.Fatalf("%s: 无法解析", c.name)
		}
		relayed, _ := vmessProxy(store.SsNode{Name: "n", Server: c.server + relay}, plain.UUID)
		for format, fn := range relayEffective {
			before, after := fn(t, plain), fn(t, relayed)
			if before.Excluded {
				if !after.Excluded {
					t.Errorf("%s / %s: 中转前不输出，中转后也不应输出", c.name, format)
				}
				continue
			}
			if after.Excluded {
				// 只有 Kitsunebi（没有 SNI 字段）会排除校验名称与连接地址不同的 TLS 节点。
				if format != "kitsunebi" || !before.TLS {
					t.Errorf("%s / %s: 中转后不应被排除", c.name, format)
				}
				continue
			}
			if after.Server != "relay.example.net" || after.Port != 30001 {
				t.Errorf("%s / %s: 连接地址应为中转入口: %s:%d", c.name, format, after.Server, after.Port)
			}
			if before.TLS != after.TLS || before.TLSName != after.TLSName {
				t.Errorf("%s / %s: TLS 校验名称应不变: 中转前 %v %q，中转后 %v %q", c.name, format, before.TLS, before.TLSName, after.TLS, after.TLSName)
			}
			// Quantumult X 的 ws 只有一个 obfs-host 字段，同时用作 Host 与校验名称（原有行为），不核对期望值。
			if before.TLS && before.TLSName != c.wantTLSName && (format != "quantumultx" || plain.Transport != "ws") {
				t.Errorf("%s / %s: 校验名称 %q, want %q", c.name, format, before.TLSName, c.wantTLSName)
			}
			if !before.TLS && c.wantTLSName != "" {
				t.Errorf("%s / %s: 应开启 TLS", c.name, format)
			}
			if before.NoHost != after.NoHost || before.Host != after.Host {
				t.Errorf("%s / %s: 传输 Host 应不变: 中转前 %q，中转后 %q", c.name, format, before.Host, after.Host)
			}
		}
	}
}

// TestLegacyClashRelayTLS 验证旧 Clash 模板（mu=4，没有 SNI 字段）不输出配置了中转的 TLS 节点，纯 TCP 中转节点照常输出。
func TestLegacyClashRelayTLS(t *testing.T) {
	tls := V2Array("real.example.com;443;0;tls;;host=cert.example.com;||relay.example.net;30001")
	if tls.Origin != "real.example.com" || tls.Str("tls") != "tls" {
		t.Fatalf("应记录原地址: %+v", tls)
	}
	tcp := V2Array("real.example.com;10086;0;tcp;||relay.example.net;30001")
	if tcp.Origin != "real.example.com" || tcp.Str("tls") != "" || tcp.Str("sni") != "" || tcp.Str("host") != "" {
		t.Fatalf("纯 TCP 只替换地址和端口: %+v", tcp.Values)
	}
	if strings.Contains(EncodeJSON(tcp, 0), "Origin") {
		t.Fatal("Origin 不应输出到 JSON")
	}
}
