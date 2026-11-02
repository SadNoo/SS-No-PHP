package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/blockedip"
	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// disconnectRepairHooks 只在测试中设置，用于在固定时机模拟并发写入。
type disconnectRepairHooks struct {
	afterAuditRecords   func()         // 检查读完 disconnect_ip 表之后
	beforeApply         func()         // 回滚文件保存之后、修复事务开始之前
	failAfter           int            // 大于 0 时，修改第 failAfter 个用户后让修复事务失败（模拟事务失败）
	afterConnectorUsers func()         // 每分钟检查读取用户列表之后、处理各用户之前
	releaseLocked       func()         // 到期解除：锁住用户行与记录之后、写入用户字段之前
	afterRepairUser     func(done int) // 修复事务中每修改完一个用户之后（事务尚未提交）
}

// checkDisconnectIP 检查全库 disconnect_ip（只读）：非法项、没有对应记录的合法项（不会按时自动解除）、写法不规范。
func (a *App) checkDisconnectIP(ctx context.Context, out io.Writer) error {
	r, err := blockedip.Audit(ctx, a.DB, nil)
	if err != nil {
		return err
	}
	w := &errWriter{w: out}
	r.Write(w, "")
	if len(r.Issues) > 0 {
		w.printf("节点接口只下发合法项。需要修复时先备份数据库，再运行 panel xcat repairDisconnectIP 预览。\n")
	}
	return w.err
}

type disconnectChange struct {
	id       uint32
	old, new string
}

// repairDisconnectIP 修复 disconnect_ip：只保留合法项（标准写法、去重）；--orphans 时也去掉没有对应记录的合法项
// （历史残留，不会按时自动解除）。不带 --apply 时只输出影响清单。
//
// --apply 必须同时指定 --rollback-file：先把全部回滚语句写入这个新文件（不覆盖已有文件），写入、刷盘与关闭都成功后
// 才修改数据库；任何一步失败都不做修改并返回错误。
//
// 每次修复有一个随机编号；实际修改了的用户在同一个事务中记入 disconnect_ip_repair 表。回滚语句只恢复
// 有本次编号记录（即本次确实提交了修改）且字段（按字节）仍是修复后值的用户：修复时跳过的用户、事务失败时
// 未提交的修改都没有记录，执行回滚文件不会改动它们，也不会覆盖其他操作写入的值。
//
// 修复在一个事务中进行（加锁顺序与每分钟检查、到期解除相同：先用户行，再该用户的 disconnect_ip 表记录）：逐个锁住用户行，按字节确认字段仍是检查时的原文，并重新读取该用户当前的 disconnect_ip 表记录
// （加锁读取，修复期间该用户不能新增记录），按同样的规则重新计算；结果与检查时不同（期间新增了封禁或记录）就跳过。
// 只修改 disconnect_ip，不删除用户、不改变其他字段与连接凭据，也不改动 disconnect_ip 表。
func (a *App) repairDisconnectIP(ctx context.Context, args []string, out io.Writer) error {
	apply, orphans, rollback := false, false, ""
	usage := "用法: panel xcat repairDisconnectIP [--orphans] [--apply --rollback-file 回滚文件]"
	wantFile := false
	for _, arg := range args {
		switch {
		case wantFile:
			rollback, wantFile = arg, false
		case arg == "--apply":
			apply = true
		case arg == "--orphans":
			orphans = true
		case arg == "--rollback-file":
			wantFile = true
		default:
			return fmt.Errorf("未知参数 %q，%s", arg, usage)
		}
	}
	if wantFile || (rollback == "" && slices.Contains(args, "--rollback-file")) {
		return errors.New("--rollback-file 需要文件路径，" + usage)
	}
	if apply && rollback == "" {
		return errors.New("--apply 需要同时用 --rollback-file 指定回滚文件（新文件，修改前写入并刷盘），" + usage)
	}
	r, err := blockedip.Audit(ctx, a.DB, a.disconnectHooks.afterAuditRecords)
	if err != nil {
		return err
	}
	w := &errWriter{w: out}
	r.Write(w, "")
	var changes []disconnectChange
	for _, is := range r.Issues {
		if v := is.Repaired(orphans); v != is.Raw {
			changes = append(changes, disconnectChange{is.ID, is.Raw, v})
		}
	}
	mode := "只去掉非法项（并统一合法项的写法）；没有对应记录的合法项保留，需要时加 --orphans"
	if orphans {
		mode = "去掉非法项与没有对应记录的合法项（并统一合法项的写法）"
	}
	w.printf("修复方式：%s\n需要修改的用户：%d 个\n", mode, len(changes))
	for _, c := range changes {
		w.printf("  用户 %d：%d 项 → %d 项\n", c.id, countItems(c.old), countItems(c.new))
	}
	if w.err != nil {
		return w.err
	}
	if !apply || len(changes) == 0 {
		if len(changes) > 0 {
			w.printf("以上为预览。确认后先备份数据库，再加 --apply --rollback-file 回滚文件 执行\n")
		}
		return w.err
	}
	repairID, err := newRepairID()
	if err != nil {
		return err
	}
	if err := writeRollbackFile(rollback, repairID, changes); err != nil {
		return fmt.Errorf("保存回滚文件失败，没有修改数据库: %w", err)
	}
	w.printf("回滚文件已保存：%s\n", rollback)
	if a.disconnectHooks.beforeApply != nil {
		a.disconnectHooks.beforeApply()
	}
	done, skipped := 0, 0
	now := time.Now().Unix()
	err = a.disconnectTx(ctx, func(q *store.Queries) error { // 死锁重试时整个事务（全部用户）重新执行
		done, skipped = 0, 0
		for _, c := range changes {
			ok, err := repairOneDisconnect(ctx, q, c, orphans)
			if err != nil {
				return err
			}
			if !ok {
				skipped++
				continue
			}
			if err := q.InsertDisconnectRepair(ctx, store.InsertDisconnectRepairParams{RepairID: repairID, Userid: c.id, CreatedAt: now}); err != nil {
				return err
			}
			done++
			if h := a.disconnectHooks.afterRepairUser; h != nil {
				h(done)
			}
			if a.disconnectHooks.failAfter > 0 && done == a.disconnectHooks.failAfter {
				return errors.New("模拟修复事务失败（测试）")
			}
		}
		return nil
	})
	if err != nil {
		w.printf("修复失败，没有任何修改（回滚文件中的语句不会改动任何用户）：%v\n", err)
		return err
	}
	w.printf("修复编号 %s：已修复 %d 个用户；%d 个用户在检查后有变化，已跳过（可以再运行一次；回滚文件不会改动跳过的用户）\n", repairID, done, skipped)
	a.Log.Info("已修复用户的 disconnect_ip", "users", done, "skipped", skipped, "orphans", orphans)
	return w.err
}

// repairOneDisconnect 在修复事务中处理一个用户：锁住用户行，按字节确认字段未变，加锁读取当前记录后重新计算，
// 结果与检查时相同才按字节条件更新。返回是否已修改。
func repairOneDisconnect(ctx context.Context, q *store.Queries, c disconnectChange, orphans bool) (bool, error) {
	cur, err := q.GetUserDisconnectIPForUpdate(ctx, c.id)
	if isNoRows(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if cur != c.old { // Go 字符串按字节比较
		return false, nil
	}
	ips, err := q.ListUserDisconnectRecordsForShare(ctx, c.id)
	if err != nil {
		return false, err
	}
	records := map[string]bool{}
	for _, ip := range ips {
		records[blockedip.RecordKey(ip)] = true
	}
	if blockedip.Inspect(c.id, cur, records).Repaired(orphans) != c.new {
		return false, nil // 检查后新增了封禁记录等，结果不同
	}
	n, err := q.ReplaceUserDisconnectIP(ctx, store.ReplaceUserDisconnectIPParams{NewValue: c.new, ID: c.id, OldValue: []byte(c.old)})
	return n == 1, err
}

// rollbackFile 是回滚文件需要的操作（测试替换为会在指定时机失败的实现）。
type rollbackFile interface {
	io.Writer
	Sync() error
	Close() error
}

// createRollbackFile 创建回滚文件（已存在则失败）。
var createRollbackFile = func(path string) (rollbackFile, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // 路径由站长在命令行指定
}

// newRepairID 生成修复编号（32 位十六进制）。
func newRepairID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writeRollbackFile 把回滚语句写入新文件（已存在则失败），写入、刷盘、关闭并刷新所在目录，任何一步失败都返回错误
// 并删除不完整的文件。每条语句只在本次修复确实修改了该用户（disconnect_ip_repair 中有本次编号的记录），
// 并且字段（按字节）仍是修复后的值时恢复原值。
func writeRollbackFile(path, repairID string, changes []disconnectChange) (err error) {
	var buf bytes.Buffer
	buf.WriteString("-- panel xcat repairDisconnectIP 回滚语句（修复编号 " + repairID + "）：在 niuniupanel 库中执行本文件即可恢复。\n")
	buf.WriteString("-- 每条语句只恢复本次修复实际修改了的用户，且 disconnect_ip（按字节）仍是修复后的值；跳过的用户、失败的修复不受影响。\n")
	for _, c := range changes {
		fmt.Fprintf(&buf, "UPDATE `user` SET disconnect_ip = %s WHERE id = %d AND CAST(disconnect_ip AS BINARY) = %s "+
			"AND EXISTS (SELECT 1 FROM disconnect_ip_repair WHERE repair_id = '%s' AND userid = %d);\n",
			hexLiteral(c.old), c.id, hexLiteral(c.new), repairID, c.id)
	}
	f, err := createRollbackFile(path)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(path)
		}
	}()
	// 逐条写入并检查每一次写入（包括部分写入），任何一条失败都停止。
	for _, line := range bytes.SplitAfter(buf.Bytes(), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var n int
		if n, err = f.Write(line); err == nil && n < len(line) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return err
		}
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

// errWriter 记录第一个写入错误，之后的写入跳过。
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	e.err = err
	return n, err
}

func (e *errWriter) printf(format string, args ...any) { _, _ = fmt.Fprintf(e, format, args...) }

func countItems(list string) int {
	n := 0
	for _, s := range strings.Split(list, ",") {
		if strings.TrimSpace(s) != "" {
			n++
		}
	}
	return n
}

// hexLiteral 把任意字符串写成 MySQL 十六进制字面量，原文中的引号、反斜杠、换行等都不需要转义。
func hexLiteral(s string) string {
	return "X'" + hex.EncodeToString([]byte(s)) + "'"
}
