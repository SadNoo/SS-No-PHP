package app

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/radius"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/testdb"
)

// freeradiusSchema 是 FreeRADIUS 3.x SQL 模块表结构中面板用到的部分。
var freeradiusSchema = []string{
	`CREATE TABLE radcheck (id INT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, username VARCHAR(64) NOT NULL DEFAULT '',
		attribute VARCHAR(64) NOT NULL DEFAULT '', op CHAR(2) NOT NULL DEFAULT '==', value VARCHAR(253) NOT NULL DEFAULT '', KEY username (username(32)))`,
	`CREATE TABLE radusergroup (id INT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, username VARCHAR(64) NOT NULL DEFAULT '',
		groupname VARCHAR(64) NOT NULL DEFAULT '', priority INT NOT NULL DEFAULT 1, KEY username (username(32)))`,
	`CREATE TABLE nas (id INT NOT NULL AUTO_INCREMENT PRIMARY KEY, nasname VARCHAR(128) NOT NULL, shortname VARCHAR(32),
		type VARCHAR(30) DEFAULT 'other', ports INT, secret VARCHAR(60) DEFAULT 'secret' NOT NULL, server VARCHAR(64),
		community VARCHAR(50), description VARCHAR(200) DEFAULT 'RADIUS Client')`,
	`CREATE TABLE radpostauth (id INT NOT NULL AUTO_INCREMENT PRIMARY KEY, username VARCHAR(64) NOT NULL DEFAULT '',
		pass VARCHAR(64) NOT NULL DEFAULT '', reply VARCHAR(32) NOT NULL DEFAULT '', authdate TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6))`,
	`CREATE TABLE radacct (radacctid BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, acctsessionid VARCHAR(64) NOT NULL DEFAULT '',
		username VARCHAR(64) NOT NULL DEFAULT '', acctstarttime DATETIME NULL, acctstoptime DATETIME NULL,
		acctinputoctets BIGINT, acctoutputoctets BIGINT)`,
}

func TestRadiusSync(t *testing.T) {
	e := newEnv(t)
	rdb := testdb.Empty(t)
	ctx := context.Background()
	for _, s := range freeradiusSchema {
		if _, err := rdb.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	e.app.Radius = &radius.Client{DB: rdb, Secret: "nas-secret"}
	rq := func(q string, args ...any) (n int64) {
		t.Helper()
		if err := rdb.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var val string
	pwdOf := func(name string) string {
		t.Helper()
		val = ""
		_ = rdb.QueryRowContext(ctx, "SELECT value FROM radcheck WHERE username = ?", name).Scan(&val)
		return val
	}

	// 注册时创建帐号，修改连接密码时同步。
	uid := e.login("vpn.user@example.com")
	u, _ := e.app.Q.GetUser(ctx, uid)
	if pwdOf("vpnuserexamplecom") != u.Passwd || rq("SELECT COUNT(*) FROM radusergroup WHERE username = 'vpnuserexamplecom' AND groupname = 'user'") != 1 {
		t.Fatal("注册后应创建 Radius 帐号")
	}
	if r := e.post("/user/sspwd", url.Values{"sspwd": {"NewPass123"}}); r["ret"] != float64(1) {
		t.Fatalf("修改连接密码失败: %v", r)
	}
	if pwdOf("vpnuserexamplecom") != "NewPass123" {
		t.Fatalf("密码未同步: %q", val)
	}

	// 禁用后删除帐号并记入 radius_ban，恢复后重新创建。
	now := time.Now()
	e.exec("UPDATE user SET enable = 0 WHERE id = ?", uid)
	if err := e.app.radiusBanCheck(ctx, now); err != nil {
		t.Fatal(err)
	}
	if rq("SELECT COUNT(*) FROM radcheck") != 0 {
		t.Fatal("禁用的用户应删除 Radius 帐号")
	}
	if n, _ := e.app.Q.CountRadiusBan(ctx, uid); n != 1 {
		t.Fatal("应记入 radius_ban")
	}
	e.app.radiusAdd(ctx, u, "x") // 封禁期间不创建
	if rq("SELECT COUNT(*) FROM radcheck") != 0 {
		t.Fatal("封禁期间不应创建帐号")
	}
	_ = e.app.radiusBanCheck(ctx, now)
	if n, _ := e.app.Q.CountRadiusBan(ctx, uid); n != 1 {
		t.Fatal("重复检查不应重复记录")
	}
	e.exec("UPDATE user SET enable = 1 WHERE id = ?", uid)
	_ = e.app.radiusBanCheck(ctx, now)
	if pwdOf("vpnuserexamplecom") != "NewPass123" {
		t.Fatal("恢复后应重新创建帐号")
	}

	// 后台修改邮箱时改名。
	u, _ = e.app.Q.GetUser(ctx, uid)
	e.app.radiusRename(ctx, u, "vpn2@example.com", "P2")
	if pwdOf("vpn2examplecom") != "P2" || rq("SELECT COUNT(*) FROM radusergroup WHERE username = 'vpn2examplecom'") != 1 {
		t.Fatal("改名失败")
	}
	e.exec("UPDATE user SET email = 'vpn2@example.com' WHERE id = ?", uid)

	// 认证与计费记录：记录处理位置，不重复、不遗漏。
	if err := e.app.radiusSyncLogin(ctx, now); err != nil { // 首次执行只记录位置
		t.Fatal(err)
	}
	if err := e.app.radiusSyncVPN(ctx, now); err != nil {
		t.Fatal(err)
	}
	rnow, _ := e.app.Radius.Now(ctx)
	past := rnow.Add(-time.Minute).Format(time.DateTime)
	for _, k := range []string{"radius:synclogin", "radius:syncvpn"} {
		_ = e.app.Q.SetJobState(ctx, store.SetJobStateParams{Name: k, Value: past, UpdatedAt: now.Unix()})
	}
	at := rnow.Add(-5 * time.Second)
	for _, q := range []string{
		"INSERT INTO radpostauth (username, reply, authdate) VALUES ('vpn2examplecom', 'Access-Accept', ?)",
		"INSERT INTO radpostauth (username, reply, authdate) VALUES ('vpn2examplecom', 'Access-Reject', ?)",
		"INSERT INTO radpostauth (username, reply, authdate) VALUES ('nobody', 'Access-Accept', ?)",
		"INSERT INTO radacct (username, acctstarttime, acctstoptime, acctinputoctets, acctoutputoctets) VALUES ('vpn2examplecom', ?, ?, 1000, 5000)",
		"INSERT INTO radacct (username, acctstarttime, acctstoptime, acctinputoctets, acctoutputoctets) VALUES ('nobody', ?, ?, 1, 1)",
	} {
		args := []any{at}
		if strings.Contains(q, "radacct") {
			args = []any{at, at}
		}
		if _, err := rdb.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := e.app.radiusSyncLogin(ctx, now); err != nil {
			t.Fatal(err)
		}
		if err := e.app.radiusSyncVPN(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	u, _ = e.app.Q.GetUser(ctx, uid)
	if u.U != 1000 || u.D != 15000 {
		t.Fatalf("流量应只记一次: u=%d d=%d", u.U, u.D)
	}

	// VPN 节点写入 NAS。
	e.app.radiusNas(ctx, "10.0.0.1", true)
	e.app.radiusNas(ctx, "10.0.0.1", true)
	if rq("SELECT COUNT(*) FROM nas WHERE shortname = '10.0.0.1' AND secret = 'nas-secret'") != 1 {
		t.Fatal("NAS 应只添加一次")
	}
	e.app.radiusNas(ctx, "10.0.0.1", false)
	if rq("SELECT COUNT(*) FROM nas") != 0 {
		t.Fatal("NAS 未删除")
	}

	// 删除帐号时删除 Radius 帐号。
	if err := e.app.killUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if rq("SELECT COUNT(*) FROM radcheck") != 0 || rq("SELECT COUNT(*) FROM radusergroup") != 0 {
		t.Fatal("删除用户后应删除 Radius 帐号")
	}
}
