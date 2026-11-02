// Package legacy 把 SSPanel-UIM 旧数据库中的数据导入新库（panel import-legacy）。
//
// 新库的表结构与原版基本一致（见 00002_schema.sql 文件头的差异说明），导入时逐表复制两边都有的列，并做以下转换：
//   - DATETIME：原版按业务时区（默认北京时间）写入，转换为 UTC；哨兵值 1989-06-04 00:05:00、2099-06-04 00:05:00 原样保留；
//     0000-00-00 等无效日期改为该列的默认值。
//   - NULL：新库列为 NOT NULL 时改为该列默认值，没有默认值时数字为 0、文本为空字符串。
//   - 主键（ID）原样保留，导入后用户的订阅链接、邀请链接、节点 ID 都不变。
package legacy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/blockedip"
)

// Importer 执行导入。
type Importer struct {
	Src      *sql.DB        // 旧库
	Dst      *sql.DB        // 新库（已执行表结构变更）
	Loc      *time.Location // 旧库 DATETIME 使用的时区
	SkipLogs bool           // 跳过日志类大表
	Out      io.Writer
	Batch    int // 每条 INSERT 的行数，默认 500
}

// logTables 是日志 / 临时记录类的表，数据量大且导入价值不高，可用 -skip-logs 跳过。
var logTables = []string{
	"user_traffic_log", "ss_node_info", "ss_node_online_log", "alive_ip", "login_ip", "speedtest",
	"detect_log", "blockip", "unblockip", "disconnect_ip", "email_verify", "telegram_session", "ss_password_reset",
}

// skipTables 是新版自有的表，不从旧库导入。
var skipTables = []string{"goose_db_version", "job_state", "sessions", "disconnect_ip_repair"}

// column 是新库中一列的信息。
type column struct {
	Name     string
	Type     string // information_schema DATA_TYPE
	Nullable bool
	Default  sql.NullString
}

func (c column) numeric() bool {
	switch c.Type {
	case "tinyint", "smallint", "mediumint", "int", "bigint", "decimal", "float", "double", "bit":
		return true
	}
	return false
}

// zero 返回 NULL 或无效值的替代值。
func (c column) zero() any {
	switch {
	case c.Default.Valid && !strings.HasPrefix(strings.ToUpper(c.Default.String), "CURRENT_TIMESTAMP"):
		return c.Default.String
	case c.numeric():
		return "0"
	case c.Type == "datetime" || c.Type == "timestamp":
		return "1970-01-01 00:00:01"
	default:
		return ""
	}
}

func columns(ctx context.Context, db *sql.DB) (map[string][]column, error) {
	rows, err := db.QueryContext(ctx, `SELECT TABLE_NAME, COLUMN_NAME, DATA_TYPE, IS_NULLABLE = 'YES', COLUMN_DEFAULT
		FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME, ORDINAL_POSITION`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]column{}
	for rows.Next() {
		var t string
		var c column
		if err := rows.Scan(&t, &c.Name, &c.Type, &c.Nullable, &c.Default); err != nil {
			return nil, err
		}
		out[strings.ToLower(t)] = append(out[strings.ToLower(t)], c)
	}
	return out, rows.Err()
}

// Run 执行导入。新库除 config 表外必须为空。
func (im *Importer) Run(ctx context.Context) error {
	if im.Batch <= 0 {
		im.Batch = 500
	}
	dst, err := columns(ctx, im.Dst)
	if err != nil {
		return fmt.Errorf("读取新库表结构: %w", err)
	}
	src, err := columns(ctx, im.Src)
	if err != nil {
		return fmt.Errorf("读取旧库表结构: %w", err)
	}
	if len(src) == 0 {
		return errors.New("旧库中没有任何表，请检查连接的数据库名")
	}
	tables := make([]string, 0, len(dst))
	for t := range dst {
		if !slices.Contains(skipTables, t) {
			tables = append(tables, t)
		}
	}
	slices.Sort(tables)
	for _, t := range tables {
		if t == "config" {
			continue
		}
		var n int64
		if err := im.Dst.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+t+"`").Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("新库的 %s 表已有 %d 行数据；导入只能在全新的数据库上进行（可删除数据库后重新创建）", t, n)
		}
	}
	for _, t := range tables {
		if im.SkipLogs && slices.Contains(logTables, t) {
			im.printf("%-20s 跳过（-skip-logs）\n", t)
			continue
		}
		sc, ok := src[t]
		if !ok {
			im.printf("%-20s 旧库中没有此表，跳过\n", t)
			continue
		}
		if err := im.copyTable(ctx, t, dst[t], sc); err != nil {
			return fmt.Errorf("导入 %s: %w", t, err)
		}
	}
	return im.reportDisconnectIP(ctx)
}

// reportDisconnectIP 检查导入后的 user.disconnect_ip（封禁 IP）并报告异常，不修改数据：
// 节点接口只下发合法项；修复由站长确认后用 panel xcat repairDisconnectIP 执行（带影响清单与回滚语句）。
func (im *Importer) reportDisconnectIP(ctx context.Context) error {
	r, err := blockedip.Audit(ctx, im.Dst, nil)
	if err != nil {
		return fmt.Errorf("检查封禁 IP（disconnect_ip）: %w", err)
	}
	if len(r.Issues) == 0 {
		im.printf("封禁 IP（disconnect_ip）检查：%d 个用户有记录，没有异常\n", r.Users)
		return nil
	}
	im.printf("\n注意：导入的封禁 IP（disconnect_ip）有异常（不影响导入结果，节点接口只下发合法项）：\n")
	r.Write(im.Out, "  ")
	if im.SkipLogs {
		im.printf("  使用了 -skip-logs，disconnect_ip 表没有导入，字段中的合法项都没有对应记录、不会按时自动解除。\n")
	}
	im.printf("  确认后先备份数据库，再用 panel xcat repairDisconnectIP 预览并修复（详见 docs/migration.md）。\n\n")
	return nil
}

func (im *Importer) printf(format string, args ...any) {
	if im.Out != nil {
		_, _ = fmt.Fprintf(im.Out, format, args...)
	}
}

func (im *Importer) copyTable(ctx context.Context, table string, dcols, scols []column) error {
	srcNames := map[string]bool{}
	for _, c := range scols {
		srcNames[strings.ToLower(c.Name)] = true
	}
	var cols []column
	var missing []string
	for _, c := range dcols {
		if srcNames[strings.ToLower(c.Name)] {
			cols = append(cols, c)
		} else {
			missing = append(missing, c.Name)
		}
	}
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = "`" + c.Name + "`"
	}
	rows, err := im.Src.QueryContext(ctx, "SELECT "+strings.Join(quoted, ", ")+" FROM `"+table+"`") //nolint:gosec // 表名、列名来自新库的 information_schema
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	insert := "INSERT INTO `" + table + "` (" + strings.Join(quoted, ", ") + ") VALUES "
	suffix := ""
	if table == "config" { // 新库已写入默认配置项，以旧库的值为准
		suffix = " ON DUPLICATE KEY UPDATE `value` = VALUES(`value`)"
	}
	placeholder := "(" + strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ") + ")"
	raw := make([]sql.RawBytes, len(cols))
	dest := make([]any, len(cols))
	for i := range raw {
		dest[i] = &raw[i]
	}
	var batch []any
	var count, converted int
	flush := func(n int) error {
		if n == 0 {
			return nil
		}
		q := insert + strings.TrimSuffix(strings.Repeat(placeholder+", ", n), ", ") + suffix //nolint:gosec // 同上，值都通过占位符传入
		if _, err := im.Dst.ExecContext(ctx, q, batch...); err != nil {
			return fmt.Errorf("第 %d～%d 行: %w", count-n+1, count, err)
		}
		batch = batch[:0]
		return nil
	}
	pending := 0
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		for i, c := range cols {
			v, changed := im.convert(c, raw[i])
			if changed {
				converted++
			}
			batch = append(batch, v)
		}
		count++
		pending++
		if pending == im.Batch {
			if err := flush(pending); err != nil {
				return err
			}
			pending = 0
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := flush(pending); err != nil {
		return err
	}
	note := ""
	if len(missing) > 0 {
		note = "；旧库没有的列使用默认值: " + strings.Join(missing, ", ")
	}
	im.printf("%-20s %d 行（时间转换 %d 处）%s\n", table, count, converted, note)
	return nil
}

var sentinels = []string{"1989-06-04 00:05:00", "2099-06-04 00:05:00"}

// convert 转换一个值，返回是否做了时区转换。
func (im *Importer) convert(c column, raw sql.RawBytes) (any, bool) {
	if raw == nil {
		if c.Nullable {
			return nil, false
		}
		return c.zero(), false
	}
	s := string(raw)
	if c.numeric() && strings.TrimSpace(s) == "" { // 原版部分数字列为 TEXT（如 user.node_speedlimit），可能为空
		return c.zero(), false
	}
	if c.Type != "datetime" {
		return s, false
	}
	// 连接开启 parseTime 时驱动返回 RFC 3339 文本（墙上时间不变），统一为 "2006-01-02 15:04:05"。
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		s = t.Format(time.DateTime)
	}
	base, _, _ := strings.Cut(s, ".")
	if slices.Contains(sentinels, base) {
		return base, false
	}
	t, err := time.ParseInLocation(time.DateTime, base, im.Loc)
	if err != nil || t.Year() < 1971 { // 0000-00-00 等无效日期
		return c.zero(), false
	}
	return t.UTC().Format(time.DateTime), true
}
