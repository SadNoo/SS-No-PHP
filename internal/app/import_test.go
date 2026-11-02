package app

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/SadNoo/SS-No-PHP/internal/legacy"
	"github.com/SadNoo/SS-No-PHP/internal/testdb"
)

// TestImportLegacy 用原版的建表脚本（sql/glzjin_all.sql）建立旧库，导入后在新版中登录并检查数据。
func TestImportLegacy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old := testdb.Empty(t)
	var name string
	if err := old.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	mc, _ := mysql.ParseDSN(os.Getenv("NIUNIU_TEST_MYSQL_DSN"))
	mc.DBName, mc.MultiStatements = name, true
	loader, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = loader.Close() }()
	schema, err := os.ReadFile("../legacy/testdata/sspanel_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.ExecContext(ctx, string(schema)); err != nil {
		t.Fatalf("加载原版表结构: %v", err)
	}
	sum := md5.Sum([]byte("oldpass123" + "salt"))
	if _, err := loader.ExecContext(ctx, `
		INSERT INTO user (id,user_name,email,pass,passwd,u,d,transfer_enable,port,reg_date,invite_num,money,node_speedlimit,im_value,class_expire,expire_in,theme,ga_token,pac,remark)
		VALUES (7,'老用户','old@example.com','`+hex.EncodeToString(sum[:])+`','sspwd',1,2,1073741824,10086,'2019-05-01 08:30:00',3,'12.50','',NULL,'1989-06-04 00:05:00','2020-12-31 23:59:59','material','',NULL,NULL);
		INSERT INTO code (id,code,type,number,isused,userid,usedatetime) VALUES (1,'C1',-1,10,1,7,'2019-05-02 00:00:00'),(2,'C2',-1,5,0,0,'0000-00-00 00:00:00');
		UPDATE config SET value = 'admin@example.com' WHERE name = 'Notice_EMail';
		UPDATE user SET disconnect_ip = '256.1.1.1,1.2.3.4:80, 198.51.100.9 ' WHERE id = 7;
		INSERT INTO disconnect_ip (userid,ip,datetime) VALUES (7,'198.51.100.9',UNIX_TIMESTAMP());`); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	sh, _ := time.LoadLocation("Asia/Shanghai")
	im := &legacy.Importer{Src: old, Dst: e.app.DB, Loc: sh, Out: &out, Batch: 1}
	if err := im.Run(ctx); err != nil {
		t.Fatalf("导入失败: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "user ") {
		t.Fatalf("导入输出缺少 user 表:\n%s", out.String())
	}
	// 封禁 IP 异常：报告非法项的分类与长度（不输出原文），导入的数据不改动。
	if s := out.String(); !strings.Contains(s, "注意：导入的封禁 IP（disconnect_ip）有异常") ||
		!strings.Contains(s, "用户 7：合法 1 项（其中没有对应记录 0 项）、非法 2 项（带端口或混入冒号×1、数字段超过 255×1；长度 9,10）") ||
		strings.Contains(s, "256.1.1.1") || strings.Contains(s, "1.2.3.4:80") {
		t.Errorf("导入应报告封禁 IP 异常且不输出原文:\n%s", s)
	}

	u, err := e.app.Q.GetUser(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.RegDate.UTC().Format(time.DateTime); got != "2019-05-01 00:30:00" {
		t.Errorf("注册时间应换算为 UTC: %s", got)
	}
	if got := u.ExpireIn.UTC().Format(time.DateTime); got != "2020-12-31 15:59:59" {
		t.Errorf("过期时间应换算为 UTC: %s", got)
	}
	if got := u.ClassExpire.UTC().Format(time.DateTime); got != "1989-06-04 00:05:00" {
		t.Errorf("哨兵值不应换算: %s", got)
	}
	if u.DisconnectIp != "256.1.1.1,1.2.3.4:80, 198.51.100.9 " {
		t.Errorf("导入不应改动封禁 IP: %q", u.DisconnectIp)
	}
	if u.ImValue != "" || u.Money.String() != "12.50" || u.NodeSpeedlimit.String() != "0.00" || u.Port != 10086 {
		t.Errorf("字段转换错误: im_value=%q money=%s speed=%s port=%d", u.ImValue, u.Money, u.NodeSpeedlimit, u.Port)
	}
	var used string
	_ = e.app.DB.QueryRowContext(ctx, "SELECT usedatetime FROM code WHERE id = 2").Scan(&used)
	if !strings.HasPrefix(used, "1970-01-01") {
		t.Errorf("无效日期应改为默认值: %s", used)
	}
	var notice string
	_ = e.app.DB.QueryRowContext(ctx, "SELECT value FROM config WHERE name = 'Notice_EMail'").Scan(&notice)
	if notice != "admin@example.com" {
		t.Errorf("config 应以旧库为准: %q", notice)
	}

	// 旧密码可以登录（并升级为 argon2id），新注册用户的 ID 接在旧数据之后。
	if r := e.post("/auth/login", url.Values{"email": {"old@example.com"}, "passwd": {"oldpass123"}}); r["ret"] != float64(1) {
		t.Fatalf("导入的用户无法登录: %v", r)
	}
	e.page("/user")
	e.get("/user/logout")
	if id := e.login("new@example.com"); id <= 7 {
		t.Errorf("新用户 ID 应大于导入的 ID: %d", id)
	}

	// 再次导入会被拒绝。
	if err := im.Run(ctx); err == nil || !strings.Contains(err.Error(), "全新的数据库") {
		t.Errorf("非空库应拒绝导入: %v", err)
	}
}
