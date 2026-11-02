package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/config"
	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// badDisconnectItems 是旧库中出现过的各种非法写法，外加端口、网段、zone 等。
var badDisconnectItems = []string{
	"256.12.1.9", "203.0.113.300", // 数字段超过 255
	"203.0.113", "10.1", // 不完整的 IPv4
	"198.51.100.1.5",            // 超过四段
	"198.51.100.2:51820", "a:b", // 混入冒号 / 端口
	"198.51.100.3;198.51.100.4",  // 混入分号
	"010.0.0.1", "198.51.100.07", // 前导零
	"198.51.100.0/24", "fe80::1%eth0", // 网段、zone
	"[2001:db8::9]:443", "not-an-ip",
}

func captureLog(e *testEnv) *bytes.Buffer {
	var buf bytes.Buffer
	e.app.Log = slog.New(slog.NewTextHandler(&buf, nil))
	return &buf
}

// TestNodeAPIDisconnectIPSanitize 历史数据中 disconnect_ip 的非法项：v2 与旧接口只输出合法项（标准写法、去重），
// 用户数量、凭据与其他字段不变，与只保留合法项时的响应完全相同；告警不含原文，同一节点限频。
func TestNodeAPIDisconnectIPSanitize(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "dip-key" })
	mixed := e.login("mixed@example.com")
	onlyBad := e.login2("onlybad@example.com")
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(61,'n',1,?,'2022-blake3-aes-256-gcm','','',14,1,0,'127.0.0.1',0)`, "n.example.com;8443;"+fixtureKey)
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(1,'旧',1,'old.example.com','aes-256-gcm','','',0,1,0,'127.0.0.1',0)`)
	valid := []string{"198.51.100.20", "2001:DB8::20", " 203.0.113.21 ", "::ffff:198.51.100.22", "198.51.100.20"}
	e.exec("UPDATE user SET disconnect_ip = ? WHERE id = ?", strings.Join(append(append([]string{}, valid...), badDisconnectItems...), ","), mixed)
	e.exec("UPDATE user SET disconnect_ip = ? WHERE id = ?", strings.Join(badDisconnectItems[:4], ","), onlyBad)
	logs := captureLog(e)

	get := func(path string) string {
		t.Helper()
		status, _, body := e.response("GET", path, "")
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, body)
		}
		return body
	}
	v2 := get("/mod_mu/v2/nodes/61/users?key=dip-key")
	legacyBody := get("/mod_mu/users?node_id=1&key=dip-key")

	var resp struct {
		Data []struct {
			ID         uint32   `json:"id"`
			BlockedIPs []string `json:"blocked_ips"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(v2), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("用户数量应不变: %d", len(resp.Data))
	}
	for _, u := range resp.Data {
		want := "198.51.100.20,2001:db8::20,203.0.113.21,198.51.100.22"
		if u.ID == onlyBad {
			want = ""
		}
		if got := strings.Join(u.BlockedIPs, ","); got != want || u.BlockedIPs == nil {
			t.Errorf("用户 %d 的 blocked_ips: %q，应为 %q", u.ID, got, want)
		}
	}
	if !strings.Contains(legacyBody, `"disconnect_ip":"198.51.100.20,2001:db8::20,203.0.113.21,198.51.100.22"`) || !strings.Contains(legacyBody, `"disconnect_ip":""`) {
		t.Errorf("旧接口的 disconnect_ip 应只含合法项:\n%s", legacyBody)
	}

	// 与只保留合法项时的响应完全相同（用户数量、凭据、有效封禁与其他字段都不变）。
	e.exec("UPDATE user SET disconnect_ip = '198.51.100.20,2001:db8::20,203.0.113.21,198.51.100.22' WHERE id = ?", mixed)
	e.exec("UPDATE user SET disconnect_ip = '' WHERE id = ?", onlyBad)
	if clean := get("/mod_mu/v2/nodes/61/users?key=dip-key"); clean != v2 {
		t.Errorf("v2 响应应与只保留合法项时相同:\n%s\n%s", v2, clean)
	}
	if clean := get("/mod_mu/users?node_id=1&key=dip-key"); clean != legacyBody {
		t.Errorf("旧接口响应应与只保留合法项时相同:\n%s\n%s", legacyBody, clean)
	}

	// 告警：每个节点一次（10 分钟内不重复），带用户 ID、项数、分类，不含任何原文。
	text := logs.String()
	if n := strings.Count(text, "节点接口已跳过"); n != 2 {
		t.Fatalf("两个节点各告警一次，实际 %d 次:\n%s", n, text)
	}
	if !strings.Contains(text, "node=61 api=v2/users users=2") || !strings.Contains(text, "items=18") || !strings.Contains(text, "数字段超过 255×4") {
		t.Errorf("告警内容不完整:\n%s", text)
	}
	for _, s := range badDisconnectItems {
		if strings.Contains(text, s) {
			t.Errorf("告警中不应出现原文 %q:\n%s", s, text)
		}
	}
	e.exec("UPDATE user SET disconnect_ip = '1.2.3' WHERE id = ?", onlyBad)
	get("/mod_mu/v2/nodes/61/users?key=dip-key")
	if n := strings.Count(logs.String(), "节点接口已跳过"); n != 2 {
		t.Errorf("10 分钟内同一节点不应重复告警: %d", n)
	}
	if got := e.app.blockedIPWarn.skipped.Load(); got != 18*2+1 {
		t.Errorf("累计跳过项数: %d", got)
	}
}

// TestDisconnectIPWritePaths 超限新增、到期解除与历史残留：新增与解除都不删除非法项与没有对应记录的项；
// 解除按标准写法比较；检查命令列出影响清单（不含原文）；修复命令默认只预览，--apply 只改 disconnect_ip，
// 输出的回滚语句可以恢复原值；--orphans 时去掉没有对应记录的项。
func TestDisconnectIPWritePaths(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "dip-key" })
	ctx := context.Background()
	uid := e.login("dip@example.com")
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(61,'n',1,?,'2022-blake3-aes-256-gcm','','',14,1,0,'127.0.0.1',0)`, "n.example.com;8443;"+fixtureKey)
	// 历史数据：两个非法项、一个没有对应记录的合法项、一个有记录但写法不同（大写）的合法项。
	legacy := "256.1.1.1,198.51.100.5:80, 2001:DB8::5 ,2001:DB8::7"
	e.exec("UPDATE user SET node_connector = 1, disconnect_ip = ? WHERE id = ?", legacy, uid)
	e.exec("INSERT INTO disconnect_ip (userid,ip,datetime) VALUES (?, '2001:db8::7', UNIX_TIMESTAMP())", uid)
	before, _ := e.app.Q.GetUser(ctx, uid)

	// 超限新增：2.2.2.2 后加入被限制；历史项原样保留。
	e.exec("INSERT INTO alive_ip (nodeid,userid,ip,datetime) VALUES (61,?,'1.1.1.1',UNIX_TIMESTAMP()-30),(61,?,'2.2.2.2',UNIX_TIMESTAMP())", uid, uid)
	e.mustJob("checkjob")
	e.mustJob("checkjob")
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.DisconnectIp != legacy+",2.2.2.2" {
		t.Fatalf("新增限制应追加且保留历史项: %q", u.DisconnectIp)
	}
	_, _, body := e.response("GET", "/mod_mu/v2/nodes/61/users?key=dip-key", "")
	if !strings.Contains(body, `"blocked_ips":["2001:db8::5","2001:db8::7","2.2.2.2"]`) {
		t.Fatalf("接口应下发全部合法项: %s", body)
	}

	// 到期解除：2.2.2.2 与写法不同的 2001:DB8::7 都按记录解除；非法项与没有记录的 2001:DB8::5 保留。
	e.exec("UPDATE disconnect_ip SET datetime = UNIX_TIMESTAMP() - 400")
	e.exec("DELETE FROM alive_ip")
	e.mustJob("checkjob")
	u, _ = e.app.Q.GetUser(ctx, uid)
	if u.DisconnectIp != "256.1.1.1,198.51.100.5:80, 2001:DB8::5 " {
		t.Fatalf("到期解除后的字段: %q", u.DisconnectIp)
	}

	// 检查命令：影响清单，不含原文。
	var out strings.Builder
	if err := e.app.XCat(ctx, []string{"checkDisconnectIP"}, &out); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "用户 "+itoa(uid)+"：合法 1 项（其中没有对应记录 1 项）、非法 2 项") || strings.Contains(s, "256.1.1.1") || strings.Contains(s, "2001") {
		t.Fatalf("检查结果:\n%s", s)
	}

	// 修复：默认只预览，不改动。
	out.Reset()
	if err := e.app.XCat(ctx, []string{"repairDisconnectIP"}, &out); err != nil {
		t.Fatal(err)
	}
	if u, _ := e.app.Q.GetUser(ctx, uid); u.DisconnectIp != "256.1.1.1,198.51.100.5:80, 2001:DB8::5 " || !strings.Contains(out.String(), "以上为预览") {
		t.Fatalf("预览不应修改: %q\n%s", u.DisconnectIp, out.String())
	}
	// --apply：只去掉非法项，没有记录的合法项保留；其他字段（凭据等）不变；输出是可执行的回滚 SQL。
	out.Reset()
	rbFile := filepath.Join(t.TempDir(), "rollback.sql")
	if err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--apply", "--rollback-file", rbFile}, &out); err != nil {
		t.Fatal(err)
	}
	after, _ := e.app.Q.GetUser(ctx, uid)
	if after.DisconnectIp != "2001:db8::5" {
		t.Fatalf("修复后应只保留合法项: %q", after.DisconnectIp)
	}
	after.DisconnectIp, before.DisconnectIp = "", ""
	before.T, after.T = 0, 0
	if after != before {
		t.Errorf("修复不应改变其他字段:\n%+v\n%+v", before, after)
	}
	rbText, _ := os.ReadFile(rbFile)
	rollback := runRollback(t, e, string(rbText))
	if u, _ := e.app.Q.GetUser(ctx, uid); u.DisconnectIp != "256.1.1.1,198.51.100.5:80, 2001:DB8::5 " || rollback != 1 {
		t.Fatalf("回滚后应恢复原值: %q（%d 条）", u.DisconnectIp, rollback)
	}
	// --orphans --apply：没有对应记录的合法项也去掉。
	out.Reset()
	if err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--orphans", "--apply", "--rollback-file", filepath.Join(t.TempDir(), "rb2.sql")}, &out); err != nil {
		t.Fatal(err)
	}
	if u, _ := e.app.Q.GetUser(ctx, uid); u.DisconnectIp != "" {
		t.Fatalf("--orphans 后应为空: %q", u.DisconnectIp)
	}
	// 条件更新：字段在检查后被改动时不覆盖。
	if n, err := e.app.Q.ReplaceUserDisconnectIP(ctx, store.ReplaceUserDisconnectIPParams{NewValue: "x", ID: uid, OldValue: "不是当前值"}); err != nil || n != 0 {
		t.Fatalf("字段已改动时不应更新: %d %v", n, err)
	}
	// 再次检查：没有需要处理的用户。
	out.Reset()
	if err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--apply", "--rollback-file", filepath.Join(t.TempDir(), "rb3.sql")}, &out); err != nil || !strings.Contains(out.String(), "需要修改的用户：0 个") {
		t.Fatalf("修复后不应再有需要修改的用户: %v\n%s", err, out.String())
	}
}

// runRollback 逐条执行修复命令输出中的回滚语句（其余行都是注释），返回语句数。
func runRollback(t *testing.T, e *testEnv, sqlText string) int {
	t.Helper()
	n := 0
	for _, line := range strings.Split(sqlText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-- ") {
			continue
		}
		if !strings.HasPrefix(line, "UPDATE `user` SET disconnect_ip = X'") || !strings.Contains(line, "AND CAST(disconnect_ip AS BINARY) = X'") || !strings.Contains(line, "AND EXISTS (SELECT 1 FROM disconnect_ip_repair WHERE repair_id = '") {
			t.Fatalf("修复输出中有不是注释也不是回滚语句的行: %q", line)
		}
		e.exec(line)
		n++
	}
	return n
}

func itoa(id uint32) string {
	b, _ := json.Marshal(id)
	return string(b)
}
