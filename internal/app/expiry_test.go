package app

import (
	"bytes"
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSubscriptionExpiryHTTP 通过真实路由验证订阅到期口径、后台保存校验、新帐号 100 年有效期与两个运维命令。
func TestSubscriptionExpiryHTTP(t *testing.T) {
	e, uid := adminEnv(t)
	ctx := context.Background()

	// 新注册帐号有效期为 100 年。
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.ExpireIn.Before(time.Now().AddDate(99, 0, 0)) {
		t.Fatalf("新帐号有效期应为 100 年: %s", u.ExpireIn)
	}

	token, err := e.app.Sub.SubToken(ctx, uid, 0)
	if err != nil {
		t.Fatal(err)
	}
	header := func() string {
		t.Helper()
		status, h, _ := e.response("GET", "/link/"+token+"?v2ray=1", "")
		if status != 200 {
			t.Fatalf("订阅返回 %d", status)
		}
		return h.Get("Subscription-Userinfo")
	}
	// 普通用户（等级 0，注册时带 24 小时等级到期）：响应头不写 expire。
	e.exec("UPDATE user SET class = 0, class_expire = UTC_TIMESTAMP() + INTERVAL 1 DAY WHERE id = ?", uid)
	if h := header(); strings.Contains(h, "expire=") || !strings.HasPrefix(h, "upload=") {
		t.Fatalf("普通用户不应写 expire: %q", h)
	}
	// 付费用户：expire 为等级到期时间。
	planEnd := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	e.exec("UPDATE user SET class = 2, class_expire = ? WHERE id = ?", planEnd, uid)
	if h := header(); !strings.HasSuffix(h, "; expire="+strconv.FormatInt(planEnd.Unix(), 10)) {
		t.Fatalf("付费用户应写套餐到期: %q", h)
	}
	// 异常用户：等级大于 0 但无等级到期，不写 expire（不能显示为 1989 年已过期）。
	e.exec("UPDATE user SET class = 2, class_expire = '1989-06-04 00:05:00' WHERE id = ?", uid)
	if h := header(); strings.Contains(h, "expire=") {
		t.Fatalf("异常用户不应写 expire: %q", h)
	}

	// 运维命令：列出异常用户。
	var out bytes.Buffer
	if err := e.app.XCat(ctx, []string{"checkClassExpire"}, &out); err != nil || !strings.Contains(out.String(), "admin@example.com") {
		t.Fatalf("checkClassExpire: %v %s", err, out.String())
	}

	// 后台保存：等级大于 0 时必须填写等级到期时间。
	form := url.Values{"email": {"admin@example.com"}, "pass": {""}, "auto_reset_day": {"0"}, "auto_reset_bandwidth": {"0"}, "is_multi_user": {"0"},
		"port": {"23456"}, "group": {"0"}, "passwd": {"pw"}, "transfer_enable": {"50"}, "invite_num": {"0"}, "node_speedlimit": {"0"},
		"method": {"rc4-md5"}, "remark": {""}, "money": {"0"}, "enable": {"1"}, "is_admin": {"1"}, "ga_enable": {"0"}, "ref_by": {"0"},
		"forbidden_ip": {""}, "forbidden_port": {""}, "class": {"3"}, "class_expire": {"1989-06-04 00:05:00"},
		"expire_in": {"2099-06-04 00:05:00"}, "node_connector": {"0"}, "protocol": {"origin"}, "protocol_param": {""}, "obfs": {"plain"}, "obfs_param": {""}}
	path := "/admin/user/" + strconv.Itoa(int(uid))
	if body := e.raw("PUT", path, "application/x-www-form-urlencoded", form.Encode()); !strings.Contains(body, "必须填写等级到期时间") {
		t.Fatalf("应拒绝无等级到期: %s", body)
	}
	form.Set("class", "0")
	if body := e.raw("PUT", path, "application/x-www-form-urlencoded", form.Encode()); !strings.Contains(body, "修改成功") {
		t.Fatalf("等级 0 应可保存: %s", body)
	}
	// 同时在线 IP 数必须是非负整数；无法识别的输入不能被当作 0（不限）。
	for _, bad := range []string{"-1", "abc", "", "1.5", "10001"} {
		form.Set("node_connector", bad)
		if body := e.raw("PUT", path, "application/x-www-form-urlencoded", form.Encode()); !strings.Contains(body, "同时连接 IP 数") {
			t.Fatalf("应拒绝 IP 数 %q: %s", bad, body)
		}
	}
	form.Set("node_connector", "10")
	if body := e.raw("PUT", path, "application/x-www-form-urlencoded", form.Encode()); !strings.Contains(body, "修改成功") {
		t.Fatalf("IP 数 10 应可保存: %s", body)
	}
	if x, _ := e.app.Q.GetUser(ctx, uid); x.NodeConnector != 10 {
		t.Fatalf("IP 数应为 10: %d", x.NodeConnector)
	}

	// 运维命令：帐号有效期调整为 100 年。预览不修改；执行只改仍有效的帐号，2099 永久与已过期帐号默认不动。
	e.exec(`INSERT INTO user (id,user_name,email,pass,passwd,reg_date,expire_in,im_value,pac,remark,forbidden_ip,forbidden_port,disconnect_ip) VALUES
		(901,'a','valid@example.com','x','p',UTC_TIMESTAMP(),UTC_TIMESTAMP() + INTERVAL 30 DAY,'','','','','',''),
		(902,'b','expired@example.com','x','p',UTC_TIMESTAMP(),UTC_TIMESTAMP() - INTERVAL 1 DAY,'','','','','',''),
		(903,'c','forever@example.com','x','p',UTC_TIMESTAMP(),'2099-06-04 00:05:00','','','','','','')`)
	expire := func(id uint32) time.Time { x, _ := e.app.Q.GetUser(ctx, id); return x.ExpireIn }
	out.Reset()
	if err := e.app.XCat(ctx, []string{"extendAccounts"}, &out); err != nil || !strings.Contains(out.String(), "预览") {
		t.Fatalf("extendAccounts 预览: %v %s", err, out.String())
	}
	if expire(901).After(time.Now().AddDate(1, 0, 0)) {
		t.Fatal("预览不应修改数据")
	}
	if err := e.app.XCat(ctx, []string{"extendAccounts", "--apply"}, &out); err != nil {
		t.Fatal(err)
	}
	if expire(901).Before(time.Now().AddDate(99, 0, 0)) || expire(902).After(time.Now()) || expire(903).Year() != 2099 {
		t.Fatalf("调整结果错误: %s %s %s", expire(901), expire(902), expire(903))
	}
	if err := e.app.XCat(ctx, []string{"extendAccounts", "--apply", "--include-expired"}, &out); err != nil {
		t.Fatal(err)
	}
	if expire(902).Before(time.Now().AddDate(99, 0, 0)) || expire(903).Year() != 2099 {
		t.Fatalf("--include-expired 结果错误: %s %s", expire(902), expire(903))
	}
	if err := e.app.XCat(ctx, []string{"extendAccounts", "--bogus"}, &out); err == nil {
		t.Fatal("未知参数应报错")
	}
}
