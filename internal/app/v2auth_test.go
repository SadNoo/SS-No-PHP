package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

// TestV2AuthRoutes 通过完整 HTTP 路由验证 WebAPI v2 认证：密钥、路径节点 ID 绑定、查询参数不能覆盖路径 ID、
// 来源 IP（登记 IP / 当场重新解析 / DDNS 宽限期），以及旧接口行为不变。来源 IP 通过可信代理头模拟。
func TestV2AuthRoutes(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "v2-test-key" })
	ctx := context.Background()
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(21,'动态节点',1,?,'2022-blake3-aes-256-gcm','','',14,1,0,'198.51.100.21',0),
		(22,'固定节点',1,?,'2022-blake3-aes-256-gcm','','',14,1,0,'198.51.100.22',0)`,
		"ddns.example.com;8443;"+fixtureKey, "198.51.100.22;8443;"+fixtureKey)
	var mu sync.Mutex
	dns := map[string][]string{"ddns.example.com": {"198.51.100.21"}}
	e.app.lookupIP = func(_ context.Context, host string) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		return dns[host], nil
	}
	get := func(ip, path string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
		if ip != "" {
			req.Header.Set("X-Forwarded-For", ip)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var b strings.Builder
		_, _ = io.Copy(&b, resp.Body)
		return resp.StatusCode, b.String()
	}
	const key = "key=v2-test-key"
	expect := func(name, ip, path string, want int) {
		t.Helper()
		if code, body := get(ip, path); code != want {
			t.Fatalf("%s: 期望 %d，实际 %d: %s", name, want, code, body)
		}
	}

	expect("登记 IP", "198.51.100.21", "/mod_mu/v2/nodes/21/config?"+key, 200)
	expect("密钥错误", "198.51.100.21", "/mod_mu/v2/nodes/21/config?key=wrong", 403)
	expect("缺少密钥", "198.51.100.21", "/mod_mu/v2/nodes/21/config", 403)
	expect("重复密钥", "198.51.100.21", "/mod_mu/v2/nodes/21/config?"+key+"&"+key, 403)
	expect("不存在的节点", "198.51.100.21", "/mod_mu/v2/nodes/999/config?"+key, 403)
	expect("节点 ID 为 0", "198.51.100.21", "/mod_mu/v2/nodes/0/config?"+key, 400)
	expect("节点 ID 非数字", "198.51.100.21", "/mod_mu/v2/nodes/abc/config?"+key, 400)
	expect("节点 ID 带前导零", "198.51.100.21", "/mod_mu/v2/nodes/021/config?"+key, 400)
	expect("查询参数与路径一致", "198.51.100.21", "/mod_mu/v2/nodes/21/config?"+key+"&node_id=21", 200)
	expect("查询参数不能覆盖路径", "198.51.100.21", "/mod_mu/v2/nodes/21/config?"+key+"&node_id=22", 400)
	expect("查询参数为 0 也不能覆盖", "198.51.100.21", "/mod_mu/v2/nodes/21/config?"+key+"&node_id=0", 400)
	expect("跨节点：用 21 的 IP 访问 22", "198.51.100.21", "/mod_mu/v2/nodes/22/users?"+key, 403)
	expect("固定节点自己的 IP", "198.51.100.22", "/mod_mu/v2/nodes/22/users?"+key, 200)

	// 节点仍在从登记 IP 正常上报：其他 IP 即使密钥正确也被拒绝。
	expect("正常运行时其他 IP", "203.0.113.9", "/mod_mu/v2/nodes/21/users?"+key, 403)

	// DNS 已更新：当场重新解析后通过，并更新节点 IP。
	mu.Lock()
	dns["ddns.example.com"] = []string{"203.0.113.50"}
	mu.Unlock()
	e.app.nodeAuth.dns = nil
	expect("域名已指向新 IP", "203.0.113.50", "/mod_mu/v2/nodes/21/users?"+key, 200)
	if n, _ := e.app.Q.GetNode(ctx, 21); !strings.HasPrefix(n.NodeIp, "203.0.113.50") {
		t.Fatalf("节点 IP 应更新: %q", n.NodeIp)
	}

	// DDNS 尚未更新：节点停止从登记 IP 上报超过 90 秒后，宽限期内放行；超过宽限期拒绝。
	e.app.nodeAuth.dns = nil
	e.app.nodeAuth.markVerified(21, time.Now().Add(-2*time.Minute))
	expect("宽限期内", "203.0.113.77", "/mod_mu/v2/nodes/21/users?"+key, 200)
	expect("宽限期只对路径中的节点生效", "203.0.113.77", "/mod_mu/v2/nodes/22/users?"+key, 403)
	e.app.nodeAuth.markVerified(21, time.Now().Add(-11*time.Minute))
	expect("超过宽限期", "203.0.113.77", "/mod_mu/v2/nodes/21/users?"+key, 403)

	// 旧接口行为不变：仍返回原版 JSON 格式。
	if code, body := get("203.0.113.88", "/mod_mu/users?"+key+"&node_id=21"); code != 200 || !strings.Contains(body, `"ret":0`) {
		t.Fatalf("旧接口行为变化: %d %s", code, body)
	}
	if code, body := get("198.51.100.22", "/mod_mu/users?"+key+"&node_id=22"); code != 200 || !strings.Contains(body, `"ret":1`) {
		t.Fatalf("旧接口应正常: %d %s", code, body)
	}
}
