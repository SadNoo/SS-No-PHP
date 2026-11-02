package app

import (
	"context"
	"fmt"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

func (e *testEnv) mailsTo(email, subjectPart string) int {
	e.mail.mu.Lock()
	defer e.mail.mu.Unlock()
	n := 0
	for _, m := range e.mail.sent {
		if m["to"] == email && strings.Contains(m["subject"].(string), subjectPart) {
			n++
		}
	}
	return n
}

func (e *testEnv) mustJob(name string) {
	e.t.Helper()
	if err := e.app.RunJob(context.Background(), name); err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
}

// TestCheckJobUsers 等级到期、账户过期只提醒一次、余额不足提醒。
func TestCheckJobUsers(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Users.NotifyLimitMode = "mb"; c.Users.NotifyLimitValue = 100 })
	ctx := context.Background()
	uid := e.login("j1@example.com")
	e.exec("UPDATE user SET class = 2, class_expire = UTC_TIMESTAMP() - INTERVAL 1 MINUTE, transfer_enable = 10737418240, u = 5 WHERE id = ?", uid)
	e.mustJob("checkjob")
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.Class != 0 || u.TransferEnable != 0 || u.U != 0 {
		t.Fatalf("等级过期后应降为 0 级并重置流量: class=%d te=%d", u.Class, u.TransferEnable)
	}
	if e.mailsTo("j1@example.com", "账户等级已经过期") != 1 {
		t.Fatal("应发送等级过期邮件")
	}
	// 1989 等未设置的等级到期时间不处理。
	e.exec("UPDATE user SET class = 1, class_expire = '1989-06-04 00:05:00' WHERE id = ?", uid)
	e.mustJob("checkjob")
	if u, _ := e.app.Q.GetUser(ctx, uid); u.Class != 1 {
		t.Fatal("未设置到期时间的等级不应被降级")
	}
	// 剩余流量不足提醒只发一次。
	if e.mailsTo("j1@example.com", "剩余流量过低") != 2-1 {
		t.Fatalf("剩余流量提醒次数不正确: %d", e.mailsTo("j1@example.com", "剩余流量过低"))
	}
	// 账户过期：清空流量，只提醒一次。
	e.exec("UPDATE user SET expire_in = UTC_TIMESTAMP() - INTERVAL 1 MINUTE, transfer_enable = 1073741824 WHERE id = ?", uid)
	e.mustJob("checkjob")
	e.mustJob("checkjob")
	if e.mailsTo("j1@example.com", "账户已经过期") != 1 {
		t.Fatalf("账户过期邮件应只发一次: %d", e.mailsTo("j1@example.com", "账户已经过期"))
	}
	if u, _ := e.app.Q.GetUser(ctx, uid); u.TransferEnable != 0 {
		t.Fatal("账户过期后流量应清零")
	}
	// 续期后清除标记；之后再次过期会重新提醒（标记在每轮开始时一次读出）。
	e.exec("UPDATE user SET expire_in = UTC_TIMESTAMP() + INTERVAL 1 DAY WHERE id = ?", uid)
	e.mustJob("checkjob")
	if e.app.flag(ctx, fmt.Sprintf("expire:user:%d", uid)) {
		t.Fatal("续期后应清除过期标记")
	}
	e.exec("UPDATE user SET expire_in = UTC_TIMESTAMP() - INTERVAL 1 MINUTE WHERE id = ?", uid)
	e.mustJob("checkjob")
	e.mustJob("checkjob")
	if e.mailsTo("j1@example.com", "账户已经过期") != 2 {
		t.Fatalf("再次过期应再提醒一次: %d", e.mailsTo("j1@example.com", "账户已经过期"))
	}
}

// TestCheckJobRenew 自动续费：余额足够时续费，余额不足只提醒一次。
func TestCheckJobRenew(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Users.EnableAutoRenew = true })
	ctx := context.Background()
	uid := e.login("j2@example.com")
	e.exec(`INSERT INTO shop (id,name,price,content,auto_renew,auto_reset_bandwidth,status) VALUES (1,'月付',10.00,'{"bandwidth":"100","class":"2","class_expire":"30"}',30,1,1)`)
	e.exec(`INSERT INTO bought (userid,shopid,datetime,renew,coupon,price) VALUES (?,1,UNIX_TIMESTAMP()-100,UNIX_TIMESTAMP()-10,'',10)`, uid)
	e.exec("UPDATE user SET money = 5 WHERE id = ?", uid)
	e.mustJob("checkjob")
	e.mustJob("checkjob")
	if e.mailsTo("j2@example.com", "续费失败") != 1 {
		t.Fatalf("余额不足提醒应只发一次: %d", e.mailsTo("j2@example.com", "续费失败"))
	}
	e.exec("UPDATE user SET money = 15 WHERE id = ?", uid)
	e.mustJob("checkjob")
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.Money.String() != "5.00" || u.Class != 2 || u.TransferEnable != 100<<30 {
		t.Fatalf("续费未生效: money=%s class=%d te=%d", u.Money, u.Class, u.TransferEnable)
	}
	var n int
	_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM bought WHERE userid = ? AND renew > UNIX_TIMESTAMP()", uid).Scan(&n)
	if n != 1 || e.mailsTo("j2@example.com", "续费成功") != 1 {
		t.Fatalf("应生成新的续费订单并通知: %d", n)
	}
}

// TestDailyJob 每日结算、按日重置流量、清理旧日志。
func TestDailyJob(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.login("j3@example.com")
	day := time.Now().In(e.app.Loc).Day()
	e.exec("UPDATE user SET u = 100, d = 200, last_day_t = 0 WHERE id = ?", uid)
	other := e.login2("j4@example.com")
	e.exec("UPDATE user SET u = 100, d = 200, auto_reset_day = ?, auto_reset_bandwidth = 5 WHERE id = ?", day, other)
	e.exec("INSERT INTO user_traffic_log (user_id,u,d,node_id,rate,traffic,log_time) VALUES (?,1,1,1,1,'',UNIX_TIMESTAMP() - 86400*4)", uid)
	e.mustJob("dailyjob")
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.LastDayT != 300 {
		t.Fatalf("今日之前已用流量应为 300: %d", u.LastDayT)
	}
	o, _ := e.app.Q.GetUser(ctx, other)
	if o.U != 0 || o.LastDayT != 0 || o.TransferEnable != 5<<30 {
		t.Fatalf("按日重置流量未生效: %+v", o)
	}
	var n int
	_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_traffic_log").Scan(&n)
	if n != 0 {
		t.Fatal("3 天前的流量记录应被清理")
	}
}

// TestCheckJobConnector 在线设备数超限的 IP 暂时限制且不重复记录（K42），5 分钟后解除。
func TestCheckJobConnector(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.login("j5@example.com")
	e.exec("UPDATE user SET node_connector = 1 WHERE id = ?", uid)
	e.exec("INSERT INTO alive_ip (nodeid,userid,ip,datetime) VALUES (1,?,'1.1.1.1',UNIX_TIMESTAMP()),(1,?,'2.2.2.2',UNIX_TIMESTAMP())", uid, uid)
	e.mustJob("checkjob")
	e.mustJob("checkjob")
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.DisconnectIp != "2.2.2.2" {
		t.Fatalf("超出的 IP 应被限制且不重复: %q", u.DisconnectIp)
	}
	e.exec("UPDATE disconnect_ip SET datetime = UNIX_TIMESTAMP() - 400")
	e.exec("DELETE FROM alive_ip")
	e.mustJob("checkjob")
	if u, _ := e.app.Q.GetUser(ctx, uid); u.DisconnectIp != "" {
		t.Fatalf("5 分钟后应解除限制: %q", u.DisconnectIp)
	}
}

// TestCheckJobConnectorOrder 超出同时在线 IP 数时保留最早在线的 IP，限制后加入的 IP（与 IP 的字符串顺序无关，K51）；
// 已离线（超过 60 秒未上报）的 IP 不计入；新注册用户默认 5 个。
func TestCheckJobConnectorOrder(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.login("order@example.com")
	if u, _ := e.app.Q.GetUser(ctx, uid); u.NodeConnector != 5 {
		t.Fatalf("新注册用户默认同时在线 IP 数应为 5: %d", u.NodeConnector)
	}
	e.exec("UPDATE user SET node_connector = 2 WHERE id = ?", uid)
	// 9.9.9.9 与 8.8.8.8 已在线 4 分钟；1.1.1.1、2.2.2.2 刚加入；7.7.7.7 已离线 2 分钟。
	e.exec(`INSERT INTO alive_ip (nodeid,userid,ip,datetime) VALUES
		(1,?,'9.9.9.9',UNIX_TIMESTAMP()-240),(1,?,'8.8.8.8',UNIX_TIMESTAMP()-230),(1,?,'7.7.7.7',UNIX_TIMESTAMP()-220),
		(1,?,'9.9.9.9',UNIX_TIMESTAMP()-10),(1,?,'8.8.8.8',UNIX_TIMESTAMP()-10),(1,?,'7.7.7.7',UNIX_TIMESTAMP()-120),
		(1,?,'1.1.1.1',UNIX_TIMESTAMP()-5),(1,?,'2.2.2.2',UNIX_TIMESTAMP()-5)`, uid, uid, uid, uid, uid, uid, uid, uid)
	e.mustJob("checkjob")
	if u, _ := e.app.Q.GetUser(ctx, uid); u.DisconnectIp != "1.1.1.1,2.2.2.2" {
		t.Fatalf("应限制后加入的 IP，保留最早在线的: %q", u.DisconnectIp)
	}
	// 0 表示不限。
	other := e.login2("unlimited@example.com")
	e.exec("UPDATE user SET node_connector = 0 WHERE id = ?", other)
	for i := range 8 {
		e.exec("INSERT INTO alive_ip (nodeid,userid,ip,datetime) VALUES (1,?,?,UNIX_TIMESTAMP())", other, fmt.Sprintf("10.0.0.%d", i))
	}
	e.mustJob("checkjob")
	if u, _ := e.app.Q.GetUser(ctx, other); u.DisconnectIp != "" {
		t.Fatalf("0 表示不限: %q", u.DisconnectIp)
	}
}

// ipSim 按分钟模拟节点上报与每分钟检查（解除到期限制 → 检查 → 清理），时间由测试指定。
type ipSim struct {
	e   *testEnv
	uid uint32
	t0  int64
}

func (s ipSim) report(minute int, ips ...string) {
	for _, ip := range ips {
		s.e.exec("INSERT INTO alive_ip (nodeid,userid,ip,datetime) VALUES (1,?,?,?)", s.uid, ip, s.t0+int64(minute)*60)
	}
}

func (s ipSim) check(minute int) string {
	s.e.t.Helper()
	ctx := context.Background()
	now := time.Unix(s.t0+int64(minute)*60+1, 0)
	for _, fn := range []func(context.Context, time.Time) error{s.e.app.releaseDisconnects, s.e.app.checkConnector, s.e.app.cleanShortLived} {
		if err := fn(ctx, now); err != nil {
			s.e.t.Fatal(err)
		}
	}
	u, _ := s.e.app.Q.GetUser(ctx, s.uid)
	return u.DisconnectIp
}

// TestCheckJobConnectorLongLived 两个 IP 都持续在线超过 1 小时（上报相位不同），后加入的始终被限制，
// 即使它被限制期间节点仍在上报、每 5 分钟解除一次（验收 R1）。
func TestCheckJobConnectorLongLived(t *testing.T) {
	e := newEnv(t)
	uid := e.login("long@example.com")
	e.exec("UPDATE user SET node_connector = 1 WHERE id = ?", uid)
	s := ipSim{e, uid, time.Now().Unix() - 3*3600}
	for m := 0; m <= 150; m++ {
		s.report(m, "192.0.2.9") // A：从第 0 分钟起每分钟上报
		if m >= 60 {
			s.report(m, "192.0.2.10") // B：第 60 分钟加入，此后一直上报（被限制时也上报）
		}
		got := s.check(m)
		if strings.Contains(got, "192.0.2.9") {
			t.Fatalf("第 %d 分钟：先上线的 A 不应被限制: %q", m, got)
		}
		if m >= 60 && got != "192.0.2.10" {
			t.Fatalf("第 %d 分钟：后加入的 B 应一直被限制（解除后立即重新限制）: %q", m, got)
		}
	}
	// A 离线超过 10 分钟后，B 成为最早在线的；A 回来时视为新加入。
	for m := 151; m <= 175; m++ {
		s.report(m, "192.0.2.10")
		if m >= 170 {
			s.report(m, "192.0.2.9")
		}
		got := s.check(m)
		if m >= 170 && got != "192.0.2.9" {
			t.Fatalf("第 %d 分钟：A 离线超过 10 分钟后再出现应视为后加入: %q", m, got)
		}
	}
}

// TestCheckJobConnectorNormalize 节点自己的 IP（逗号分隔、带 #源节点ID）不计入；同一 IPv6 地址的不同写法、
// IPv4 映射地址只算一个（验收 R2、R3）。
func TestCheckJobConnectorNormalize(t *testing.T) {
	e := newEnv(t)
	uid := e.login("norm@example.com")
	e.exec("UPDATE user SET node_connector = 1 WHERE id = ?", uid)
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(81,'多 IP',1,'a.example.com','aes-256-gcm','','',0,1,0,'198.51.100.61,203.0.113.61',0),
		(82,'带专用项',1,'b.example.com','aes-256-gcm','','',0,1,0,'198.51.100.62#7, 2001:DB8:0:0::62',0)`)
	s := ipSim{e, uid, time.Now().Unix() - 3600}
	for m := 0; m < 5; m++ {
		// 节点之间的中转连接（节点自己的 IP）先出现，真实客户端后出现。
		s.report(m, "198.51.100.61", "203.0.113.61", "198.51.100.62", "2001:db8::62")
		s.report(m, "192.0.2.9")
		if got := s.check(m); got != "" {
			t.Fatalf("节点 IP 不应计入，唯一的客户端不应被限制: %q", got)
		}
	}
	// 同一 IPv6 地址：旧接口原样保存的展开写法与 v2 的压缩写法；IPv4 映射地址与 IPv4。
	e2 := newEnv(t)
	uid2 := e2.login("v6@example.com")
	e2.exec("UPDATE user SET node_connector = 1 WHERE id = ?", uid2)
	s2 := ipSim{e2, uid2, time.Now().Unix() - 3600}
	for m := 0; m < 5; m++ {
		s2.report(m, "2001:0DB8:0000:0000:0000:0000:0000:0001", "2001:db8::1", "2001:db8:0:0::1")
		if got := s2.check(m); got != "" {
			t.Fatalf("同一 IPv6 地址的不同写法应只算一个: %q", got)
		}
	}
	s2.report(5, "::FFFF:192.0.2.1", "192.0.2.1", "2001:db8::1")
	if got := s2.check(5); got != "192.0.2.1" {
		t.Fatalf("IPv4 映射地址与 IPv4 为同一个 IP，且后加入: %q", got)
	}
}

// TestCheckJobConnectorMalformed 无效的在线 IP 记录（旧接口上报的任意文本、数据库中已有的脏记录）被跳过，
// 不中断检查、不影响其他用户；有效的 IPv4、IPv6、IPv4 映射地址照常统计（复验 N1）。
func TestCheckJobConnectorMalformed(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "legacy-key" })
	ctx := context.Background()
	a := e.login("limited@example.com")
	b := e.login2("free@example.com")
	e.exec("UPDATE user SET node_connector = 1 WHERE id = ?", a)
	e.exec("UPDATE user SET node_connector = 0 WHERE id = ?", b)
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(61,'旧节点',1,'old.example.com','aes-256-gcm','','',0,1,0,'127.0.0.1',0)`)

	// 旧接口：无效条目跳过，响应与原版相同，有效条目照常写入。
	status, _, body := e.response("POST", "/mod_mu/users/aliveip?node_id=61&key=legacy-key", fmt.Sprintf(
		`{"data":[{"user_id":%d,"ip":"无效地址"},{"user_id":%d,"ip":"not-an-ip"},{"user_id":%d,"ip":" "},{"user_id":%d,"ip":"fe80::1%%eth0"},{"user_id":%d,"ip":"192.0.2.77"}]}`,
		b, b, b, b, b))
	if status != 200 || !strings.Contains(body, `"ret":1`) {
		t.Fatalf("旧接口应保持原响应: %d %s", status, body)
	}
	var rows int
	_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM alive_ip WHERE userid = ?", b).Scan(&rows)
	if rows != 1 {
		t.Fatalf("只应写入有效的 IP: %d 条", rows)
	}

	// 数据库中已有的脏记录（旧版本写入）：非 ASCII、任意文本、空、只有空白、超长。
	s := ipSim{e, a, time.Now().Unix() - 600}
	for m := 0; m < 3; m++ {
		for _, bad := range []string{"无效地址", "not-an-ip", "", "   ", "1.2.3", strings.Repeat("a", 200), "2001:db8::zz"} {
			e.exec("INSERT INTO alive_ip (nodeid,userid,ip,datetime) VALUES (1,?,?,?)", b, bad, s.t0+int64(m)*60)
		}
		s.report(m, "192.0.2.9") // A 先上线
		if m >= 1 {
			s.report(m, "192.0.2.10") // 后加入
		}
		e.exec("INSERT INTO alive_ip (nodeid,userid,ip,datetime) VALUES (1,?,?,?),(1,?,?,?),(1,?,?,?)",
			b, " 198.51.100.5 ", s.t0+int64(m)*60, b, "2001:DB8::5", s.t0+int64(m)*60, b, "::ffff:198.51.100.6", s.t0+int64(m)*60)
		got := s.check(m)
		if m >= 1 && got != "192.0.2.10" {
			t.Fatalf("第 %d 分钟：其他用户的无效记录不应影响限制: %q", m, got)
		}
	}
	// 有效的 IPv4（带空白）、IPv6、IPv4 映射地址正常统计；无效记录不进入在线状态。
	var joined string
	_ = e.app.DB.QueryRowContext(ctx, "SELECT GROUP_CONCAT(ip ORDER BY ip) FROM ip_online WHERE userid = ?", b).Scan(&joined)
	if got := joined; got != "192.0.2.77,198.51.100.5,198.51.100.6,2001:db8::5" { // 192.0.2.77 来自上面旧接口的有效上报
		t.Fatalf("在线状态应只含有效 IP（标准写法）: %q", got)
	}
}

func TestNormalizeIP(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.1": "192.0.2.1", " 192.0.2.1 ": "192.0.2.1", "::ffff:192.0.2.1": "192.0.2.1", "::FFFF:192.0.2.1": "192.0.2.1",
		"2001:0DB8:0000:0000:0000:0000:0000:0001": "2001:db8::1", "2001:db8::1": "2001:db8::1",
		"无效地址": "", "not-an-ip": "", "": "", "   ": "", "1.2.3": "", "fe80::1%eth0": "", "192.0.2.1/24": "",
	} {
		got, ok := normalizeIP(in)
		if got != want || ok != (want != "") {
			t.Errorf("normalizeIP(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// TestDetectOffline 节点掉线与恢复各通知一次。
func TestDetectOffline(t *testing.T) {
	e := newEnv(t)
	uid := e.login("adm2@example.com")
	e.exec("UPDATE user SET is_admin = 1 WHERE id = ?", uid)
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_speedlimit,node_heartbeat,node_ip,node_group,custom_rss,mu_only,custom_method)
		VALUES (1,'香港',1,'hk','m','','',0,1,0,0,UNIX_TIMESTAMP()-1000,'1.1.1.1',0,0,0,0)`)
	e.mustJob("checkjob")
	e.mustJob("checkjob")
	if e.mailsTo("adm2@example.com", "系统警告") != 1 {
		t.Fatalf("掉线通知应只发一次: %d", e.mailsTo("adm2@example.com", "系统警告"))
	}
	e.exec("UPDATE ss_node SET node_heartbeat = UNIX_TIMESTAMP() WHERE id = 1")
	e.mustJob("checkjob")
	if e.mailsTo("adm2@example.com", "系统提示") != 1 {
		t.Fatal("恢复上线应通知")
	}
}

// TestFinanceAndSchedule 财务日报统计昨天的充值；调度器首次运行不执行每日任务。
func TestFinanceAndSchedule(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.login("adm3@example.com")
	e.exec("UPDATE user SET is_admin = 1 WHERE id = ?", uid)
	y := time.Now().In(e.app.Loc).AddDate(0, 0, -1)
	used := time.Date(y.Year(), y.Month(), y.Day(), 12, 0, 0, 0, e.app.Loc).UTC()
	e.exec("INSERT INTO code (code,type,number,isused,userid,usedatetime) VALUES ('a',-1,12.5,1,?,?),('b',-1,99,1,?,UTC_TIMESTAMP())", uid, used, uid)
	e.mustJob("sendFinanceMail_day")
	e.mail.mu.Lock()
	var text string
	for _, m := range e.mail.sent {
		if strings.Contains(m["subject"].(string), "财务日报") {
			text = string(m["data"].(map[string]any)["text"].(template.HTML))
		}
	}
	e.mail.mu.Unlock()
	if !strings.Contains(text, "昨日总收入笔数：1") || !strings.Contains(text, "昨日总收入金额：12.5") {
		t.Fatalf("财务日报不正确: %s", text)
	}
	if e.app.onceADay(ctx, "test:daily", "2026-01-01", true) {
		t.Fatal("首次运行不应执行")
	}
	if !e.app.onceADay(ctx, "test:daily", "2026-01-02", true) || e.app.onceADay(ctx, "test:daily", "2026-01-02", true) {
		t.Fatal("每天应只执行一次")
	}
}
