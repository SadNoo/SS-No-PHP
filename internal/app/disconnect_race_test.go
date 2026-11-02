package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// raceUser 准备一个限制同时在线 1 个 IP 的用户：字段为 field；在线 IP 为 1.1.1.1（先上线）与 2.2.2.2（后上线，应被限制）。
func raceUser(t *testing.T, e *testEnv, email, field string) uint32 {
	t.Helper()
	res, err := e.app.DB.Exec(`INSERT INTO user (user_name,email,pass,passwd,reg_date,im_value,pac,remark,forbidden_ip,forbidden_port,disconnect_ip,node_connector)
		VALUES (?,?,'x','p',UTC_TIMESTAMP(),'','','','','',?,1)`, email, email, field)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	uid := uint32(id) //nolint:gosec // 测试库中的自增 ID 很小
	e.exec("INSERT INTO alive_ip (nodeid,userid,ip,datetime) VALUES (61,?,'1.1.1.1',UNIX_TIMESTAMP()-30),(61,?,'2.2.2.2',UNIX_TIMESTAMP())", uid, uid)
	return uid
}

func raceEnv(t *testing.T) *testEnv {
	e := newEnv(t)
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
		(61,'n',1,?,'2022-blake3-aes-256-gcm','','',14,1,0,'127.0.0.1',0)`, "n.example.com;8443;"+fixtureKey)
	return e
}

// disconnectRecords 返回用户的限制记录 IP（排序）。
func disconnectRecords(t *testing.T, e *testEnv, uid uint32) []string {
	t.Helper()
	rows, err := e.app.DB.Query("SELECT ip FROM disconnect_ip WHERE userid = ? ORDER BY ip", uid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ips []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			t.Fatal(err)
		}
		ips = append(ips, ip)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ips
}

// sameExceptDisconnect 确认除 disconnect_ip 外的字段都没有变化。
func sameExceptDisconnect(t *testing.T, e *testEnv, uid uint32, before store.User) {
	t.Helper()
	after, err := e.app.Q.GetUser(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	after.DisconnectIp, before.DisconnectIp = "", ""
	if after != before {
		t.Errorf("用户 %d 的其他字段不应改变:\n%+v\n%+v", uid, before, after)
	}
}

// TestDisconnectConnectorRepairRace 线上现象（第三方 P2）：每分钟检查读取用户后，repairDisconnectIP --orphans 清理并提交，
// 检查随后新增一个正常限制。最终字段只能是新增的限制，清理掉的历史项不能被旧值写回；新增的限制有记录；其他字段不变。
func TestDisconnectConnectorRepairRace(t *testing.T) {
	e := raceEnv(t)
	ctx := context.Background()
	uid := raceUser(t, e, "race1@example.com", "198.51.100.9") // 没有记录的历史项
	before, _ := e.app.Q.GetUser(ctx, uid)
	ran := false
	e.app.disconnectHooks.afterConnectorUsers = func() {
		if ran {
			return
		}
		ran = true
		var out strings.Builder
		if err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--orphans", "--apply", "--rollback-file", filepath.Join(t.TempDir(), "rb.sql")}, &out); err != nil {
			t.Errorf("修复失败: %v\n%s", err, out.String())
		}
		if v := disconnectOf(t, e, uid); v != "" || !strings.Contains(out.String(), "已修复 1 个用户") {
			t.Errorf("修复应已提交并清空历史项: %q\n%s", v, out.String())
		}
	}
	if err := e.app.checkConnector(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("钩子没有执行")
	}
	if v := disconnectOf(t, e, uid); v != "2.2.2.2" {
		t.Fatalf("最终字段应只有新增的限制，历史项不能写回: %q", v)
	}
	if r := disconnectRecords(t, e, uid); !slices.Equal(r, []string{"2.2.2.2"}) {
		t.Fatalf("新增的限制应有且只有一条记录: %v", r)
	}
	sameExceptDisconnect(t, e, uid, before)
}

// TestDisconnectReleaseAddRace 到期解除与新增限制并发，两种先后顺序都不能互相覆盖：
//   - 检查读取用户后，到期解除先完成：已解除的项不能被检查的旧值写回；
//   - 到期解除处理中（已读取用户），检查新增限制：新增的限制不能被解除的旧值覆盖掉。
//
// 两种情况的最终状态都是：字段只有新增的 2.2.2.2，记录只有它一条，其他字段不变。
func TestDisconnectReleaseAddRace(t *testing.T) {
	e := raceEnv(t)
	ctx := context.Background()
	expired := func(uid uint32) {
		e.exec("INSERT INTO disconnect_ip (userid,ip,datetime) VALUES (?, '203.0.113.1', UNIX_TIMESTAMP() - 400)", uid)
	}
	check := func(name string, uid uint32, before store.User) {
		t.Helper()
		if v := disconnectOf(t, e, uid); v != "2.2.2.2" {
			t.Errorf("%s：最终字段应只有新增的限制: %q", name, v)
		}
		if r := disconnectRecords(t, e, uid); !slices.Equal(r, []string{"2.2.2.2"}) {
			t.Errorf("%s：记录应只有新增的限制: %v", name, r)
		}
		sameExceptDisconnect(t, e, uid, before)
	}

	// 一：检查读取用户后，到期解除先完成。
	a := raceUser(t, e, "race2@example.com", "203.0.113.1")
	expired(a)
	beforeA, _ := e.app.Q.GetUser(ctx, a)
	ran := false
	e.app.disconnectHooks.afterConnectorUsers = func() {
		if ran {
			return
		}
		ran = true
		if err := e.app.releaseDisconnects(ctx, time.Now()); err != nil {
			t.Errorf("到期解除失败: %v", err)
		}
		if v := disconnectOf(t, e, a); v != "" {
			t.Errorf("到期解除应已提交: %q", v)
		}
	}
	if err := e.app.checkConnector(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	e.app.disconnectHooks.afterConnectorUsers = nil
	if !ran {
		t.Fatal("钩子没有执行")
	}
	check("检查读取后解除先完成", a, beforeA)

	// 二：到期解除处理中，检查新增限制。检查在另一个连接中运行；修复后它要等解除的事务结束（等待 1 秒确认被挡住）。
	e.exec("DELETE FROM alive_ip WHERE userid = ?", a)
	e.exec("DELETE FROM ip_online WHERE userid = ?", a)
	b := raceUser(t, e, "race3@example.com", "203.0.113.1")
	expired(b)
	beforeB, _ := e.app.Q.GetUser(ctx, b)
	var wg sync.WaitGroup
	var connErr error
	blocked := false
	e.app.disconnectHooks.releaseLocked = func() {
		if e.app.disconnectHooks.releaseLocked == nil {
			return
		}
		e.app.disconnectHooks.releaseLocked = nil
		done := make(chan struct{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(done)
			connErr = e.app.checkConnector(ctx, time.Now())
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			blocked = true
		}
	}
	if err := e.app.releaseDisconnects(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if connErr != nil {
		t.Fatal(connErr)
	}
	if !blocked {
		t.Error("到期解除处理该用户期间，检查应等待（同一用户的写入按用户行锁串行）")
	}
	check("解除处理中新增限制", b, beforeB)
}

// TestDisconnectWritersConcurrent 每分钟检查、到期解除、修复命令同时反复运行（线上检查与解除在同一个命名锁下依次执行，
// 不会同时运行；这里让它们同时运行，确认即使重叠也不会互相覆盖）：不出错（包括死锁）、不产生重复记录，
// 结束后每个用户的字段与记录一致（字段中的每一项都有记录，每条记录都在字段中）。
func TestDisconnectWritersConcurrent(t *testing.T) {
	e := raceEnv(t)
	ctx := context.Background()
	var users []uint32
	for i := range 6 {
		users = append(users, raceUser(t, e, fmt.Sprintf("racec%d@example.com", i), ""))
		e.exec("INSERT INTO alive_ip (nodeid,userid,ip,datetime) VALUES (61,?,?,UNIX_TIMESTAMP())", users[i], fmt.Sprintf("3.3.3.%d", i+1))
	}
	stop := time.Now().Add(3 * time.Second)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	run := func(name string, fn func(i int) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; time.Now().Before(stop); i++ {
				if err := fn(i); err != nil {
					errs <- fmt.Errorf("%s: %w", name, err)
					return
				}
			}
		}()
	}
	dir := t.TempDir()
	run("检查", func(int) error { return e.app.checkConnector(ctx, time.Now()) })
	// 到期解除按 6 分钟后计算，刚新增的限制也会被解除，让新增与解除反复交替。
	run("解除", func(int) error { return e.app.releaseDisconnects(ctx, time.Now().Add(6*time.Minute)) })
	run("修复", func(i int) error {
		var out strings.Builder
		return e.app.XCat(ctx, []string{"repairDisconnectIP", "--orphans", "--apply", "--rollback-file", filepath.Join(dir, fmt.Sprintf("rb%d.sql", i))}, &out)
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var dup int
	if err := e.app.DB.QueryRow("SELECT COUNT(*) FROM (SELECT userid, ip FROM disconnect_ip GROUP BY userid, ip HAVING COUNT(*) > 1) d").Scan(&dup); err != nil || dup != 0 {
		t.Errorf("不应有重复的限制记录: %d %v", dup, err)
	}
	for _, uid := range users {
		field := strings.Split(disconnectOf(t, e, uid), ",")
		if field[0] == "" {
			field = nil
		}
		slices.Sort(field)
		if r := disconnectRecords(t, e, uid); !slices.Equal(field, r) {
			t.Errorf("用户 %d 的字段与记录不一致: 字段 %v，记录 %v", uid, field, r)
		}
	}
}

// TestDisconnectRepairConnectorGapLock 第三方 P2（复验）：记录表为空、多个用户有历史残留时，修复事务处理完第一个用户后
// （已读取过不存在的记录），每分钟检查为排在后面的用户 B 新增限制。原先在 REPEATABLE READ 下修复事务的间隙锁挡住 B 的插入，
// 修复事务随后又要等 B 的用户行锁，形成死锁。要求：两边都不出错；检查不被修复事务挡住；B 的新限制建立且有记录；
// 修复提交其余用户、跳过检查后有变化的 B；回滚只作用于实际修改了的用户；再次修复后所有用户的字段与记录一致。
func TestDisconnectRepairConnectorGapLock(t *testing.T) {
	e := raceEnv(t)
	ctx := context.Background()
	var others []uint32
	for i := range 100 {
		uid := raceUser(t, e, fmt.Sprintf("gap%d@example.com", i), fmt.Sprintf("198.51.100.%d", i+1))
		e.exec("UPDATE user SET node_connector = 0 WHERE id = ?", uid)
		others = append(others, uid)
	}
	e.exec("DELETE FROM alive_ip")
	b := raceUser(t, e, "gapb@example.com", "203.0.113.200") // 排在最后，有历史残留，在线 IP 超限
	beforeB, _ := e.app.Q.GetUser(ctx, b)
	logs := captureLog(e)

	var connErr error
	waited := false
	finished := make(chan struct{})
	e.app.disconnectHooks.afterRepairUser = func(done int) {
		if done != 1 {
			return
		}
		go func() {
			defer close(finished)
			connErr = e.app.checkConnector(ctx, time.Now())
		}()
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			waited = true // 被挡住：让修复继续（随后去锁 B 的用户行，原先在这里形成死锁）
		}
	}
	rb := filepath.Join(t.TempDir(), "rb.sql")
	var out strings.Builder
	repairErr := e.app.XCat(ctx, []string{"repairDisconnectIP", "--orphans", "--apply", "--rollback-file", rb}, &out)
	e.app.disconnectHooks.afterRepairUser = nil
	<-finished
	if repairErr != nil || connErr != nil {
		t.Fatalf("修复与检查都不应出错: repair=%v connector=%v\n%s", repairErr, connErr, out.String())
	}
	if waited {
		t.Error("检查不应被修复事务挡住（修复事务不应锁住其他用户的记录间隙）")
	}
	if strings.Contains(logs.String(), "死锁") {
		t.Errorf("不应依靠死锁重试（应根本不发生死锁）:\n%s", logs.String())
	}
	if !strings.Contains(out.String(), "已修复 100 个用户；1 个用户在检查后有变化，已跳过") {
		t.Fatalf("修复应提交 100 个用户、跳过 B:\n%s", out.String())
	}
	for _, uid := range others {
		if v := disconnectOf(t, e, uid); v != "" {
			t.Fatalf("用户 %d 应已清理: %q", uid, v)
		}
	}
	if v, r := disconnectOf(t, e, b), disconnectRecords(t, e, b); v != "203.0.113.200,2.2.2.2" || !slices.Equal(r, []string{"2.2.2.2"}) {
		t.Fatalf("B 的新限制应建立，历史项留待下次修复: 字段 %q 记录 %v", v, r)
	}
	// 回滚只恢复实际修改了的 100 个用户，跳过的 B 不受影响。
	rbText, _ := os.ReadFile(rb)
	runRollback(t, e, string(rbText))
	if v := disconnectOf(t, e, others[0]); v != "198.51.100.1" {
		t.Fatalf("回滚应恢复已修改的用户: %q", v)
	}
	if v := disconnectOf(t, e, b); v != "203.0.113.200,2.2.2.2" {
		t.Fatalf("回滚不应改动跳过的 B: %q", v)
	}
	// 再次修复：全部清理，B 只剩有记录的限制；字段与记录一致，其他字段不变。
	out.Reset()
	if err := e.app.XCat(ctx, []string{"repairDisconnectIP", "--orphans", "--apply", "--rollback-file", filepath.Join(t.TempDir(), "rb2.sql")}, &out); err != nil {
		t.Fatal(err)
	}
	if v := disconnectOf(t, e, b); v != "2.2.2.2" {
		t.Fatalf("再次修复后 B 应只剩有记录的限制: %q", v)
	}
	sameExceptDisconnect(t, e, b, beforeB)
	for _, uid := range append(others, b) {
		field := strings.Split(disconnectOf(t, e, uid), ",")
		if field[0] == "" {
			field = nil
		}
		if r := disconnectRecords(t, e, uid); !slices.Equal(field, r) {
			t.Errorf("用户 %d 的字段与记录不一致: 字段 %v，记录 %v", uid, field, r)
		}
	}
}

// TestDisconnectTxDeadlockRetry 死锁（1213）时回滚并重新执行整个事务，最多 disconnectTxAttempts 次；
// 用尽后返回死锁错误且没有任何提交；其他错误不重试。
func TestDisconnectTxDeadlockRetry(t *testing.T) {
	e := raceEnv(t)
	ctx := context.Background()
	uid := raceUser(t, e, "retry@example.com", "")
	deadlock := &mysql.MySQLError{Number: 1213, Message: "Deadlock found when trying to get lock（测试）"}
	write := func(q *store.Queries, v string) error {
		return q.UpdateUserDisconnectIP(ctx, store.UpdateUserDisconnectIPParams{DisconnectIp: v, ID: uid})
	}

	calls := 0
	err := e.app.disconnectTx(ctx, func(q *store.Queries) error {
		calls++
		if err := write(q, fmt.Sprintf("192.0.2.%d", calls)); err != nil {
			return err
		}
		if calls == 1 {
			return deadlock
		}
		return nil
	})
	if err != nil || calls != 2 || disconnectOf(t, e, uid) != "192.0.2.2" {
		t.Fatalf("死锁后应重试并提交第二次的结果: err=%v calls=%d 字段=%q", err, calls, disconnectOf(t, e, uid))
	}

	calls = 0
	err = e.app.disconnectTx(ctx, func(q *store.Queries) error {
		calls++
		if err := write(q, "198.51.100.77"); err != nil {
			return err
		}
		return fmt.Errorf("包装: %w", deadlock)
	})
	if !isDeadlock(err) || calls != disconnectTxAttempts || disconnectOf(t, e, uid) != "192.0.2.2" {
		t.Fatalf("一直死锁时应重试 %d 次后返回死锁错误、没有提交: err=%v calls=%d 字段=%q", disconnectTxAttempts, err, calls, disconnectOf(t, e, uid))
	}

	calls = 0
	other := errors.New("其他错误（测试）")
	if err := e.app.disconnectTx(ctx, func(*store.Queries) error { calls++; return other }); !errors.Is(err, other) || calls != 1 {
		t.Fatalf("其他错误不应重试: err=%v calls=%d", err, calls)
	}
}
