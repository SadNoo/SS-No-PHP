package app

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/sub"
)

func TestNodeIPHelpers(t *testing.T) {
	// 主 IP 是第一个不带 # 的项；# 专用项不参与。
	if got := nodeIPPrimary(" 1.1.1.1#3 , 2.2.2.2"); got != "2.2.2.2" {
		t.Errorf("primary = %q", got)
	}
	if got := nodeIPPrimary("1.1.1.1#3"); got != "" {
		t.Errorf("只有专用项时没有主 IP: %q", got)
	}
	if got := nodeIPWithPrimary("1.1.1.1,5.6.7.8,9.9.9.9#2", "1.1.1.9"); got != "1.1.1.9,5.6.7.8,9.9.9.9#2" {
		t.Errorf("withPrimary = %q", got)
	}
	if got := nodeIPWithPrimary("", "1.1.1.9"); got != "1.1.1.9" {
		t.Errorf("withPrimary empty = %q", got)
	}
	// 旧面板格式：专用项在前时，自动更新只替换不带 # 的主 IP，保留 #8 绑定。
	if got := nodeIPWithPrimary("198.51.100.10#8,203.0.113.20", "203.0.113.99"); got != "198.51.100.10#8,203.0.113.99" {
		t.Errorf("withPrimary 应保留专用项: %q", got)
	}
	if got := nodeIPWithPrimary("198.51.100.10#8", "203.0.113.99"); got != "203.0.113.99,198.51.100.10#8" {
		t.Errorf("没有主 IP 时加在最前: %q", got)
	}
	for _, bad := range []string{"hk.example.com", "1.1.1.1#", "1.1.1.1#a", "1.1.1"} {
		if validateNodeIP(nodeIPEntries(bad)) == nil {
			t.Errorf("应拒绝 %q", bad)
		}
	}
	if validateNodeIP(nodeIPEntries("1.1.1.1, 2001:db8::1,3.3.3.3#2|5")) != nil {
		t.Error("合法列表被拒绝")
	}
	// 中转目标：默认为主 IP（手动添加的出口 IP 不能作为中转目标），#源节点ID 优先。
	if got := relayDistIP(store.SsNode{ID: 7}, "1.1.1.1,5.6.7.8"); got != "1.1.1.1" {
		t.Errorf("relay 默认应为主 IP: %q", got)
	}
	if got := relayDistIP(store.SsNode{ID: 7}, "1.1.1.1,5.6.7.8,9.9.9.9#7"); got != "9.9.9.9" {
		t.Errorf("relay 专用 IP: %q", got)
	}
	// 旧面板格式：源节点 8 使用专用目标，其他源节点使用不带 # 的默认目标。
	legacy := "198.51.100.10#8,203.0.113.20"
	for id, want := range map[uint32]string{8: "198.51.100.10", 9: "203.0.113.20", 0: "203.0.113.20"} {
		if got := relayDistIP(store.SsNode{ID: id}, legacy); got != want {
			t.Errorf("relay 源节点 %d = %q, want %q", id, got, want)
		}
	}
	if got := relayDistIP(store.SsNode{ID: 8}, "198.51.100.10#3|8"); got != "198.51.100.10" {
		t.Errorf("多个源节点 ID: %q", got)
	}
	// 原版规则（只用于 checkNodeIP 对比）：默认取最后一个不带 # 的项，后面的项覆盖前面匹配的专用项。
	for _, c := range []struct {
		id         uint32
		ip, legacy string
	}{{8, legacy, "203.0.113.20"}, {9, legacy, "203.0.113.20"}, {8, "203.0.113.20,198.51.100.10#8", "198.51.100.10"}, {7, "1.1.1.1,5.6.7.8", "5.6.7.8"}} {
		if got := legacyRelayDistIP(c.id, c.ip); got != c.legacy {
			t.Errorf("legacyRelayDistIP(%d, %q) = %q, want %q", c.id, c.ip, got, c.legacy)
		}
	}
	if !nodeIPHas(legacy, "198.51.100.10") || !nodeIPHas(legacy, "203.0.113.20") || nodeIPHas(legacy, "198.51.100.1") {
		t.Error("nodeIPHas")
	}
}

// TestNodeIPSync 验证：每分钟同步覆盖新协议节点、只替换主 IP 并保留手动项、解析失败保留原 IP；
// 后台保存时解析失败的处理与节点 IP 格式校验。
func TestNodeIPSync(t *testing.T) {
	e, _ := adminEnv(t)
	ctx := context.Background()
	var mu sync.Mutex
	dns := map[string]string{}
	orig := lookupIPv4
	lookupIPv4 = func(_ context.Context, host string) ([]net.IP, error) {
		mu.Lock()
		defer mu.Unlock()
		if ip, ok := dns[host]; ok {
			return []net.IP{net.ParseIP(ip)}, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { lookupIPv4 = orig })
	setDNS := func(host, ip string) { mu.Lock(); dns[host] = ip; mu.Unlock() }

	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(41,'SS2022',1,?,'2022-blake3-aes-256-gcm','','',14,1,0,'198.51.100.1,203.0.113.7',0),
		(42,'VLESS',1,?,'','','',15,1,0,'198.51.100.2',0),
		(43,'AnyTLS',1,'any.example.com;443;0;tcp;anytls;sni=any.example.com','','','',16,1,0,'198.51.100.3',0),
		(44,'解析失败',1,?,'2022-blake3-aes-256-gcm','','',14,1,0,'198.51.100.4,203.0.113.8',0)`,
		"ss.example.com;8443;"+fixtureKey, "vless.example.com;443;0;tcp;reality;sni=s.co|pbk="+fixturePublic+"|sid=ab",
		"gone.example.com;8443;"+fixtureKey)
	setDNS("ss.example.com", "198.51.100.11")
	setDNS("vless.example.com", "198.51.100.12")
	setDNS("any.example.com", "198.51.100.13")
	if err := e.app.RunJob(ctx, "syncnode"); err != nil {
		t.Fatal(err)
	}
	nodeIP := func(id uint32) string { n, _ := e.app.Q.GetNode(ctx, id); return n.NodeIp }
	for id, want := range map[uint32]string{41: "198.51.100.11,203.0.113.7", 42: "198.51.100.12", 43: "198.51.100.13", 44: "198.51.100.4,203.0.113.8"} {
		if got := nodeIP(id); got != want {
			t.Errorf("节点 %d: 节点 IP = %q，期望 %q", id, got, want)
		}
	}

	// 来源 IP 不符时当场重新解析：同样只替换主 IP。
	setDNS("ss.example.com", "198.51.100.21")
	e.app.nodeAuth.dns = nil
	e.app.lookupIP = func(_ context.Context, host string) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		return []string{dns[host]}, nil
	}
	if ok, err := e.app.authorizeNode(ctx, "198.51.100.21", "41", true); err != nil || !ok {
		t.Fatalf("当场重新解析应通过: %v %v", ok, err)
	}
	if got := nodeIP(41); got != "198.51.100.21,203.0.113.7" {
		t.Fatalf("当场解析后应保留手动项: %q", got)
	}

	form := func(server, nodeIP string) url.Values {
		return url.Values{"name": {"新节点"}, "server": {server}, "sort": {"14"}, "type": {"1"}, "rate": {"1"}, "class": {"0"}, "group": {"0"},
			"node_ip": {nodeIP}, "node_speedlimit": {"0"}, "node_bandwidth_limit": {"0"}, "bandwidthlimit_resetday": {"1"}, "custom_method": {"0"},
			"custom_rss": {"1"}, "mu_only": {"0"}, "status": {"可用"}, "info": {""}, "method": {""}}
	}
	// 新建：解析失败且未填节点 IP，明确提示且不保存。
	if r := e.post("/admin/node", form("new.example.com;8443;"+fixtureKey, "")); r["ret"] != float64(0) || !strings.Contains(r["msg"].(string), "获取节点IP失败") {
		t.Fatalf("新建解析失败应提示: %v", r)
	}
	var cnt int
	_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM ss_node WHERE server LIKE 'new.example.com%'").Scan(&cnt)
	if cnt != 0 {
		t.Fatal("解析失败不应保存节点")
	}
	// 新建：解析失败但手动填写了节点 IP，照常保存。
	if r := e.post("/admin/node", form("new.example.com;8443;"+fixtureKey, "198.51.100.30")); r["ret"] != float64(1) {
		t.Fatalf("填写节点 IP 应可保存: %v", r)
	}
	// 新建：节点 IP 格式无效。
	if r := e.post("/admin/node", form("ok.example.com;8443;"+fixtureKey, "ok.example.com")); r["ret"] != float64(0) || !strings.Contains(r["msg"].(string), "节点 IP 格式无效") {
		t.Fatalf("应拒绝无效节点 IP: %v", r)
	}
	// 编辑：解析失败、表单节点 IP 清空，保留原主 IP；手动项按表单为准。
	if body := e.raw("PUT", "/admin/node/44", "application/x-www-form-urlencoded", form("gone.example.com;8443;"+fixtureKey, "").Encode()); !strings.Contains(body, "修改成功") {
		t.Fatalf("编辑失败: %s", body)
	}
	if got := nodeIP(44); got != "198.51.100.4" {
		t.Fatalf("解析失败应保留原主 IP: %q", got)
	}
	// 编辑：解析成功时主 IP 取解析结果，表单第二项起作为手动项保留。
	if body := e.raw("PUT", "/admin/node/42", "application/x-www-form-urlencoded", func() url.Values {
		f := form("vless.example.com;443;0;tcp;reality;sni=s.co|pbk="+fixturePublic+"|sid=ab", "10.0.0.1,203.0.113.9")
		f.Set("sort", "15")
		return f
	}().Encode()); !strings.Contains(body, "修改成功") {
		t.Fatalf("编辑失败: %s", body)
	}
	if got := nodeIP(42); got != "198.51.100.12,203.0.113.9" {
		t.Fatalf("主 IP 应为解析结果并保留手动项: %q", got)
	}
}

// TestLegacyRelayNodeIP 验证旧面板“IP#源节点ID”格式：自动更新只替换不带 # 的主 IP、保留专用绑定；
// 同步只更新目标 IP 已失效的中转规则，不静默改变仍有效的旧规则；xcat checkNodeIP 列出新旧规则结果不同的规则。
func TestLegacyRelayNodeIP(t *testing.T) {
	e, _ := adminEnv(t)
	ctx := context.Background()
	orig := lookupIPv4
	lookupIPv4 = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "legacy.example.com" {
			return []net.IP{net.ParseIP("203.0.113.99")}, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { lookupIPv4 = orig })

	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(51,'旧目标',1,'legacy.example.com','aes-256-gcm','','',0,1,0,'198.51.100.10#58,203.0.113.20',0),
		(52,'多 IP 目标',1,'1.1.1.1','aes-256-gcm','','',0,1,0,'1.1.1.1,5.6.7.8',0),
		(58,'源 58',1,'198.51.100.58','aes-256-gcm','','',10,1,0,'198.51.100.58',0),
		(59,'源 59',1,'198.51.100.59','aes-256-gcm','','',10,1,0,'198.51.100.59',0)`)
	e.exec(`INSERT INTO relay (id,user_id,source_node_id,dist_node_id,dist_ip,port,priority) VALUES
		(901,0,58,51,'198.51.100.10',0,1),
		(902,0,59,51,'203.0.113.20',0,1),
		(903,0,59,52,'5.6.7.8',0,1)`)
	if err := e.app.RunJob(ctx, "syncnode"); err != nil {
		t.Fatal(err)
	}
	n, _ := e.app.Q.GetNode(ctx, 51)
	if n.NodeIp != "198.51.100.10#58,203.0.113.99" {
		t.Fatalf("同步应只替换主 IP 并保留 #58: %q", n.NodeIp)
	}
	distIP := func(id int) string {
		var ip string
		_ = e.app.DB.QueryRowContext(ctx, "SELECT dist_ip FROM relay WHERE id = ?", id).Scan(&ip)
		return ip
	}
	for id, want := range map[int]string{
		901: "198.51.100.10", // 专用目标仍有效，不变
		902: "203.0.113.99",  // 原默认目标已失效，改为新的主 IP
		903: "5.6.7.8",       // 旧数据（原版默认取最后一项）仍有效，不静默改为新规则的 1.1.1.1
	} {
		if got := distIP(id); got != want {
			t.Errorf("规则 %d 目标 IP = %q, want %q", id, got, want)
		}
	}

	var out strings.Builder
	if err := e.app.XCat(ctx, []string{"checkNodeIP"}, &out); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "规则 903 ") || !strings.Contains(s, "原版规则 5.6.7.8  新版规则 1.1.1.1") || strings.Contains(s, "规则 902 ") {
		t.Fatalf("checkNodeIP 输出不符: %s", s)
	}
	if s := out.String(); !strings.Contains(s, "规则 901 ") || !strings.Contains(s, "当前保存 198.51.100.10  原版规则 203.0.113.99  新版规则 198.51.100.10") {
		t.Fatalf("checkNodeIP 应列出新旧规则结果不同的专用目标: %s", s)
	}
	if distIP(903) != "5.6.7.8" {
		t.Fatal("checkNodeIP 不应修改数据")
	}

	// 后台保存：表单中的 # 专用项原样保留，主 IP 取解析结果。
	ip, msg := e.app.resolveNodeIP(ctx, "legacy.example.com", "198.51.100.10#58,203.0.113.20,10.0.0.1", nil)
	if msg != "" || ip != "198.51.100.10#58,203.0.113.99,10.0.0.1" {
		t.Fatalf("保存时应保留专用项与手动项: %q %q", ip, msg)
	}
	ip, msg = e.app.resolveNodeIP(ctx, "legacy.example.com", "198.51.100.10#58", nil)
	if msg != "" || ip != "203.0.113.99,198.51.100.10#58" {
		t.Fatalf("只有专用项时主 IP 加在最前: %q %q", ip, msg)
	}
}

// TestComposeNodeServerRelay 验证节点地址中直接粘贴中转后缀时，保存统一为 “;||”，原节点配置（含空字段）不变。
func TestComposeNodeServerRelay(t *testing.T) {
	vm := "real.example.com;443;0;ws;tls;path=/ws"
	for _, c := range []struct {
		sort          int32
		in, want, err string
	}{
		{11, vm + "||relay.example.net;30001", vm + ";||relay.example.net;30001", ""},
		{11, vm + ";||relay.example.net;30001", vm + ";||relay.example.net;30001", ""},
		{11, "real.example.com;443;0;tcp;;||relay.example.net;30001", "real.example.com;443;0;tcp;;||relay.example.net;30001", ""},
		{14, "ss.example.com;8443;" + fixtureKey + "||203.0.113.5;30002", "ss.example.com;8443;" + fixtureKey + ";||203.0.113.5;30002", ""},
		{11, vm, vm, ""},
		{11, vm + "||relay.example.net", "", "中转格式无效"},
		{0, "ss.example.com||relay.example.net;30001", "", "不支持外部中转"},
	} {
		got, msg := composeNodeServer(c.sort, c.in, "", "")
		if c.err != "" {
			if !strings.Contains(msg, c.err) {
				t.Errorf("%q: 应报错 %q，得到 %q %q", c.in, c.err, got, msg)
			}
			continue
		}
		if msg != "" || got != c.want {
			t.Errorf("%q: 得到 %q %q, want %q", c.in, got, msg, c.want)
		}
		// 统一后读取结果与原文一致。
		if a, b := sub.RealServer(got), sub.RealServer(c.in); a != b {
			t.Errorf("%q: 原节点配置应不变: %q vs %q", c.in, a, b)
		}
	}
}
