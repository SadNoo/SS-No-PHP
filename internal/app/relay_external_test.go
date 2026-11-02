package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

// flattenSubscription 解开订阅内容中的 Base64 层与 vmess:// 链接，便于检查其中的地址。
func flattenSubscription(body string) string {
	text := body
	trimmed := strings.TrimSpace(body)
	if b, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		text = string(b)
	} else if b, err := base64.RawStdEncoding.DecodeString(trimmed); err == nil { // 旧格式不带填充
		text = string(b)
	}
	var out strings.Builder
	out.WriteString(text)
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "vmess://"); ok {
			if b, err := base64.StdEncoding.DecodeString(rest); err == nil {
				out.WriteString("\n" + string(b))
			}
		}
	}
	return out.String()
}

// TestExternalRelay 验证外部 NAT 中转：全部订阅格式统一使用中转入口；节点后端、旧接口、节点 IP 同步使用真实地址；
// 后台“中转地址/中转端口”输入框的拆分、回显、校验与拼接。
func TestExternalRelay(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "relay-key" })
	ctx := context.Background()
	uid := e.login("relay@example.com")
	e.exec("UPDATE user SET class = 1, class_expire = ?, transfer_enable = 1073741824 WHERE id = ?", time.Now().AddDate(0, 1, 0), uid)
	const pbk = fixturePublic
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(51,'香港 SS2022',1,?,'2022-blake3-aes-256-gcm','','',14,1,0,'198.51.100.51',0),
		(52,'日本 VMess',1,'real-vm.example.com;443;0;ws;tls;path=/v2;||relay-vm.example.net;30002','','','',11,1,0,'198.51.100.52',0),
		(53,'美国 REALITY',1,?,'','','',15,1,0,'198.51.100.53',0),
		(54,'新加坡 AnyTLS',1,'real-at.example.com;8443;0;tcp;anytls;sni=real-at.example.com;||relay-at.example.net;30004','','','',16,1,0,'198.51.100.54',0)`,
		"real-ss.example.com;8443;"+fixtureKey+";||relay-ss.example.net;30001",
		"real-vl.example.com;443;0;tcp;reality;sni=www.microsoft.com|pbk="+pbk+"|sid=;||relay-vl.example.net;30003")
	token, err := e.app.Sub.SubToken(ctx, uid, 0)
	if err != nil {
		t.Fatal(err)
	}
	realHosts := []string{"real-ss.example.com", "real-vl.example.com"} // 无 TLS / REALITY 用独立 SNI：任何输出都不应出现
	formats := []string{"v2ray=1", "clash=1", "surge=5", "surge=4", "loon=1", "quantumultx=1", "shadowrocket=1", "ss2022=1", "vless=1",
		"anytls=1", "sing-box=1", "surfboard=1", "quantumult=3", "kitsunebi=1", "mu=2", "mu=4"}
	for _, q := range formats {
		status, _, body := e.response("GET", "/link/"+token+"?"+q, "")
		if status != 200 || body == "" {
			t.Fatalf("%s: %d %s", q, status, body)
		}
		if q == "kitsunebi=1" {
			// Kitsunebi 链接无法表达与连接地址不同的 SNI：中转后 SNI（原域名）≠ 连接地址（中转），
			// 这类 VMess TLS 节点按既有规则不输出，不会通过关闭证书校验来兼容。
			if strings.TrimSpace(flattenSubscription(body)) != "" {
				t.Errorf("kitsunebi: TLS 中转节点不应输出: %q", body)
			}
			continue
		}
		flat := flattenSubscription(body)
		for _, h := range realHosts {
			if strings.Contains(flat, h) {
				t.Errorf("%s: 输出中出现了真实节点地址 %s", q, h)
			}
		}
		if !strings.Contains(flat, "relay-") && !strings.Contains(flat, "30002") {
			t.Errorf("%s: 输出中没有中转入口: %q", q, flat)
		}
		for _, port := range []string{":8443", ", 8443", `"port":8443`, `"server_port":8443`, `port: 8443`} {
			if strings.Contains(flat, port) {
				t.Errorf("%s: 输出中出现了真实端口 %q", q, port)
			}
		}
	}
	// VMess：连接中转，SNI / Host 固定为原域名。
	_, _, body := e.response("GET", "/link/"+token+"?v2ray=1", "")
	flat := flattenSubscription(body)
	if !strings.Contains(flat, `"add":"relay-vm.example.net"`) || !strings.Contains(flat, `"sni":"real-vm.example.com"`) || !strings.Contains(flat, `"host":"real-vm.example.com"`) {
		t.Fatalf("VMess 中转输出错误: %s", flat)
	}
	// sing-box：四个协议都连接中转，SNI 不变。
	_, _, body = e.response("GET", "/link/"+token+"?sing-box=1", "")
	var sb struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(body), &sb); err != nil {
		t.Fatal(err)
	}
	servers := map[string]bool{}
	for _, o := range sb.Outbounds {
		if s, ok := o["server"].(string); ok {
			servers[s] = true
			if tls, ok := o["tls"].(map[string]any); ok && o["type"] == "anytls" && tls["server_name"] != "real-at.example.com" {
				t.Errorf("AnyTLS SNI 应保持原域名: %v", tls)
			}
		}
	}
	for _, s := range []string{"relay-ss.example.net", "relay-vm.example.net", "relay-vl.example.net", "relay-at.example.net"} {
		if !servers[s] {
			t.Errorf("sing-box 缺少中转入口 %s: %v", s, servers)
		}
	}

	// 节点详情页（用户复制的链接）同样使用中转入口。
	for _, id := range []string{"51", "53", "54"} {
		page := e.page("/user/node/" + id)
		if !strings.Contains(page, "relay-") || strings.Contains(page, "real-ss.example.com") || strings.Contains(page, "real-vl.example.com") {
			t.Errorf("节点 %s 详情页应显示中转入口", id)
		}
	}

	// 节点后端（WebAPI v2 与旧接口）使用真实地址和端口。
	for id, want := range map[string][2]string{"51": {`"server":"real-ss.example.com"`, `"server_port":8443`}, "52": {`"server":"real-vm.example.com"`, `"server_port":443`},
		"53": {`"server":"real-vl.example.com"`, `"server_port":443`}, "54": {`"server":"real-at.example.com"`, `"server_port":8443`}} {
		_, _, cfg := e.response("GET", "/mod_mu/v2/nodes/"+id+"/config?key=relay-key", "")
		if !strings.Contains(cfg, want[0]) || !strings.Contains(cfg, want[1]) || strings.Contains(cfg, "relay-") || strings.Contains(cfg, "3000") {
			t.Errorf("v2 配置 %s 应为真实地址: %s", id, cfg)
		}
	}
	_, _, legacy := e.response("GET", "/mod_mu/nodes/51/info?key=relay-key&node_id=51", "")
	if !strings.Contains(legacy, `real-ss.example.com;8443;`) || strings.Contains(legacy, "relay-") {
		t.Errorf("旧接口应返回真实节点地址: %s", legacy)
	}

	// 节点 IP 同步只解析真实地址，不解析中转入口。
	var mu sync.Mutex
	var looked []string
	orig := lookupIPv4
	lookupIPv4 = func(_ context.Context, host string) ([]net.IP, error) {
		mu.Lock()
		defer mu.Unlock()
		looked = append(looked, host)
		if strings.HasPrefix(host, "real-") {
			return []net.IP{net.ParseIP("198.51.100.99")}, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { lookupIPv4 = orig })
	if err := e.app.RunJob(ctx, "syncnode"); err != nil {
		t.Fatal(err)
	}
	for _, h := range looked {
		if strings.HasPrefix(h, "relay-") {
			t.Errorf("同步不应解析中转地址: %s", h)
		}
	}
	if n, _ := e.app.Q.GetNode(ctx, 51); n.NodeIp != "198.51.100.99" {
		t.Errorf("节点 IP 应为真实地址的解析结果: %q", n.NodeIp)
	}

	// 后台：编辑页拆分回显。
	e.exec("UPDATE user SET is_admin = 1 WHERE id = ?", uid)
	page := e.page("/admin/node/51/edit")
	if !strings.Contains(page, `value="real-ss.example.com;8443;`+fixtureKey+`"`) || !strings.Contains(page, `value="relay-ss.example.net"`) || !strings.Contains(page, `value="30001"`) {
		t.Fatal("编辑页应分别显示原节点配置与中转地址、端口")
	}
	form := func(server, host, port, sort string) url.Values {
		return url.Values{"name": {"中转节点"}, "server": {server}, "relay_host": {host}, "relay_port": {port}, "sort": {sort}, "type": {"1"},
			"rate": {"1"}, "class": {"0"}, "group": {"0"}, "node_ip": {"198.51.100.70"}, "node_speedlimit": {"0"}, "node_bandwidth_limit": {"0"},
			"bandwidthlimit_resetday": {"1"}, "custom_method": {"0"}, "custom_rss": {"1"}, "mu_only": {"0"}, "status": {"可用"}, "info": {""}, "method": {""}}
	}
	server := func(id string) string {
		var s string
		_ = e.app.DB.QueryRowContext(ctx, "SELECT server FROM ss_node WHERE id = ?", id).Scan(&s)
		return s
	}
	ssBase := "198.51.100.70;8443;" + fixtureKey
	// 拼接保存为 ;|| 形式。
	if body := e.raw("PUT", "/admin/node/51", "application/x-www-form-urlencoded", form(ssBase, "relay2.example.net", "40001", "14").Encode()); !strings.Contains(body, "修改成功") {
		t.Fatalf("保存中转失败: %s", body)
	}
	if got := server("51"); got != ssBase+";||relay2.example.net;40001" {
		t.Fatalf("保存格式错误: %q", got)
	}
	// 清空中转：恢复原格式。
	if body := e.raw("PUT", "/admin/node/51", "application/x-www-form-urlencoded", form(ssBase, "", "", "14").Encode()); !strings.Contains(body, "修改成功") || server("51") != ssBase {
		t.Fatalf("清空中转失败: %s %q", body, server("51"))
	}
	for name, f := range map[string]url.Values{
		"只填地址":       form(ssBase, "relay.example.net", "", "14"),
		"端口无效":       form(ssBase, "relay.example.net", "70000", "14"),
		"地址无效":       form(ssBase, "bad host", "443", "14"),
		"重复中转":       form(ssBase+";||1.2.3.4;443", "relay.example.net", "443", "14"),
		"不支持的类型":     form("198.51.100.70", "relay.example.net", "443", "0"),
		"节点地址中后缀不合法": form(ssBase+";||1.2.3.4", "", "", "14"),
	} {
		if body := e.raw("PUT", "/admin/node/51", "application/x-www-form-urlencoded", f.Encode()); strings.Contains(body, "修改成功") {
			t.Errorf("%s: 应拒绝保存: %s", name, body)
		}
	}
	if server("51") != ssBase {
		t.Fatal("被拒绝的保存不应修改数据")
	}
}
