package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

func TestEffectiveNodePolicy(t *testing.T) {
	now := int64(10000)
	blocks := map[string]int64{"equal": 9900, "newer-unblock": 9900, "old-unblock": 9900, "expired": 6399, "boundary": 6400, "future": 10001}
	got := effectiveNodePolicy(blocks, map[string]int64{"equal": 9900, "newer-unblock": 9901, "old-unblock": 9899}, now)
	b, _ := json.Marshal(got)
	if string(b) != `[{"ip":"boundary","blocked_at":6400},{"ip":"equal","blocked_at":9900},{"ip":"old-unblock","blocked_at":9900}]` {
		t.Fatalf("unexpected effective policy: %s", b)
	}
	b, _ = json.Marshal(effectiveNodePolicy(nil, nil, now))
	if string(b) != "[]" {
		t.Fatal("empty must be an array")
	}
	for raw, want := range map[string]string{"::ffff:192.0.2.1": "192.0.2.1", "[fe80::1%eth0]": "fe80::1", " 2001:db8::01 ": "2001:db8::1"} {
		ip, err := canonicalPolicyIP(raw)
		if err != nil || ip != want {
			t.Fatalf("canonical %q: %q %v", raw, ip, err)
		}
	}
	if _, err := canonicalPolicyIP("example.com"); err == nil {
		t.Fatal("invalid IP accepted")
	}
}

func TestV2NodePolicySnapshot(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "policy-test-key" })
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_speedlimit,node_ip,node_group,custom_rss,mu_only,custom_method)
 VALUES (1,'test',1,'example.com;12345;0;tcp;;','none','','ok',11,1,0,0,'198.51.100.1',0,1,0,0),
 (2,'other',1,'example.com;12346;0;tcp;;','none','','ok',11,1,0,0,'192.0.2.10',0,1,0,0)`)
	e.app.lookupIP = func(context.Context, string) ([]string, error) { return nil, nil }
	get := func(path string) (*http.Response, error) {
		req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
		req.Header.Set("X-Forwarded-For", "198.51.100.1")
		return e.c.Do(req)
	}
	now := time.Now().Unix()
	e.exec(fmt.Sprintf(`INSERT INTO blockip (nodeid,ip,datetime) VALUES
 (1,'::ffff:192.0.2.1',%d),(1,'192.0.2.2',%d),(1,'192.0.2.3',%d),
 (1,'192.0.2.4',%d),(2,'192.0.2.5',%d),(0,'192.0.2.6',%d)`, now-300, now-200, now-200, now-3601, now-200, now-200))
	e.exec(fmt.Sprintf(`INSERT INTO unblockip (ip,datetime,userid) VALUES ('192.0.2.1',%d,1),('192.0.2.2',%d,1)`, now-301, now-199))
	e.exec(`INSERT INTO detect_list (id,name,text,regex,type) VALUES (1,'test','','blocked.example',1)`)
	path := "/mod_mu/v2/nodes/1/policy?key=policy-test-key"
	r, err := get(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	var body struct {
		Ret  int                `json:"ret"`
		Data nodePolicySnapshot `json:"data"`
	}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != 200 || body.Ret != 1 || body.Data.NodeID != 1 || body.Data.BlockRetention != 3600 {
		t.Fatalf("bad snapshot: %+v status=%d", body, r.StatusCode)
	}
	if !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("snapshot may be cached")
	}
	if len(body.Data.BlockedIPs) != 2 || body.Data.BlockedIPs[0].IP != "192.0.2.1" || body.Data.BlockedIPs[1].IP != "192.0.2.3" {
		t.Fatalf("partial/delta/cross-node policy: %+v", body.Data.BlockedIPs)
	}
	if len(body.Data.DetectRules) != 1 || body.Data.DetectRules[0].ID != 1 {
		t.Fatal("missing detect rules")
	}
	for _, bad := range []string{"/mod_mu/v2/nodes/1/policy", "/mod_mu/v2/nodes/1/policy?key=wrong", "/mod_mu/v2/nodes/2/policy?key=policy-test-key", "/mod_mu/v2/nodes/1/policy?key=policy-test-key&node_id=2", "/mod_mu/v2/nodes/1/policy?key=policy-test-key&key=policy-test-key"} {
		resp, err := get(bad)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("auth bypass %s", bad)
		}
	}
	e.exec(`UPDATE detect_list SET regex=REPEAT('x',65537)`)
	if got := e.raw("GET", path, "", ""); !strings.Contains(got, `"ret":0`) {
		t.Fatalf("partial rules accepted: %s", got)
	}
	e.exec(`DELETE FROM blockip`)
	e.exec(`DELETE FROM detect_list`)
	if got := e.raw("GET", path, "", ""); !strings.Contains(got, `"blocked_ips":[]`) || !strings.Contains(got, `"detect_rules":[]`) {
		t.Fatalf("not complete empty arrays: %s", got)
	}
}

// TestV2NodePolicyRobust 无效 IP 的封禁 / 解封记录被跳过，不让整个快照失败（P1）；
// 用户在用户中心解封时，节点以其他写法记录的同一 IP 一并删除，解封记录清理后不会重新被封（P2）。
func TestV2NodePolicyRobust(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "policy-test-key" })
	uid := e.login("unblock@example.com")
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_speedlimit,node_ip,node_group,custom_rss,mu_only,custom_method)
 VALUES (1,'test',1,'example.com;12345;0;tcp;;','none','','ok',11,1,0,0,'127.0.0.1',0,1,0,0)`)
	path := "/mod_mu/v2/nodes/1/policy?key=policy-test-key"
	now := time.Now().Unix()
	e.exec(fmt.Sprintf(`INSERT INTO blockip (nodeid,ip,datetime) VALUES (1,'not-an-ip',%d),(1,'无效',%d),(1,'192.0.2.7',%d)`, now-10, now-10, now-10))
	e.exec(fmt.Sprintf(`INSERT INTO unblockip (ip,datetime,userid) VALUES ('also-bad',%d,%d)`, now-5, uid))
	got := e.raw("GET", path, "", "")
	if !strings.Contains(got, `"ret":1`) || !strings.Contains(got, `"ip":"192.0.2.7"`) || strings.Contains(got, "not-an-ip") {
		t.Fatalf("无效记录应跳过，有效封禁照常下发: %s", got)
	}
	e.exec(`DELETE FROM blockip`)
	e.exec(`DELETE FROM unblockip`)

	// 节点以 IPv4 映射写法封禁了用户当前的 IP；用户在用户中心解封（来源 IP 为 127.0.0.1）。
	e.exec(fmt.Sprintf(`INSERT INTO blockip (nodeid,ip,datetime) VALUES (1,'::ffff:127.0.0.1',%d),(1,'127.0.0.1',%d),(1,'192.0.2.8',%d)`, now-600, now-600, now-600))
	if r := e.post("/user/unblock", nil); r["ret"] != float64(1) {
		t.Fatalf("解封失败: %v", r)
	}
	var left int
	_ = e.app.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM blockip WHERE ip LIKE '%127.0.0.1'").Scan(&left)
	if left != 0 {
		t.Fatalf("同一 IP 的其他写法也应删除: 剩 %d 条", left)
	}
	// 5 分钟后解封记录被清理：解封的 IP 不应重新出现在快照中，其他 IP 的封禁不受影响。
	e.exec(`UPDATE unblockip SET datetime = datetime - 400`)
	if err := e.app.cleanShortLived(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	got = e.raw("GET", path, "", "")
	if strings.Contains(got, "127.0.0.1") || !strings.Contains(got, `"ip":"192.0.2.8"`) {
		t.Fatalf("解封的 IP 不应重新被封，其他封禁保留: %s", got)
	}
}

// TestAdminUnblockAllForms 管理后台解封与用户中心相同：删除该 IP 在所有节点上的全部写法（IPv4 映射、IPv6 不同书写），
// 解封记录 5 分钟后被清理也不会重新被封；其他 IP 的封禁保留。
func TestAdminUnblockAllForms(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "policy-test-key" })
	ctx := context.Background()
	uid := e.login("admin-unblock@example.com")
	e.exec("UPDATE user SET is_admin = 1 WHERE id = ?", uid)
	for _, id := range []int{1, 2} {
		e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_speedlimit,node_ip,node_group,custom_rss,mu_only,custom_method)
 VALUES (?,'test',1,'example.com;12345;0;tcp;;','none','','ok',11,1,0,0,'127.0.0.1',0,1,0,0)`, id)
	}
	ago := time.Now().Unix() - 600
	e.exec(`INSERT INTO blockip (nodeid,ip,datetime) VALUES
		(1,'::ffff:192.0.2.55',?),(2,'192.0.2.55',?),
		(1,'2001:0db8:0000:0000:0000:0000:0000:0001',?),(2,'[2001:db8::1]',?),
		(1,'192.0.2.56',?),(1,'::ffff:192.0.2.56',?),(1,'2001:db8::2',?),(1,'garbage',?)`, ago, ago, ago, ago, ago, ago, ago, ago)
	for _, ip := range []string{"192.0.2.55", " 2001:db8::1 "} {
		if r := e.post("/admin/unblock", url.Values{"ip": {ip}}); r["ret"] != float64(1) {
			t.Fatalf("解封 %q 失败: %v", ip, r)
		}
	}
	left := func() []string {
		rows, err := e.app.DB.QueryContext(ctx, "SELECT ip FROM blockip")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var ip string
			_ = rows.Scan(&ip)
			out = append(out, ip)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		slices.Sort(out)
		return out
	}
	if got, want := left(), []string{"192.0.2.56", "2001:db8::2", "::ffff:192.0.2.56", "garbage"}; !slices.Equal(got, want) {
		t.Fatalf("应删除该 IP 在各节点上的全部写法、保留其他 IP: %v", got)
	}
	// 5 分钟后解封记录被清理：解封的 IP 不应重新出现在任何节点的快照中。
	e.exec(`UPDATE unblockip SET datetime = datetime - 400`)
	if err := e.app.cleanShortLived(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	p1 := e.raw("GET", "/mod_mu/v2/nodes/1/policy?key=policy-test-key", "", "")
	if strings.Contains(p1, "192.0.2.55") || strings.Contains(p1, `"2001:db8::1"`) || !strings.Contains(p1, `"ip":"192.0.2.56"`) || !strings.Contains(p1, `"ip":"2001:db8::2"`) {
		t.Fatalf("节点 1 快照不正确: %s", p1)
	}
	if p2 := e.raw("GET", "/mod_mu/v2/nodes/2/policy?key=policy-test-key", "", ""); !strings.Contains(p2, `"blocked_ips":[]`) {
		t.Fatalf("节点 2 的封禁应全部解除: %s", p2)
	}
	// 不是有效 IP 的输入只按原文删除。
	if r := e.post("/admin/unblock", url.Values{"ip": {"garbage"}}); r["ret"] != float64(1) {
		t.Fatalf("解封失败: %v", r)
	}
	if got, want := left(), []string{"192.0.2.56", "2001:db8::2", "::ffff:192.0.2.56"}; !slices.Equal(got, want) {
		t.Fatalf("无效输入只应删除原文记录: %v", got)
	}
}
