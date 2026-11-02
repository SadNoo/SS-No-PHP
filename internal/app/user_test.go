package app

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/config"
	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/sub"
)

// login 注册并登录一个测试用户，返回用户 ID。
func (e *testEnv) login(email string) uint32 {
	e.t.Helper()
	if r := e.post("/auth/register", regForm(email)); r["ret"] != float64(1) {
		e.t.Fatalf("注册失败: %v", r)
	}
	if r := e.post("/auth/login", url.Values{"email": {email}, "passwd": {"Passw0rd!x"}}); r["ret"] != float64(1) {
		e.t.Fatalf("登录失败: %v", r)
	}
	u, err := e.app.Q.GetUserByEmail(context.Background(), strings.ToLower(email))
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

// page 访问页面，要求返回 200，返回页面内容。
func (e *testEnv) page(path string) string {
	e.t.Helper()
	resp, err := e.c.Get(e.ts.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("%s 返回 %d: %s", path, resp.StatusCode, b)
	}
	return string(b)
}

func (e *testEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.app.DB.ExecContext(context.Background(), q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

// seedNodes 写入几种典型节点：普通 SS、中转、V2Ray、单端口承载、VPN。
func (e *testEnv) seedNodes() {
	now := time.Now().Unix()
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_speedlimit,node_heartbeat,node_ip,node_group,custom_rss,mu_only,custom_method)
		VALUES (1,'香港 01',1,'hk.example.com','aes-256-cfb','说明<b>','可用',0,1,0,0,?, '1.1.1.1',0,1,0,0),
		       (2,'日本 中转',1,'jp.example.com','aes-256-cfb','','可用',10,1.5,0,1024,?, '2.2.2.2',0,1,0,0),
		       (3,'美国 V2',1,'us.example.com;443;32;ws;tls;path=/v2|host=us.example.com','', '','可用',11,1,0,100,0,'3.3.3.3',0,1,0,0),
		       (4,'单端口',1,'8443','', '','可用',9,1,0,0,0,'',0,1,0,0),
		       (5,'VPN 节点',1,'vpn.example.com','', '','可用',1,1,0,0,0,'',0,1,0,0),
		       (6,'高级 节点',1,'vip.example.com','aes-256-cfb','','可用',0,1,3,0,0,'6.6.6.6',0,1,0,0)`, now, now-1000)
	e.exec(`INSERT INTO user (user_name,email,pass,passwd,port,reg_date,im_value,pac,remark,forbidden_ip,forbidden_port,disconnect_ip,method,protocol,obfs,is_multi_user,class)
		VALUES ('mu','mu@example.com','x','mupass',8443,UTC_TIMESTAMP(),'','','','','','','chacha20-ietf','auth_aes128_md5','tls1.2_ticket_auth',1,10)`)
	e.exec(`INSERT INTO ss_node_info (node_id,uptime,`+"`load`"+`,log_time) VALUES (1,3600,'0.52 0.4 0.3',?),(1,3600,'0.1 0 0',?)`, now-100, now-1800*3)
	e.exec(`INSERT INTO ss_node_online_log (node_id,online_user,log_time) VALUES (1,7,?)`, now-100)
	e.exec(`INSERT INTO speedtest (nodeid,datetime,telecomping,telecomeupload,telecomedownload,unicomping,unicomupload,unicomdownload,cmccping,cmccupload,cmccdownload)
		VALUES (1,?,'30 ms','10','20','40','10','20','50','10','20')`, now-60)
	e.exec(`INSERT INTO announcement (date,content,markdown) VALUES (UTC_TIMESTAMP(),'<p>公告<b>内容</b></p>','公告')`)
}

// TestUserCenterPages 逐个访问第一批用户中心页面，确认模板能正常渲染。
func TestUserCenterPages(t *testing.T) {
	e := newEnv(t)
	e.seedNodes()
	uid := e.login("u1@example.com")
	e.exec(`INSERT INTO user_traffic_log (user_id,u,d,node_id,rate,traffic,log_time) VALUES (?,1024,2048,1,1,'3KB',?)`, uid, time.Now().Unix()-60)
	e.exec(`INSERT INTO alive_ip (nodeid,userid,ip,datetime) VALUES (1,?,'::ffff:9.9.9.9',?),(1,?,'1.1.1.1',?)`, uid, time.Now().Unix(), uid, time.Now().Unix())

	index := e.page("/user")
	// 新注册用户默认同时在线 IP 数为 5。
	if strings.Contains(index, "/user/url_reset") {
		t.Error("重置订阅链接应在资料编辑页面")
	}
	for _, want := range []string{"公告<b>内容</b>", "按协议订阅", `data-sub-copy="vmess"`, `data-copy-text="vmess://`, "<dd>1 / 5</dd>"} {
		if !strings.Contains(index, want) {
			t.Errorf("用户中心首页缺少 %q", want)
		}
	}
	node := e.page("/user/node")
	for _, want := range []string{"香港 01", "单端口 Shadowsocks - 8443 端口", "您当前等级不足以使用该节点", "vmess://", "说明&lt;b&gt;", "1Gbps"} {
		if !strings.Contains(node, want) {
			t.Errorf("节点列表缺少 %q", want)
		}
	}
	if strings.Contains(node, "VPN 节点") == false {
		t.Error("节点列表应包含 VPN 节点")
	}
	info := e.page("/user/node/1?ismu=0&relay_rule=0")
	if !strings.Contains(info, "ssr://") || !strings.Contains(info, "hk.example.com") {
		t.Error("节点详情缺少连接信息")
	}
	if !strings.Contains(e.page("/user/node/1?ismu=8443&relay_rule=0"), "8443") {
		t.Error("单端口节点详情缺少端口")
	}
	if !strings.Contains(e.page("/user/node/5"), "VPN 信息<br>地址：vpn.example.com") {
		t.Error("VPN 节点详情不正确")
	}
	ajax := e.page("/user/node/1/ajax")
	if !strings.Contains(ajax, `class="node-chart-data"`) || !strings.Contains(ajax, `"prefix":"香港 01"`) {
		t.Errorf("节点图表数据不正确: %s", ajax)
	}
	if !strings.Contains(e.page("/user/announcement"), "公告<b>内容</b>") {
		t.Error("公告页不正确")
	}
	if !strings.Contains(e.page("/user/trafficlog"), "jd: \"香港 01\"") {
		t.Error("流量记录不正确")
	}
	e.page("/user/sys")
	e.page("/user/disable")
	e.app.Cfg.Display.EnableDonate = true
	e.page("/user/donate")
}

// TestCheckinOncePerDay 验证签到一天只能一次，并发请求也不会重复签到（K24）。
func TestCheckinOncePerDay(t *testing.T) {
	e := newEnv(t)
	uid := e.login("u2@example.com")
	before, _ := e.app.Q.GetUser(context.Background(), uid)
	var wg sync.WaitGroup
	var mu sync.Mutex
	okCount := 0
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := e.post("/user/checkin", url.Values{})
			if r["ret"] == float64(1) {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("并发签到应只成功一次，实际 %d 次", okCount)
	}
	after, _ := e.app.Q.GetUser(context.Background(), uid)
	got := after.TransferEnable - before.TransferEnable
	if got < 1<<20 || got > 50<<20 {
		t.Fatalf("签到流量不在配置范围内: %d", got)
	}
}

// TestStateChangingRoutesRequirePost 验证会修改数据的操作不再接受 GET（K23）。
func TestStateChangingRoutesRequirePost(t *testing.T) {
	e := newEnv(t)
	e.login("u3@example.com")
	e.page("/user") // 生成订阅令牌
	if resp := e.get("/user/url_reset"); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /user/url_reset 应返回 405，实际 %d", resp.StatusCode)
	}
	resp, err := e.c.PostForm(e.ts.URL+"/user/url_reset", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/user" {
		t.Fatalf("POST /user/url_reset 应跳转用户中心: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// TestUserSettings 覆盖资料编辑页的各项设置。
func TestUserSettings(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.Users.PortPrice = 1
		c.Users.PortPriceSpecify = -1
		c.Register.MinPort, c.Register.MaxPort = 20000, 20010
	})
	uid := e.login("s1@example.com")
	ctx := context.Background()
	for _, p := range []string{"/user/edit", "/user/profile", "/user/kill"} {
		e.page(p)
	}
	if !strings.Contains(e.page("/user/edit"), "otpauth:") {
		t.Error("资料编辑页缺少两步验证二维码内容")
	}

	// 修改密码后当前登录仍然有效。
	if r := e.post("/user/password", url.Values{"oldpwd": {"wrong"}, "pwd": {"NewPassw0rd"}, "repwd": {"NewPassw0rd"}}); r["msg"] != "旧密码错误" {
		t.Fatalf("旧密码错误应被拒绝: %v", r)
	}
	if r := e.post("/user/password", url.Values{"oldpwd": {"Passw0rd!x"}, "pwd": {"NewPassw0rd"}, "repwd": {"NewPassw0rd"}}); r["ret"] != float64(1) {
		t.Fatalf("修改密码失败: %v", r)
	}
	e.page("/user/edit")

	// 加密方式与协议混淆不允许用户修改，提交不改动数据库。
	before, _ := e.app.Q.GetUser(ctx, uid)
	if r := e.post("/user/ssr", url.Values{"protocol": {"auth_aes128_md5"}, "obfs": {"tls1.2_ticket_auth"}, "obfs_param": {"a.com"}}); r["ret"] != float64(0) || r["msg"] != "不支持修改" {
		t.Fatalf("修改协议混淆应被拒绝: %v", r)
	}
	if r := e.post("/user/method", url.Values{"method": {"AES-256-CFB"}}); r["ret"] != float64(0) || r["msg"] != "不支持修改" {
		t.Fatalf("修改加密方式应被拒绝: %v", r)
	}
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.Method != before.Method || u.Protocol != before.Protocol || u.Obfs != before.Obfs || u.ObfsParam != before.ObfsParam {
		t.Fatalf("连接参数不应改变: %s %s %s %s", u.Method, u.Protocol, u.Obfs, u.ObfsParam)
	}
	if edit := e.page("/user/edit"); strings.Contains(edit, "选择客户端") || strings.Contains(edit, "method-update") || strings.Contains(edit, "ssr-update") || !strings.Contains(edit, "AnyTLS 节点的密码") ||
		!strings.Contains(edit, `<div class="card-heading">重置订阅链接</div>`) || !strings.Contains(edit, "postTo('/user/url_reset'") {
		t.Fatal("资料编辑页面不应再有加密方式与协议混淆设置，并应有重置订阅链接")
	}

	// 只接受 0 / 1 的选项。
	if r := e.post("/user/hide", url.Values{"hide": {"5"}}); r["ret"] != float64(0) {
		t.Fatalf("hide=5 应被拒绝: %v", r)
	}
	if r := e.post("/user/gaset", url.Values{"enable": {"1"}}); r["ret"] != float64(1) {
		t.Fatalf("开启两步验证失败: %v", r)
	}
	if r := e.post("/user/theme", url.Values{"theme": {"../x"}}); r["ret"] != float64(0) {
		t.Fatalf("不存在的主题应被拒绝: %v", r)
	}

	// 更换端口：余额不足、扣款、中转规则端口同步；未开放的指定端口（价格为负）不可调用（K30）。
	if r := e.post("/user/resetport", nil); r["msg"] != "余额不足" {
		t.Fatalf("余额不足应被拒绝: %v", r)
	}
	e.exec("UPDATE user SET money = 1.5 WHERE id = ?", uid)
	e.exec("INSERT INTO relay (user_id,source_node_id,dist_node_id,dist_ip,port,priority) VALUES (?,1,2,'',?,0)", uid, u.Port)
	r := e.post("/user/resetport", nil)
	if r["ret"] != float64(1) {
		t.Fatalf("更换端口失败: %v", r)
	}
	u, _ = e.app.Q.GetUser(ctx, uid)
	if float64(u.Port) != r["msg"] || u.Port < 20000 || u.Port > 20010 || u.Money.String() != "0.50" {
		t.Fatalf("更换端口结果不正确: port=%d money=%s msg=%v", u.Port, u.Money, r["msg"])
	}
	var relayPort int32
	_ = e.app.DB.QueryRowContext(ctx, "SELECT port FROM relay WHERE user_id = ?", uid).Scan(&relayPort)
	if relayPort != u.Port {
		t.Fatalf("中转规则端口未同步: %d != %d", relayPort, u.Port)
	}
	if r := e.post("/user/specifyport", url.Values{"port": {"20005"}}); r["ret"] != float64(0) {
		t.Fatalf("未开放的指定端口不应可用: %v", r)
	}

	// 删除账户。
	if r := e.post("/user/kill", url.Values{"passwd": {"Passw0rd!x"}}); r["ret"] != float64(0) {
		t.Fatalf("旧密码不应能删除账户: %v", r)
	}
	if r := e.post("/user/kill", url.Values{"passwd": {"NewPassw0rd"}}); r["ret"] != float64(1) {
		t.Fatalf("删除账户失败: %v", r)
	}
	if _, err := e.app.Q.GetUser(ctx, uid); !isNoRows(err) {
		t.Fatalf("账户应已删除: %v", err)
	}
}

// TestShopBuy 覆盖商店购买、优惠码、购买记录与关闭自动续费。
func TestShopBuy(t *testing.T) {
	e := newEnv(t)
	uid := e.login("b1@example.com")
	ctx := context.Background()
	e.exec(`INSERT INTO shop (id,name,price,content,auto_renew,auto_reset_bandwidth,status) VALUES
		(1,'月付套餐',10.00,'{"bandwidth":"100","expire":"30","class":"2","class_expire":"30","speedlimit":"50","connector":"3","content_extra":"check-全部节点;服务"}',30,1,1),
		(2,'下架',1.00,'{}',0,0,0)`)
	e.exec(`INSERT INTO coupon (code,onetime,expire,shop,credit) VALUES ('HALF',1,?, '1',50),('OLD',0,1,'',10)`, time.Now().Unix()+3600)

	shop := e.page("/user/shop")
	for _, want := range []string{"月付套餐", "全部节点", `data-args='["1",30]'`} {
		if !strings.Contains(shop, want) {
			t.Errorf("商店页缺少 %q", want)
		}
	}
	if r := e.post("/user/coupon_check", url.Values{"shop": {"1"}, "coupon": {"HALF"}}); r["total"] != "5元" || r["credit"] != "50 %" {
		t.Fatalf("优惠码计算不正确: %v", r)
	}
	if r := e.post("/user/coupon_check", url.Values{"shop": {"2"}}); r["msg"] != "非法请求" {
		t.Fatalf("下架商品不可购买: %v", r)
	}
	if r := e.post("/user/buy", url.Values{"shop": {"1"}, "coupon": {"NOPE"}}); r["msg"] != "优惠码无效" {
		t.Fatalf("无效优惠码应提示: %v", r)
	}
	if r := e.post("/user/buy", url.Values{"shop": {"1"}, "coupon": {"OLD"}}); r["msg"] != "此优惠码已过期" {
		t.Fatalf("过期优惠码应提示: %v", r)
	}
	if r := e.post("/user/buy", url.Values{"shop": {"1"}, "coupon": {"HALF"}}); !strings.Contains(r["msg"].(string), "余额不足，总价为5元") {
		t.Fatalf("余额不足应提示: %v", r)
	}
	// 管理员给个别用户设置的同时在线 IP 数（这里为 10）不被套餐的“IP限制”（3）覆盖。
	e.exec("UPDATE user SET money = 6, class = 0, node_connector = 10 WHERE id = ?", uid)
	if r := e.post("/user/buy", url.Values{"shop": {"1"}, "coupon": {"HALF"}, "autorenew": {"1"}}); r["ret"] != float64(1) {
		t.Fatalf("购买失败: %v", r)
	}
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.Money.String() != "1.00" || u.Class != 2 || u.NodeConnector != 10 || u.NodeSpeedlimit.String() != "50.00" || u.TransferEnable != 100<<30 {
		t.Fatalf("套餐未正确生效: money=%s class=%d conn=%d speed=%s te=%d", u.Money, u.Class, u.NodeConnector, u.NodeSpeedlimit, u.TransferEnable)
	}
	// 一次性优惠码：购买时也检查使用次数（K32）。
	e.exec("UPDATE user SET money = 100 WHERE id = ?", uid)
	if r := e.post("/user/buy", url.Values{"shop": {"1"}, "coupon": {"HALF"}}); r["msg"] != "优惠码次数已用完" {
		t.Fatalf("一次性优惠码不应再次可用: %v", r)
	}
	bought := e.page("/user/bought")
	if !strings.Contains(bought, "添加流量 100 G , 为账号的有效期添加 30 天") || !strings.Contains(bought, "10.00 元") {
		t.Errorf("购买记录不正确（一次性优惠码记录原价）")
	}
	var bid uint64
	_ = e.app.DB.QueryRowContext(ctx, "SELECT id FROM bought WHERE userid = ?", uid).Scan(&bid)
	req, _ := http.NewRequest(http.MethodDelete, e.ts.URL+"/user/bought", strings.NewReader("id="+strconv.FormatUint(bid, 10)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := e.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(b), "关闭自动续费成功") {
		t.Fatalf("关闭自动续费失败: %s", b)
	}
	var renew int64
	_ = e.app.DB.QueryRowContext(ctx, "SELECT renew FROM bought WHERE id = ?", bid).Scan(&renew)
	if renew != 0 {
		t.Fatal("自动续费未关闭")
	}
}

// TestRechargeCodeOnce 验证同一个充值码并发提交只能使用一次（K33），并在开启返利时给邀请人返利。
func TestRechargeCodeOnce(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Users.CodePayback = 20 })
	ref := e.login("r0@example.com")
	e.get("/user/logout")
	uid := e.login("r1@example.com")
	e.exec("UPDATE user SET ref_by = ? WHERE id = ?", ref, uid)
	e.exec(`INSERT INTO code (code,type,number,isused,userid,usedatetime) VALUES ('C100',-1,100,0,0,UTC_TIMESTAMP()),('T10',10001,10,0,0,UTC_TIMESTAMP())`)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); e.post("/user/code", url.Values{"code": {"C100"}}) }()
	}
	wg.Wait()
	ctx := context.Background()
	u, _ := e.app.Q.GetUser(ctx, uid)
	r, _ := e.app.Q.GetUser(ctx, ref)
	if u.Money.String() != "100.00" || r.Money.String() != "20.00" {
		t.Fatalf("充值码应只生效一次: 用户 %s，邀请人 %s", u.Money, r.Money)
	}
	if res := e.post("/user/code", url.Values{"code": {"T10"}}); res["ret"] != float64(1) {
		t.Fatalf("流量充值码失败: %v", res)
	}
	u2, _ := e.app.Q.GetUser(ctx, uid)
	if u2.TransferEnable-u.TransferEnable != 10<<30 {
		t.Fatalf("流量未增加: %d", u2.TransferEnable-u.TransferEnable)
	}
	if !strings.Contains(e.page("/user/code"), "充值 100.00 元") {
		t.Error("充值记录不正确")
	}
}

// TestNoPaybackByDefault 确认默认不返利：充值不给邀请人加钱、不写返利记录；邀请页只显示致谢、不显示返利。
func TestNoPaybackByDefault(t *testing.T) {
	e := newEnv(t)
	ref := e.login("np0@example.com")
	e.get("/user/logout")
	uid := e.login("np1@example.com")
	e.exec("UPDATE user SET ref_by = ?, class = 1 WHERE id = ?", ref, uid)
	e.exec(`INSERT INTO code (code,type,number,isused,userid,usedatetime) VALUES ('NP100',-1,100,0,0,UTC_TIMESTAMP())`)
	if res := e.post("/user/code", url.Values{"code": {"NP100"}}); res["ret"] != float64(1) {
		t.Fatalf("充值失败: %v", res)
	}
	ctx := context.Background()
	u, _ := e.app.Q.GetUser(ctx, uid)
	r, _ := e.app.Q.GetUser(ctx, ref)
	var n int
	_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM payback").Scan(&n)
	if u.Money.String() != "100.00" || r.Money.String() != "0.00" || n != 0 {
		t.Fatalf("默认不应返利: 用户 %s，邀请人 %s，返利记录 %d", u.Money, r.Money, n)
	}
	page := e.page("/user/invite")
	if !strings.Contains(page, "<p>谢谢大家的关照</p>") || !strings.Contains(page, "/auth/register?code=") {
		t.Fatal("邀请页应显示致谢与邀请链接")
	}
	for _, gone := range []string{"说明</p>", "返利", "流量奖励", "初始资金"} {
		if strings.Contains(page, gone) {
			t.Errorf("邀请页不应再有 %q", gone)
		}
	}
	if def := config.Default(); def.Users.CodePayback != 0 || def.Users.InviteGift != 0 || def.Register.InviteGetMoney != 0 {
		t.Errorf("返利默认值应为 0: %v %v %v", def.Users.CodePayback, def.Users.InviteGift, def.Register.InviteGetMoney)
	}
	if def := config.Default(); def.Register.ForbiddenIP != "127.0.0.0/8,0.0.0.0/8,10.0.0.0/8,100.64.0.0/10,169.254.0.0/16,172.16.0.0/12,192.168.0.0/16,::1/128,fc00::/7,fe80::/10" {
		t.Errorf("禁止访问 IP 默认值不正确: %s", def.Register.ForbiddenIP)
	}
}

// TestInvite 覆盖邀请页、购买邀请次数与定制邀请链接。
func TestInvite(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Register.InvitePrice = 2; c.Register.CustomInvitePrice = 5 })
	uid := e.login("i1@example.com")
	e.exec("UPDATE user SET class = 1, money = 10 WHERE id = ?", uid)
	if !strings.Contains(e.page("/user/invite"), "/auth/register?code=") {
		t.Fatal("邀请页缺少邀请链接")
	}
	if r := e.post("/user/buy_invite", url.Values{"num": {"2"}}); r["ret"] != float64(1) {
		t.Fatalf("购买邀请次数失败: %v", r)
	}
	if r := e.post("/user/custom_invite", url.Values{"customcode": {"my vip"}}); r["ret"] != float64(0) {
		t.Fatalf("含特殊字符的后缀应被拒绝: %v", r)
	}
	if r := e.post("/user/custom_invite", url.Values{"customcode": {"myvip"}}); r["ret"] != float64(1) {
		t.Fatalf("定制邀请链接失败: %v", r)
	}
	if !strings.Contains(e.page("/user/invite"), "code=myvip") {
		t.Fatal("邀请链接未更新")
	}
	u, _ := e.app.Q.GetUser(context.Background(), uid)
	if u.Money.String() != "1.00" {
		t.Fatalf("扣款不正确: %s", u.Money)
	}
}

// TestRetiredUserFeatures 确认用户工单、面板中转、延迟检测与站内教程已停用，联系方式页面与审计页面正常。
func TestRetiredUserFeatures(t *testing.T) {
	e := newEnv(t)
	e.seedNodes()
	uid := e.login("t1@example.com")
	ctx := context.Background()
	e.exec("UPDATE user SET class = 5 WHERE id = ?", uid)

	for path, to := range map[string]string{
		"/user/tutorial": "/client-subscription-guide.html", "/user/lookingglass": "/user",
		"/user/relay": "/user", "/user/relay/create": "/user", "/user/relay/1/edit": "/user",
		"/user/ticket": "/user/contact", "/user/ticket/create": "/user/contact", "/user/ticket/1/view": "/user/contact",
	} {
		resp := e.get(path)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != to {
			t.Errorf("%s 应跳转到 %s: %d %s", path, to, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	if r := e.post("/user/ticket", url.Values{"title": {"求助"}, "content": {"<p>你好</p>"}}); r["ret"] != float64(0) || r["msg"] != "不支持发起工单，请通过联系方式联系管理员" {
		t.Fatalf("应拒绝发起工单: %v", r)
	}
	if r := e.post("/user/relay", url.Values{"source_node": {"2"}, "dist_node": {"1"}, "port": {"1"}, "priority": {"1"}}); r["ret"] != float64(0) || r["msg"] != "不支持面板中转" {
		t.Fatalf("应拒绝添加中转规则: %v", r)
	}
	for _, m := range []string{http.MethodPut, http.MethodDelete} {
		path := "/user/ticket/1"
		if m == http.MethodDelete {
			path = "/user/relay"
		}
		req, _ := http.NewRequest(m, e.ts.URL+path, strings.NewReader("id=1&content=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := e.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !strings.Contains(string(b), `"ret":0`) {
			t.Fatalf("%s %s 应被拒绝: %s", m, path, b)
		}
	}
	var n int
	_ = e.app.DB.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM ticket) + (SELECT COUNT(*) FROM relay)").Scan(&n)
	if n != 0 {
		t.Fatalf("不应写入工单或中转规则: %d", n)
	}

	// 联系方式页面：未设置时有提示，设置后显示配置的内容。
	if page := e.page("/user/contact"); !strings.Contains(page, "管理员尚未设置联系方式") || !strings.Contains(page, "不支持发起工单") {
		t.Fatal("联系方式页面未设置时的提示不正确")
	}
	e.app.Cfg.Contact.AdminContact1 = "邮箱：admin@example.com"
	e.app.Cfg.Contact.AdminContact3 = "Telegram：<b>@example</b>"
	page := e.page("/user/contact")
	if !strings.Contains(page, "邮箱：admin@example.com") || !strings.Contains(page, "Telegram：&lt;b&gt;@example&lt;/b&gt;") || strings.Contains(page, "管理员尚未设置联系方式") {
		t.Fatal("联系方式页面内容不正确")
	}
	menu := e.page("/user")
	for _, gone := range []string{`href="/user/ticket"`, `href="/user/relay"`, `href="/user/lookingglass"`, `href="/user/tutorial"`} {
		if strings.Contains(menu, gone) {
			t.Errorf("菜单不应再有 %s", gone)
		}
	}
	if !strings.Contains(menu, `href="/user/contact"`) || !strings.Contains(menu, `href="/client-subscription-guide.html" target="_blank" rel="noopener"><i class="icon icon-lg">start</i>&nbsp;使用教程`) {
		t.Error("菜单缺少联系我们或客户端订阅教程")
	}

	// 审计。
	e.exec("INSERT INTO detect_list (name,text,regex,type) VALUES ('BT','禁止 BT','(Torrent)',1)")
	e.exec("INSERT INTO detect_log (user_id,list_id,datetime,node_id) VALUES (?,1,UNIX_TIMESTAMP(),1)", uid)
	if !strings.Contains(e.page("/user/detect"), "禁止 BT") || !strings.Contains(e.page("/user/detect/log"), "香港 01") {
		t.Fatal("审计页面不正确")
	}
}

// TestRelayConflictLoop 验证环形中转规则不会导致无限递归（原版会栈溢出）。
func TestRelayConflictLoop(t *testing.T) {
	rules := []store.Relay{
		{ID: 1, SourceNodeID: 10, DistNodeID: 11},
		{ID: 2, SourceNodeID: 11, DistNodeID: 10},
	}
	nodes := map[int32]store.SsNode{10: {ID: 10, Sort: 10}, 11: {ID: 11, Sort: 10}, 12: {ID: 12, Sort: 0}}
	got := relayConflict(store.Relay{SourceNodeID: 12, DistNodeID: 10}, rules, 0, 12, nodes, map[uint64]bool{})
	if got != 0 {
		t.Fatalf("不应判定为冲突: %d", got)
	}
	if got := relayConflict(store.Relay{SourceNodeID: 11, DistNodeID: 10}, rules, 0, 11, nodes, map[uint64]bool{}); got != 1 {
		t.Fatalf("应与规则 1 冲突: %d", got)
	}
}

// login2 在已登录状态下注册另一个用户（不切换登录），返回其 ID。
func (e *testEnv) login2(email string) uint32 {
	e.t.Helper()
	hash := "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	p := e.app.newUserParams(email, email, hash, "127.0.0.1", 0, 2, email, time.Now())
	id, err := e.app.Q.CreateUser(context.Background(), p)
	if err != nil {
		e.t.Fatal(err)
	}
	return uint32(id) //nolint:gosec // 测试
}

// TestConnectionPasswordRotatesCredentials 确认重置节点连接密码会同时更换 VMess / VLESS 的 UUID、SS2022 用户密钥与 AnyTLS 密码。
func TestConnectionPasswordRotatesCredentials(t *testing.T) {
	e := newEnv(t)
	uid := e.login("pwd@example.com")
	ctx := context.Background()
	creds := func() [3]string {
		u, err := e.app.Q.GetUser(ctx, uid)
		if err != nil {
			t.Fatal(err)
		}
		return [3]string{model.NewUser(u, time.UTC).UUID(), sub.SS2022UserKey(u, 1, "c2VydmVyLWtleS0xMjM0NTY3ODkwMTIzNDU2Nzg5MDE="), sub.AnyTLSPassword(u)}
	}
	before := creds()
	if r := e.post("/user/sspwd", url.Values{"sspwd": {"Rotated123"}}); r["ret"] != float64(1) {
		t.Fatalf("重置连接密码失败: %v", r)
	}
	after := creds()
	for i, name := range []string{"UUID", "SS2022 密钥", "AnyTLS 密码"} {
		if before[i] == after[i] {
			t.Errorf("重置连接密码后 %s 未改变", name)
		}
	}
}

// TestGroupLinks 确认首页「群组」按配置显示，等级不足时不显示链接，未配置时不显示区块。
func TestGroupLinks(t *testing.T) {
	e := newEnv(t)
	uid := e.login("group@example.com")
	if strings.Contains(e.page("/user"), "group-links") {
		t.Fatal("未配置群组时不应显示")
	}
	e.app.Cfg.Contact.GroupLinks = []config.GroupLink{
		{Title: "Telegram(VIP可见)", Text: "点击加入TG群", URL: "https://t.me/example_group", MinClass: 1},
		{Title: "公开<b>频道</b>", Text: "点击加入频道", URL: "https://t.me/example_channel"},
	}
	e.exec("UPDATE user SET class = 0 WHERE id = ?", uid)
	page := e.page("/user")
	if !strings.Contains(page, "Telegram(VIP可见)") || strings.Contains(page, "https://t.me/example_group") || !strings.Contains(page, "购买套餐后可见") {
		t.Fatal("等级不足时应只显示标题")
	}
	if !strings.Contains(page, `href="https://t.me/example_channel"`) || !strings.Contains(page, "公开&lt;b&gt;频道&lt;/b&gt;") {
		t.Fatal("所有人可见的入口应显示链接并转义标题")
	}
	if strings.Index(page, "流量使用情况") > strings.Index(page, "group-links") || strings.Index(page, "group-links") > strings.Index(page, "签到</p>") {
		t.Fatal("群组应位于流量使用情况与签到之间")
	}
	e.exec("UPDATE user SET class = 1 WHERE id = ?", uid)
	if page := e.page("/user"); !strings.Contains(page, `href="https://t.me/example_group" target="_blank" rel="noopener noreferrer">点击加入TG群</a>`) {
		t.Fatal("等级足够时应显示链接")
	}

	// 四种允许的协议在页面上原样输出（html/template 默认会把 tg:// 换成 #ZgotmplZ）；特殊字符仍转义。
	e.app.Cfg.Contact.GroupLinks = []config.GroupLink{
		{Title: "a", Text: "https", URL: "https://t.me/example_group"},
		{Title: "b", Text: "http", URL: "http://example.com/x?a=1&b=2"},
		{Title: "c", Text: "tg", URL: "tg://resolve?domain=example"},
		{Title: "d", Text: "mailto", URL: "mailto:admin@example.com"},
		{Title: "e", Text: "非法", URL: "javascript:alert(1)"},
	}
	page = e.page("/user")
	for _, href := range []string{`href="https://t.me/example_group"`, `href="http://example.com/x?a=1&amp;b=2"`, `href="tg://resolve?domain=example"`, `href="mailto:admin@example.com"`} {
		if !strings.Contains(page, href) {
			t.Errorf("缺少 %s", href)
		}
	}
	if strings.Contains(page, "ZgotmplZ") || strings.Contains(page, "javascript:alert") || strings.Contains(page, ">非法<") {
		t.Error("链接被过滤，或非法协议被输出")
	}
}

// TestSubscriptionLines 确认没有备用订阅地址时不显示订阅线路；设置后列出主线路与备用线路，且备用域名能取得同样的订阅。
func TestSubscriptionLines(t *testing.T) {
	e := newEnv(t)
	e.seedNodes()
	e.login("lines@example.com")
	page := e.page("/user")
	if strings.Contains(page, `id="sub-line"`) {
		t.Fatal("没有备用订阅地址时不应显示订阅线路")
	}
	m := regexp.MustCompile(`data-sub-base="([^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("缺少订阅地址")
	}
	main := m[1]
	token := main[strings.LastIndex(main, "/")+1:]
	e.app.Cfg.App.SubURLBackup = []string{"https://sub2.example.net/link/", "https://sub3.example.org/link/"}
	page = e.page("/user")
	for _, want := range []string{`<select id="sub-line" class="form-control">`, `<option value="` + main + `">主线路</option>`,
		`<option value="https://sub2.example.net/link/` + token + `">备用线路 1</option>`, `<option value="https://sub3.example.org/link/` + token + `">备用线路 2</option>`} {
		if !strings.Contains(page, want) {
			t.Errorf("缺少 %s", want)
		}
	}
	// 订阅路径与域名无关：用备用域名访问得到与主域名相同的内容。
	req := func(host string) string {
		r, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/link/"+token+"?sub=3", nil)
		r.Host = host
		resp, err := e.c.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s 返回 %d", host, resp.StatusCode)
		}
		return string(b)
	}
	if a, b := req("panel.example.com"), req("sub2.example.net"); a != b || a == "" {
		t.Error("备用域名取得的订阅与主域名不同")
	}
}
