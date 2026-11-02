package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// failingRollback 在第 failAt 次写入（从 1 开始）、Sync 或 Close 时返回错误；short 时返回部分写入。
type failingRollback struct {
	writes, failAt          int
	short, failSync, failCl bool
	buf                     strings.Builder
}

func (f *failingRollback) Write(p []byte) (int, error) {
	f.writes++
	if f.writes == f.failAt {
		if f.short {
			f.buf.Write(p[:len(p)/2])
			return len(p) / 2, nil
		}
		return 0, errors.New("磁盘已满（测试）")
	}
	f.buf.Write(p)
	return len(p), nil
}
func (f *failingRollback) Sync() error {
	if f.failSync {
		return errors.New("刷盘失败（测试）")
	}
	return nil
}
func (f *failingRollback) Close() error {
	if f.failCl {
		return errors.New("关闭失败（测试）")
	}
	return nil
}

// repairEnv 准备两个需要修复的用户（各有合法项与非法项）。
func repairEnv(t *testing.T) (*testEnv, []uint32) {
	e := newEnv(t)
	a := e.login("r1@example.com")
	b := e.login2("r2@example.com")
	e.exec("UPDATE user SET disconnect_ip = '198.51.100.1,256.1.1.1' WHERE id = ?", a)
	e.exec("UPDATE user SET disconnect_ip = '198.51.100.2,1.2.3' WHERE id = ?", b)
	return e, []uint32{a, b}
}

func disconnectOf(t *testing.T, e *testEnv, uid uint32) string {
	t.Helper()
	var v string
	if err := e.app.DB.QueryRow("SELECT disconnect_ip FROM user WHERE id = ?", uid).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestDisconnectRepairRollbackWriteFailure 回滚文件（第一条、中途、部分写入、刷盘、关闭）任何一步失败、文件已存在、
// 未指定回滚文件、输出失败时：返回错误，数据库零修改，不留下不完整的回滚文件。（第三方 P2-1）
func TestDisconnectRepairRollbackWriteFailure(t *testing.T) {
	e, users := repairEnv(t)
	ctx := context.Background()
	orig := createRollbackFile
	t.Cleanup(func() { createRollbackFile = orig })
	unchanged := func(name string) {
		t.Helper()
		if disconnectOf(t, e, users[0]) != "198.51.100.1,256.1.1.1" || disconnectOf(t, e, users[1]) != "198.51.100.2,1.2.3" {
			t.Fatalf("%s：数据库不应有任何修改", name)
		}
	}
	// 回滚文件：说明 2 行 + 回滚语句 2 条，第 3 次写入是第一条语句，第 4 次是第二条（中途）。
	for _, c := range []struct {
		name string
		f    *failingRollback
	}{
		{"第一条回滚语句写入失败", &failingRollback{failAt: 3}},
		{"中途（第二条）回滚语句写入失败", &failingRollback{failAt: 4}},
		{"第二条回滚语句只写入一半", &failingRollback{failAt: 4, short: true}},
		{"刷盘失败", &failingRollback{failSync: true}},
		{"关闭失败", &failingRollback{failCl: true}},
	} {
		createRollbackFile = func(string) (rollbackFile, error) { return c.f, nil }
		path := filepath.Join(t.TempDir(), "rb.sql")
		var out strings.Builder
		err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--apply", "--rollback-file", path}, &out)
		if err == nil || !strings.Contains(err.Error(), "没有修改数据库") {
			t.Fatalf("%s：应返回错误: %v", c.name, err)
		}
		unchanged(c.name)
	}
	createRollbackFile = orig

	// 真实文件：已存在时不覆盖；目录不存在。
	exists := filepath.Join(t.TempDir(), "exists.sql")
	_ = os.WriteFile(exists, []byte("旧内容"), 0o600)
	for name, path := range map[string]string{"回滚文件已存在": exists, "目录不存在": filepath.Join(t.TempDir(), "no", "rb.sql")} {
		if err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--apply", "--rollback-file", path}, &strings.Builder{}); err == nil {
			t.Fatalf("%s：应返回错误", name)
		}
		unchanged(name)
	}
	if b, _ := os.ReadFile(exists); string(b) != "旧内容" {
		t.Error("不应覆盖已有文件")
	}
	// 未指定回滚文件。
	if err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--apply"}, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "--rollback-file") {
		t.Fatalf("--apply 应要求 --rollback-file: %v", err)
	}
	unchanged("未指定回滚文件")
	for _, args := range [][]string{{"--apply", "--rollback-file"}, {"--apply", "--rollback-file", ""}} {
		if err := e.app.XCat(ctx, append([]string{"repairDisconnectIP"}, args...), &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "需要文件路径") {
			t.Fatalf("%v 应提示需要文件路径: %v", args, err)
		}
	}
	unchanged("回滚文件路径为空")
	// 输出（影响清单）写入失败。
	if err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--apply", "--rollback-file", filepath.Join(t.TempDir(), "x.sql")}, failWriter{}); err == nil {
		t.Fatal("输出失败应返回错误")
	}
	unchanged("输出失败")

	// 成功：回滚文件完整、权限 600，执行后恢复原值。
	path := filepath.Join(t.TempDir(), "ok.sql")
	if err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--apply", "--rollback-file", path}, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if disconnectOf(t, e, users[0]) != "198.51.100.1" || disconnectOf(t, e, users[1]) != "198.51.100.2" {
		t.Fatal("修复后应只保留合法项")
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("回滚文件权限应为 600: %v", err)
	}
	b, _ := os.ReadFile(path)
	if n := runRollback(t, e, string(b)); n != 2 {
		t.Fatalf("回滚语句数: %d", n)
	}
	unchanged("回滚后")
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("输出失败（测试）") }

// TestDisconnectRepairConcurrentNewBan 检查与修复期间按每分钟任务的顺序（先写记录、再写字段）新增有效封禁时，
// --orphans 不会把它当作没有记录的残留清除。（第三方 P2-2）
func TestDisconnectRepairConcurrentNewBan(t *testing.T) {
	addBan := func(e *testEnv, uid uint32) {
		e.exec("INSERT INTO disconnect_ip (userid, ip, datetime) VALUES (?, '198.51.100.77', UNIX_TIMESTAMP())", uid)
		e.exec("UPDATE user SET disconnect_ip = '198.51.100.77' WHERE id = ?", uid)
	}
	ctx := context.Background()

	// 一：检查读完记录表之后、读取用户字段之前新增（第三方复现）。检查使用同一快照，看不到新增的字段。
	e := newEnv(t)
	uid := e.login("ban1@example.com")
	e.app.disconnectHooks.afterAuditRecords = func() { addBan(e, uid) }
	if err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--orphans", "--apply", "--rollback-file", filepath.Join(t.TempDir(), "rb.sql")}, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if v := disconnectOf(t, e, uid); v != "198.51.100.77" {
		t.Fatalf("检查期间新增的有效封禁不应被清除: %q", v)
	}

	// 二：检查时字段已有该 IP 但还没有记录（字段先于记录的旧数据），修复前补上了记录：修复时重新读取记录，跳过。
	e2 := newEnv(t)
	uid2 := e2.login("ban2@example.com")
	e2.exec("UPDATE user SET disconnect_ip = '198.51.100.77' WHERE id = ?", uid2)
	e2.app.disconnectHooks.beforeApply = func() {
		e2.exec("INSERT INTO disconnect_ip (userid, ip, datetime) VALUES (?, '198.51.100.77', UNIX_TIMESTAMP())", uid2)
	}
	var out strings.Builder
	if err := e2.app.XCat(ctx, []string{"repairDisconnectIP", "--orphans", "--apply", "--rollback-file", filepath.Join(t.TempDir(), "rb.sql")}, &out); err != nil {
		t.Fatal(err)
	}
	if v := disconnectOf(t, e2, uid2); v != "198.51.100.77" || !strings.Contains(out.String(), "已修复 0 个用户；1 个用户在检查后有变化") {
		t.Fatalf("修复前补上记录的封禁应保留: %q\n%s", v, out.String())
	}
	// 没有并发变化时，--orphans 照常清除没有记录的项。
	e2.exec("DELETE FROM disconnect_ip WHERE userid = ?", uid2)
	e2.app.disconnectHooks.beforeApply = nil
	if err := e2.app.XCat(ctx, []string{"repairDisconnectIP", "--orphans", "--apply", "--rollback-file", filepath.Join(t.TempDir(), "rb2.sql")}, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if v := disconnectOf(t, e2, uid2); v != "" {
		t.Fatalf("没有记录的残留应清除: %q", v)
	}
}

// TestDisconnectRepairCompareRawBytes 条件更新与回滚语句按字节比较：全角 / 半角、大小写、尾随空格都视为不同。（第三方 P2-3）
func TestDisconnectRepairCompareRawBytes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.login("bytes@example.com")
	for _, c := range []struct{ field, old string }{
		{"1.2.3.4", "１.2.3.4"}, // 全角数字
		{"１.2.3.4", "1.2.3.4"},
		{"2001:DB8::1", "2001:db8::1"}, // 大小写
		{"1.2.3.4 ", "1.2.3.4"},        // 尾随空格
		{"1.2.3.4", "1.2.3.4 "},
		{"1.2.3.4", "1.2.3.4 "}, // 不换行空格
	} {
		e.exec("UPDATE user SET disconnect_ip = ? WHERE id = ?", c.field, uid)
		n, err := e.app.Q.ReplaceUserDisconnectIP(ctx, store.ReplaceUserDisconnectIPParams{NewValue: "changed", ID: uid, OldValue: []byte(c.old)})
		if err != nil || n != 0 || disconnectOf(t, e, uid) != c.field {
			t.Errorf("字段 %q 与 %q 不应视为相同: %d %v", c.field, c.old, n, err)
		}
	}
	e.exec("UPDATE user SET disconnect_ip = '1.2.3.4' WHERE id = ?", uid)
	if n, _ := e.app.Q.ReplaceUserDisconnectIP(ctx, store.ReplaceUserDisconnectIPParams{NewValue: "", ID: uid, OldValue: []byte("1.2.3.4")}); n != 1 {
		t.Error("完全相同时应更新")
	}

	// 第三方复现：检查时是全角的非法项，修复前被纠正为合法的 1.2.3.4：跳过，保留 1.2.3.4。
	e.exec("UPDATE user SET disconnect_ip = '１.2.3.4' WHERE id = ?", uid)
	e.app.disconnectHooks.beforeApply = func() { e.exec("UPDATE user SET disconnect_ip = '1.2.3.4' WHERE id = ?", uid) }
	path := filepath.Join(t.TempDir(), "rb.sql")
	var out strings.Builder
	if err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--apply", "--rollback-file", path}, &out); err != nil {
		t.Fatal(err)
	}
	if v := disconnectOf(t, e, uid); v != "1.2.3.4" || !strings.Contains(out.String(), "1 个用户在检查后有变化") {
		t.Fatalf("已纠正的合法 IP 不应被清空: %q\n%s", v, out.String())
	}
	// 回滚语句也不覆盖之后的变化：字段现在是 1.2.3.4（不是修复后的空值），执行回滚不改动。
	b, _ := os.ReadFile(path)
	runRollback(t, e, string(b))
	if v := disconnectOf(t, e, uid); v != "1.2.3.4" {
		t.Fatalf("回滚不应覆盖之后的变化: %q", v)
	}

	// 回滚按字节：修复后的值被改成大小写不同的写法时，回滚不生效。
	e.app.disconnectHooks.beforeApply = nil
	e.exec("UPDATE user SET disconnect_ip = '2001:db8::5,256.1.1.1' WHERE id = ?", uid)
	path2 := filepath.Join(t.TempDir(), "rb2.sql")
	if err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--apply", "--rollback-file", path2}, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	e.exec("UPDATE user SET disconnect_ip = '2001:DB8::5' WHERE id = ?", uid)
	b, _ = os.ReadFile(path2)
	runRollback(t, e, string(b))
	if v := disconnectOf(t, e, uid); v != "2001:DB8::5" {
		t.Fatalf("回滚应按字节比较，不覆盖大小写不同的新值: %q", v)
	}
	e.exec("UPDATE user SET disconnect_ip = '2001:db8::5' WHERE id = ?", uid)
	runRollback(t, e, string(b))
	if v := disconnectOf(t, e, uid); v != "2001:db8::5,256.1.1.1" {
		t.Fatalf("字段仍是修复后的值时回滚应恢复原值: %q", v)
	}
}

// TestDisconnectRepairRollbackOnlyCommitted 回滚文件只恢复本次修复实际提交了修改的用户：修复时跳过的用户、
// 事务失败时未提交的修改，即使其他操作之后把字段改成了与计划相同的值，执行回滚也不改动。（第三方复验 P2）
func TestDisconnectRepairRollbackOnlyCommitted(t *testing.T) {
	ctx := context.Background()
	repair := func(t *testing.T, e *testEnv) (string, string, error) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "rb.sql")
		var out strings.Builder
		err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--apply", "--rollback-file", path}, &out)
		b, _ := os.ReadFile(path)
		return string(b), out.String(), err
	}

	t.Run("全部跳过（第三方复现）", func(t *testing.T) {
		e, users := repairEnv(t)
		e.exec("UPDATE user SET disconnect_ip = '' WHERE id = ?", users[1]) // 只留一个需要修复的用户
		// 回滚文件保存后、修复事务开始前，另一个操作把字段清理成与计划相同的值。
		e.app.disconnectHooks.beforeApply = func() { e.exec("UPDATE user SET disconnect_ip = '198.51.100.1' WHERE id = ?", users[0]) }
		rb, out, err := repair(t, e)
		if err != nil || !strings.Contains(out, "已修复 0 个用户；1 个用户在检查后有变化") {
			t.Fatalf("应跳过: %v\n%s", err, out)
		}
		if n := runRollback(t, e, rb); n != 1 {
			t.Fatalf("回滚文件应有 1 条语句: %d", n)
		}
		if v := disconnectOf(t, e, users[0]); v != "198.51.100.1" {
			t.Fatalf("跳过的用户执行回滚后应不变: %q", v)
		}
	})

	t.Run("部分成功、部分跳过", func(t *testing.T) {
		e, users := repairEnv(t)
		e.app.disconnectHooks.beforeApply = func() { e.exec("UPDATE user SET disconnect_ip = '198.51.100.2' WHERE id = ?", users[1]) }
		rb, out, err := repair(t, e)
		if err != nil || !strings.Contains(out, "已修复 1 个用户；1 个用户在检查后有变化") {
			t.Fatalf("应修复 1 个、跳过 1 个: %v\n%s", err, out)
		}
		if disconnectOf(t, e, users[0]) != "198.51.100.1" {
			t.Fatal("第一个用户应已修复")
		}
		runRollback(t, e, rb)
		if v := disconnectOf(t, e, users[0]); v != "198.51.100.1,256.1.1.1" {
			t.Fatalf("修复了的用户应恢复原值: %q", v)
		}
		if v := disconnectOf(t, e, users[1]); v != "198.51.100.2" {
			t.Fatalf("跳过的用户应不变: %q", v)
		}
	})

	t.Run("事务失败", func(t *testing.T) {
		e, users := repairEnv(t)
		e.app.disconnectHooks.failAfter = 1 // 修改第一个用户后事务失败，全部回滚
		rb, out, err := repair(t, e)
		if err == nil || !strings.Contains(out, "修复失败，没有任何修改") {
			t.Fatalf("应返回事务失败: %v\n%s", err, out)
		}
		if disconnectOf(t, e, users[0]) != "198.51.100.1,256.1.1.1" || disconnectOf(t, e, users[1]) != "198.51.100.2,1.2.3" {
			t.Fatal("事务失败时不应有任何修改")
		}
		var n int
		_ = e.app.DB.QueryRow("SELECT COUNT(*) FROM disconnect_ip_repair").Scan(&n)
		if n != 0 {
			t.Fatalf("失败的事务不应留下修复记录: %d", n)
		}
		// 其他操作随后把两个用户都清理成与计划相同的值；执行这次的回滚文件不改动。
		e.exec("UPDATE user SET disconnect_ip = '198.51.100.1' WHERE id = ?", users[0])
		e.exec("UPDATE user SET disconnect_ip = '198.51.100.2' WHERE id = ?", users[1])
		if runRollback(t, e, rb) != 2 {
			t.Fatal("回滚文件应有 2 条语句")
		}
		if disconnectOf(t, e, users[0]) != "198.51.100.1" || disconnectOf(t, e, users[1]) != "198.51.100.2" {
			t.Fatal("事务失败的回滚文件不应覆盖其他操作写入的值")
		}
	})

	t.Run("两次修复的回滚互不影响", func(t *testing.T) {
		e, users := repairEnv(t)
		rb1, _, err := repair(t, e) // 修复两个用户
		if err != nil {
			t.Fatal(err)
		}
		// 第一个用户被恢复成原值后再修复一次（第二次的编号不同）；第一次的回滚文件只看第一次的记录。
		runRollback(t, e, rb1)
		rb2, _, err := repair(t, e)
		if err != nil {
			t.Fatal(err)
		}
		if disconnectOf(t, e, users[0]) != "198.51.100.1" || rb1 == rb2 {
			t.Fatal("第二次修复应成功且回滚文件不同")
		}
		runRollback(t, e, rb2)
		if disconnectOf(t, e, users[0]) != "198.51.100.1,256.1.1.1" || disconnectOf(t, e, users[1]) != "198.51.100.2,1.2.3" {
			t.Fatal("第二次的回滚文件应恢复第二次修复的用户")
		}
	})
}
