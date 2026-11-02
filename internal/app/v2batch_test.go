package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

// TestV2BatchWindow 验证流量批次的去重约定：只接受 7 天内的批次（过期 410、未来超过 10 分钟 400），
// reported_at 参与内容校验；每日任务分批清理 30 天前的去重记录，接受窗口内的记录不会被清理。
func TestV2BatchWindow(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "batch-key" })
	ctx := context.Background()
	uid := e.login("batch@example.com")
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(31,'n',1,?,'2022-blake3-aes-256-gcm','','',14,1,0,'127.0.0.1',0)`, "n.example.com;8443;"+fixtureKey)
	post := func(batch string, reported int64) (int, string) {
		t.Helper()
		body := fmt.Sprintf(`{"batch_id":%q,"reported_at":%d,"data":[{"user_id":%d,"u":100,"d":200}]}`, batch, reported, uid)
		if reported == 0 {
			body = fmt.Sprintf(`{"batch_id":%q,"data":[{"user_id":%d,"u":100,"d":200}]}`, batch, uid)
		}
		status, _, resp := e.response("POST", "/mod_mu/v2/nodes/31/traffic?key=batch-key", body)
		return status, resp
	}
	now := time.Now()
	for _, c := range []struct {
		name     string
		batch    string
		reported int64
		want     int
	}{
		{"缺少 reported_at", "b-missing", 0, 400},
		{"超过 7 天", "b-old", now.Add(-8 * 24 * time.Hour).Unix(), 410},
		{"未来超过 10 分钟", "b-future", now.Add(time.Hour).Unix(), 400},
		{"未来 5 分钟以内（时钟误差）", "b-skew", now.Add(5 * time.Minute).Unix(), 200},
		{"6 天前（窗口内）", "b-6d", now.Add(-6 * 24 * time.Hour).Unix(), 200},
	} {
		if status, body := post(c.batch, c.reported); status != c.want {
			t.Fatalf("%s: 期望 %d，实际 %d %s", c.name, c.want, status, body)
		}
	}
	// 同一批次号、不同 reported_at 视为内容冲突。
	if status, _ := post("b-6d", now.Add(-5*24*time.Hour).Unix()); status != 409 {
		t.Fatalf("reported_at 不同应为冲突: %d", status)
	}
	if u, _ := e.app.Q.GetUser(ctx, uid); u.U != 200 || u.D != 400 {
		t.Fatalf("只有两个被接受的批次计费: u=%d d=%d", u.U, u.D)
	}

	// 分批清理：12000 条 30 天前的记录与 5 条较新的记录。
	insert := func(prefix string, n int, age time.Duration) {
		for start := 0; start < n; start += 1000 {
			var rows []string
			var args []any
			for i := start; i < min(start+1000, n); i++ {
				rows = append(rows, "(31,?,?,?)")
				args = append(args, fmt.Sprintf("%s%d", prefix, i), strings.Repeat("a", 64), now.Add(-age).Unix())
			}
			e.exec("INSERT INTO node_traffic_batch (node_id,batch_id,payload_hash,created_at) VALUES "+strings.Join(rows, ","), args...)
		}
	}
	insert("old-", 12000, 31*24*time.Hour)
	insert("recent-", 5, 29*24*time.Hour)
	trafficBatchCleanPause = 0
	if err := e.app.RunJob(ctx, "dailyjob"); err != nil {
		t.Fatal(err)
	}
	count := func(prefix string) (n int) {
		_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM node_traffic_batch WHERE batch_id LIKE ?", prefix+"%").Scan(&n)
		return n
	}
	if count("old-") != 0 || count("recent-") != 5 || count("b-") != 2 {
		t.Fatalf("清理结果错误: old=%d recent=%d accepted=%d", count("old-"), count("recent-"), count("b-"))
	}
	// 窗口内批次的去重记录仍在：重复提交返回 duplicate，不再计费。
	if _, body := post("b-6d", now.Add(-6*24*time.Hour).Unix()); !strings.Contains(body, `"duplicate":true`) {
		t.Fatalf("清理后窗口内批次仍应去重: %s", body)
	}
}

// TestV2TrafficIgnoredUsers 批次中有已删除、或等级 / 分组已不符合本节点的用户（如套餐到期降为 0 级）时，
// 只跳过这几条、不计费，其余用户照常入账，整批返回成功；不能整批拒绝而挡住该节点之后的所有流量。
func TestV2TrafficIgnoredUsers(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "batch-key" })
	ctx := context.Background()
	active := e.login("active@example.com")
	expired := e.login2("expired@example.com")
	admin := e.login2("admin@example.com")
	other := e.login2("other-group@example.com")
	e.exec("UPDATE user SET class = 1 WHERE id IN (?, ?)", active, other)
	e.exec("UPDATE user SET class = 0 WHERE id = ?", expired) // 套餐到期被每分钟任务降为 0 级
	e.exec("UPDATE user SET class = 0, is_admin = 1 WHERE id = ?", admin)
	e.exec("UPDATE user SET node_group = 2 WHERE id = ?", other)
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(32,'n',1,?,'2022-blake3-aes-256-gcm','','',14,1,1,'127.0.0.1',1)`, "n.example.com;8443;"+fixtureKey)
	e.exec("UPDATE user SET node_group = 1 WHERE id IN (?, ?, ?)", active, expired, admin)
	body := fmt.Sprintf(`{"batch_id":"mixed","reported_at":%d,"data":[{"user_id":%d,"u":10,"d":20},{"user_id":%d,"u":30,"d":40},{"user_id":999999,"u":50,"d":60},{"user_id":%d,"u":1,"d":2},{"user_id":%d,"u":7,"d":8}]}`,
		time.Now().Unix(), active, expired, admin, other)
	status, _, resp := e.response("POST", "/mod_mu/v2/nodes/32/traffic?key=batch-key", body)
	if status != 200 || !strings.Contains(resp, `"ret":1`) || !strings.Contains(resp, `"duplicate":false`) {
		t.Fatalf("混合批次应整批成功: %d %s", status, resp)
	}
	for _, want := range []string{fmt.Sprintf(`{"reason":"user_not_on_node","user_id":%d}`, expired), `{"reason":"user_not_found","user_id":999999}`, fmt.Sprintf(`{"reason":"user_not_on_node","user_id":%d}`, other)} {
		if !strings.Contains(resp, want) {
			t.Errorf("ignored 缺少 %s: %s", want, resp)
		}
	}
	used := func(id uint32) int64 { u, _ := e.app.Q.GetUser(ctx, id); return u.U + u.D }
	if used(active) != 30 || used(admin) != 3 {
		t.Fatalf("可服务的用户应照常计费: active=%d admin=%d", used(active), used(admin))
	}
	if used(expired) != 0 || used(other) != 0 {
		t.Fatalf("跳过的用户不应计费: expired=%d other=%d", used(expired), used(other))
	}
	var logs int
	_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_traffic_log WHERE node_id = 32").Scan(&logs)
	if logs != 2 {
		t.Fatalf("只为计费的用户写流量记录: %d", logs)
	}
	n, _ := e.app.Q.GetNode(ctx, 32)
	if n.NodeBandwidth != 228 {
		t.Fatalf("节点已用流量按实际转发计算（含跳过的条目）: %d", n.NodeBandwidth)
	}
	// 重试同一批：去重，不重复计费。
	if status, _, resp := e.response("POST", "/mod_mu/v2/nodes/32/traffic?key=batch-key", body); status != 200 || !strings.Contains(resp, `"duplicate":true`) {
		t.Fatalf("重试应返回 duplicate: %d %s", status, resp)
	}
	if used(active) != 30 {
		t.Fatal("重试不应重复计费")
	}
	// 整批都是跳过的用户：同样成功，之后的批次不会被挡住。
	only := fmt.Sprintf(`{"batch_id":"all-ignored","reported_at":%d,"data":[{"user_id":%d,"u":5,"d":5}]}`, time.Now().Unix(), expired)
	if status, _, resp := e.response("POST", "/mod_mu/v2/nodes/32/traffic?key=batch-key", only); status != 200 || !strings.Contains(resp, `"ret":1`) {
		t.Fatalf("全部跳过的批次也应成功: %d %s", status, resp)
	}
	next := fmt.Sprintf(`{"batch_id":"after","reported_at":%d,"data":[{"user_id":%d,"u":1,"d":1}]}`, time.Now().Unix(), active)
	if status, _, _ := e.response("POST", "/mod_mu/v2/nodes/32/traffic?key=batch-key", next); status != 200 || used(active) != 32 {
		t.Fatalf("之后的批次应正常入账: %d %d", status, used(active))
	}
}
