package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

// TestV2AliveIP 验证 WebAPI v2 的同时在线 IP 数限制：节点通过 v2 上报在线 IP，面板每分钟检查后，
// 超出的 IP 出现在 v2 用户列表的 blocked_ips 中；上报的校验、去重，以及不属于该节点的用户被忽略。
func TestV2AliveIP(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "alive-key" })
	ctx := context.Background()
	uid := e.login("alive@example.com")
	high := e.login2("alive-high@example.com")
	e.exec("UPDATE user SET class = 5 WHERE id = ?", high)
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(61,'n',1,?,'2022-blake3-aes-256-gcm','','',14,1,0,'127.0.0.1',0),
		(62,'高级',1,?,'2022-blake3-aes-256-gcm','','',14,1,3,'127.0.0.1',0)`,
		"n.example.com;8443;"+fixtureKey, "h.example.com;8443;"+fixtureKey)
	post := func(node int, body string) (int, map[string]any) {
		t.Helper()
		status, _, resp := e.response("POST", fmt.Sprintf("/mod_mu/v2/nodes/%d/alive_ip?key=alive-key", node), body)
		var out map[string]any
		_ = json.Unmarshal([]byte(resp), &out)
		return status, out
	}
	for _, c := range []struct {
		name, body string
		want       int
	}{
		{"IP 无效", fmt.Sprintf(`{"data":[{"user_id":%d,"ip":"1.2.3"}]}`, uid), 400},
		{"用户 ID 为 0", `{"data":[{"user_id":0,"ip":"1.2.3.4"}]}`, 400},
		{"缺少 data", `{}`, 400},
		{"多余字段", fmt.Sprintf(`{"data":[{"user_id":%d,"ip":"1.2.3.4","x":1}]}`, uid), 400},
		{"空数组（没有在线用户）", `{"data":[]}`, 200},
	} {
		if status, _ := post(61, c.body); status != c.want {
			t.Fatalf("%s: 期望 %d，实际 %d", c.name, c.want, status)
		}
	}
	if status, _, _ := e.response("POST", "/mod_mu/v2/nodes/61/alive_ip?key=wrong", `{"data":[]}`); status != 403 {
		t.Fatalf("密钥错误应 403: %d", status)
	}

	// 用户限制 5 个：先上报 5 个，再上报 2 个新 IP；重复条目与 IPv4 映射地址只算一次。
	e.exec("UPDATE user SET node_connector = 5 WHERE id = ?", uid)
	var first []string
	for i := 1; i <= 5; i++ {
		first = append(first, fmt.Sprintf(`{"user_id":%d,"ip":"198.51.100.%d"}`, uid, i))
	}
	first = append(first, fmt.Sprintf(`{"user_id":%d,"ip":"::ffff:198.51.100.1"}`, uid))
	first = append(first, fmt.Sprintf(`{"user_id":%d,"ip":"203.0.113.1"}`, high)) // 节点 61 等级 0，高等级用户也可用
	if status, out := post(61, `{"data":[`+strings.Join(first, ",")+`]}`); status != 200 || out["accepted"] != float64(6) || out["ignored"] != float64(0) {
		t.Fatalf("上报失败: %d %v", status, out)
	}
	e.exec("UPDATE alive_ip SET datetime = datetime - 120") // 前 5 个 IP 已在线 2 分钟
	if status, out := post(61, fmt.Sprintf(`{"data":[{"user_id":%d,"ip":"198.51.100.1"},{"user_id":%d,"ip":"198.51.100.2"},{"user_id":%d,"ip":"198.51.100.3"},{"user_id":%d,"ip":"198.51.100.4"},{"user_id":%d,"ip":"198.51.100.5"},{"user_id":%d,"ip":"2001:db8::1"},{"user_id":%d,"ip":"1.1.1.1"}]}`,
		uid, uid, uid, uid, uid, uid, uid)); status != 200 || out["accepted"] != float64(7) {
		t.Fatalf("上报失败: %d %v", status, out)
	}
	// 节点 62 只允许等级 3 以上：普通用户的条目被忽略，不写入。
	if status, out := post(62, fmt.Sprintf(`{"data":[{"user_id":%d,"ip":"192.0.2.9"}]}`, uid)); status != 200 || out["ignored"] != float64(1) || out["accepted"] != float64(0) {
		t.Fatalf("不属于该节点的用户应忽略: %d %v", status, out)
	}
	e.mustJob("checkjob")

	status, _, body := e.response("GET", "/mod_mu/v2/nodes/61/users?key=alive-key", "")
	if status != 200 {
		t.Fatalf("users: %d %s", status, body)
	}
	var resp struct {
		Data []struct {
			ID         uint32   `json:"id"`
			IPLimit    int      `json:"ip_limit"`
			BlockedIPs []string `json:"blocked_ips"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, u := range resp.Data {
		switch u.ID {
		case uid:
			found++
			if u.IPLimit != 5 || strings.Join(u.BlockedIPs, ",") != "1.1.1.1,2001:db8::1" {
				t.Fatalf("应限制后加入的 2 个 IP: limit=%d blocked=%v", u.IPLimit, u.BlockedIPs)
			}
		case high:
			found++
			if u.BlockedIPs == nil || len(u.BlockedIPs) != 0 {
				t.Fatalf("未超限的用户 blocked_ips 应为空数组: %v", u.BlockedIPs)
			}
		}
	}
	if found != 2 {
		t.Fatalf("用户列表缺少用户: %s", body)
	}
	var cnt int
	_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM alive_ip WHERE ip = '192.0.2.9'").Scan(&cnt)
	if cnt != 0 {
		t.Fatal("被忽略的条目不应写入")
	}

	// 停用的节点、流量超限的节点（users 返回空列表）：上报全部忽略，不写入（验收 R4）。
	for _, c := range []struct{ name, set, reset string }{
		{"停用", "UPDATE ss_node SET type = 0 WHERE id = 61", "UPDATE ss_node SET type = 1 WHERE id = 61"},
		{"流量超限", "UPDATE ss_node SET node_bandwidth_limit = 10, node_bandwidth = 20 WHERE id = 61", "UPDATE ss_node SET node_bandwidth_limit = 0, node_bandwidth = 0 WHERE id = 61"},
	} {
		e.exec(c.set)
		if _, _, body := e.response("GET", "/mod_mu/v2/nodes/61/users?key=alive-key", ""); !strings.Contains(body, `"data":[]`) {
			t.Fatalf("%s: users 应为空: %s", c.name, body)
		}
		if status, out := post(61, fmt.Sprintf(`{"data":[{"user_id":%d,"ip":"192.0.2.77"}]}`, uid)); status != 200 || out["accepted"] != float64(0) || out["ignored"] != float64(1) {
			t.Fatalf("%s: 上报应忽略: %d %v", c.name, status, out)
		}
		_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM alive_ip WHERE ip = '192.0.2.77'").Scan(&cnt)
		if cnt != 0 {
			t.Fatalf("%s: 不应写入在线 IP", c.name)
		}
		e.exec(c.reset)
	}
}
