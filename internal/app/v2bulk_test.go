package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

type bulkUser struct {
	id    uint32
	class int
}

// insertBulkUsers 一次插入 n 个测试用户（邮箱前缀 prefix，等级由 SQL 表达式 classExpr 按序号 n 计算），按 ID 升序返回。
func insertBulkUsers(t *testing.T, e *testEnv, prefix string, n int, classExpr string) []bulkUser {
	t.Helper()
	digits := "(SELECT 0 d UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9)"
	e.exec(`INSERT INTO user (user_name,email,pass,passwd,reg_date,im_value,pac,remark,forbidden_ip,forbidden_port,disconnect_ip,class,class_expire,expire_in,enable,port,transfer_enable)
		SELECT CONCAT(?,n), CONCAT(?,n,'@example.com'),'x','p',UTC_TIMESTAMP(),'','','','','','', `+classExpr+`,'1989-06-04 00:05:00','2099-06-04 00:05:00',1,20000+n,107374182400
		FROM (SELECT a.d*1000+b.d*100+c.d*10+e.d AS n FROM `+digits+` a, `+digits+` b, `+digits+` c, `+digits+` e) s
		WHERE n < ?`, prefix, prefix, n)
	rows, err := e.app.DB.QueryContext(context.Background(), "SELECT id, class FROM user WHERE email LIKE ? ORDER BY id", prefix+"%@example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var users []bulkUser
	for rows.Next() {
		var u bulkUser
		if err := rows.Scan(&u.id, &u.class); err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return users
}

// TestV2BulkReports 验证大批次上报（跨越批量语句的 500 行与用户查询的 1000 个分组）：
// 每个用户按倍率准确计费、只为计费的用户写流量记录、跳过的用户不计费，在线 IP 全部写入。
func TestV2BulkReports(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "bulk-key" })
	ctx := context.Background()
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip) VALUES
		(33,'n',1,?,'2022-blake3-aes-256-gcm','','',14,1.5,1,'127.0.0.1')`, "n.example.com;8443;"+fixtureKey)
	// 1205 个用户，每 7 个中有 1 个为 0 级（不属于本节点）。
	users := insertBulkUsers(t, e, "bulk", 1205, "IF(n % 7 = 0, 0, 1)")
	if len(users) != 1205 {
		t.Fatalf("测试用户数: %d", len(users))
	}

	type delta struct {
		UserID uint32 `json:"user_id"`
		U      int64  `json:"u"`
		D      int64  `json:"d"`
	}
	var data []delta
	var raw, billedRaw int64
	billed, skipped := 0, 0
	for _, u := range users {
		d := delta{u.id, int64(u.id%97) + 1, int64(u.id)*3 + 1}
		data = append(data, d)
		raw += d.U + d.D
		if u.class == 1 {
			billed++
			billedRaw += d.U + d.D
		} else {
			skipped++
		}
	}
	for id := uint32(999991); id <= 999993; id++ { // 不存在的用户
		data = append(data, delta{id, 5, 5})
		raw += 10
	}
	body, _ := json.Marshal(map[string]any{"batch_id": "bulk", "reported_at": time.Now().Unix(), "data": data})
	status, _, resp := e.response("POST", "/mod_mu/v2/nodes/33/traffic?key=bulk-key", string(body))
	if status != 200 {
		t.Fatalf("大批次应成功: %d %s", status, resp)
	}
	var out struct {
		Ret     int              `json:"ret"`
		Ignored []map[string]any `json:"ignored"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil || out.Ret != 1 || len(out.Ignored) != skipped+3 {
		t.Fatalf("ignored 数量应为 %d: %v %s", skipped+3, err, resp)
	}
	for _, u := range users {
		got, _ := e.app.Q.GetUser(ctx, u.id)
		wantU, wantD := int64(math.Round(float64(int64(u.id%97)+1)*1.5)), int64(math.Round(float64(int64(u.id)*3+1)*1.5))
		if u.class == 0 {
			wantU, wantD = 0, 0
		}
		if got.U != wantU || got.D != wantD || (u.class == 1) != (got.T > 0) {
			t.Fatalf("用户 %d 计费不正确: u=%d d=%d t=%d，应为 u=%d d=%d", u.id, got.U, got.D, got.T, wantU, wantD)
		}
	}
	var logs int
	var logRaw int64
	_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(u + d), 0) FROM user_traffic_log WHERE node_id = 33").Scan(&logs, &logRaw)
	if logs != billed || logRaw != billedRaw {
		t.Fatalf("流量记录应为 %d 条、原始流量 %d: %d 条、%d", billed, billedRaw, logs, logRaw)
	}
	if n, _ := e.app.Q.GetNode(ctx, 33); n.NodeBandwidth != raw {
		t.Fatalf("节点已用流量应为 %d（含跳过的条目）: %d", raw, n.NodeBandwidth)
	}

	// 在线 IP：0 级用户不能使用本节点，忽略；其余全部写入。
	type entry struct {
		UserID uint32 `json:"user_id"`
		IP     string `json:"ip"`
	}
	var ips []entry
	for _, u := range users {
		ips = append(ips, entry{u.id, fmt.Sprintf("10.%d.%d.%d", u.id>>16&255, u.id>>8&255, u.id&255)})
	}
	body, _ = json.Marshal(map[string]any{"data": ips})
	status, _, resp = e.response("POST", "/mod_mu/v2/nodes/33/alive_ip?key=bulk-key", string(body))
	if status != 200 || resp != fmt.Sprintf(`{"accepted":%d,"data":"ok","ignored":%d,"ret":1}`, billed, skipped)+"\n" {
		t.Fatalf("在线 IP 上报结果不正确: %d %q", status, resp)
	}
	var alive int
	_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM alive_ip WHERE nodeid = 33").Scan(&alive)
	if alive != billed {
		t.Fatalf("在线 IP 记录数应为 %d: %d", billed, alive)
	}
}
