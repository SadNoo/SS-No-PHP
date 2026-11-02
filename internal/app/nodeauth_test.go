package app

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestNodeAuthDDNS 覆盖动态 IP 节点：登记 IP 通过、域名已更新时立即通过、DNS 未更新时的宽限期及其限制。
func TestNodeAuthDDNS(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,node_ip,custom_rss,mu_only,custom_method)
		VALUES (5,'动态节点',1,'ddns.example.com','aes-256-cfb','','',0,'1.1.1.1',0,0,0),
		       (6,'固定节点',1,'9.9.9.9','aes-256-cfb','','',0,'9.9.9.9',0,0,0)`)
	var mu sync.Mutex
	dns := map[string][]string{"ddns.example.com": {"1.1.1.1"}}
	lookups := 0
	e.app.lookupIP = func(_ context.Context, host string) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		lookups++
		return dns[host], nil
	}
	auth := func(ip, id string, keyOK bool) bool {
		t.Helper()
		ok, err := e.app.authorizeNode(ctx, ip, id, keyOK)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	if !auth("1.1.1.1", "5", true) {
		t.Fatal("登记 IP 应通过")
	}
	if auth("6.6.6.6", "5", false) || auth("6.6.6.6", "0", true) {
		t.Fatal("未知 IP：密钥错误或未指明节点时不应通过")
	}

	// 节点仍在从登记 IP 正常上报：宽限期不生效（密钥泄露也无法冒充）。
	if auth("2.2.2.2", "5", true) {
		t.Fatal("节点正常运行时，其他 IP 不应通过")
	}

	// 节点换了 IP、DNS 尚未更新：静默超过 90 秒后进入宽限期。
	e.app.nodeAuth.markVerified(5, time.Now().Add(-2*time.Minute))
	if !auth("2.2.2.2", "5", true) {
		t.Fatal("宽限期内应通过")
	}
	if auth("2.2.2.2", "6", true) {
		t.Fatal("宽限期只对该节点自己生效")
	}
	// 超过宽限期。
	e.app.nodeAuth.markVerified(5, time.Now().Add(-11*time.Minute))
	if auth("2.2.2.2", "5", true) {
		t.Fatal("超过宽限期不应通过")
	}
	// 宽限期为 0 时关闭。
	e.app.Cfg.Security.NodeIPGrace = 0
	e.app.nodeAuth.markVerified(5, time.Now().Add(-2*time.Minute))
	if auth("2.2.2.2", "5", true) {
		t.Fatal("宽限期关闭时不应通过")
	}

	// DDNS 已更新：立即重新解析通过，并更新节点 IP。
	mu.Lock()
	dns["ddns.example.com"] = []string{"3.3.3.3"}
	mu.Unlock()
	e.app.nodeAuth.dns = nil // 清空解析缓存
	if !auth("3.3.3.3", "5", true) {
		t.Fatal("域名已指向新 IP 时应立即通过")
	}
	n, _ := e.app.Q.GetNode(ctx, 5)
	if n.NodeIp != "3.3.3.3" {
		t.Fatalf("node_ip 应更新为新 IP: %q", n.NodeIp)
	}

	// 解析结果有缓存，伪造来源的请求不会触发大量 DNS 查询。
	before := lookups
	for range 20 {
		auth("4.4.4.4", "5", true)
	}
	if lookups-before > 1 {
		t.Fatalf("应使用解析缓存: %d 次查询", lookups-before)
	}
}
