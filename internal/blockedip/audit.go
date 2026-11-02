package blockedip

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// UserIssue 是一个用户的 disconnect_ip 需要注意的情况。
type UserIssue struct {
	ID      uint32
	Raw     string   // 字段原文
	Valid   []string // 合法项（标准写法，去重）
	Invalid []string // 非法项原文
	Orphans []string // 合法但 disconnect_ip 表中没有对应记录的项：不会按时自动解除
	Changed bool     // 合法项的写法与原文不同（空白、大小写、重复、IPv4 映射地址等）
}

// Report 是全库 disconnect_ip 的检查结果。
type Report struct {
	Users        int // disconnect_ip 不为空的用户数
	Issues       []UserIssue
	InvalidItems int
	OrphanItems  int
	Records      int // disconnect_ip 表的记录数
	StaleRecords int // 记录对应的 IP 已不在用户字段中（到期时照常删除，无需处理）
}

// Audit 检查全库 user.disconnect_ip 与 disconnect_ip 表（只读）。两张表在同一个只读的可重复读事务中读取，
// 来自同一个快照：不会把检查期间新增（先写记录、再写字段）的封禁误判为没有对应记录。
// afterRecords 在读完 disconnect_ip 表之后调用（测试用于模拟并发写入，正式调用传 nil）。
func Audit(ctx context.Context, db *sql.DB, afterRecords func()) (Report, error) {
	var r Report
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return r, err
	}
	defer func() { _ = tx.Rollback() }()
	records := map[uint32]map[string]bool{}
	rows, err := tx.QueryContext(ctx, "SELECT userid, ip FROM disconnect_ip")
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var uid uint32
		var ip string
		if err := rows.Scan(&uid, &ip); err != nil {
			_ = rows.Close()
			return r, err
		}
		r.Records++
		if records[uid] == nil {
			records[uid] = map[string]bool{}
		}
		records[uid][RecordKey(ip)] = true
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return r, err
	}
	if afterRecords != nil {
		afterRecords()
	}

	inField := map[uint32]map[string]bool{}
	rows, err = tx.QueryContext(ctx, "SELECT id, disconnect_ip FROM `user` WHERE disconnect_ip <> '' ORDER BY id")
	if err != nil {
		return r, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var uid uint32
		var raw string
		if err := rows.Scan(&uid, &raw); err != nil {
			return r, err
		}
		r.Users++
		is := Inspect(uid, raw, records[uid])
		inField[uid] = map[string]bool{}
		for _, ip := range is.Valid {
			inField[uid][ip] = true
		}
		if len(is.Invalid) > 0 || len(is.Orphans) > 0 || is.Changed {
			r.InvalidItems += len(is.Invalid)
			r.OrphanItems += len(is.Orphans)
			r.Issues = append(r.Issues, is)
		}
	}
	if err := rows.Err(); err != nil {
		return r, err
	}
	for uid, ips := range records {
		for ip := range ips {
			if !inField[uid][ip] {
				r.StaleRecords++
			}
		}
	}
	return r, nil
}

// RecordKey 是 disconnect_ip 表记录用于比较的写法：合法 IP 用标准写法，其他原样。
func RecordKey(ip string) string {
	if n, ok := Parse(ip); ok {
		return n
	}
	return ip
}

// Inspect 检查一个用户的字段 raw；records 是该用户 disconnect_ip 表记录的 RecordKey 集合。
func Inspect(uid uint32, raw string, records map[string]bool) UserIssue {
	l := Split(raw)
	is := UserIssue{ID: uid, Raw: raw, Valid: l.Valid, Invalid: l.Invalid}
	for _, ip := range l.Valid {
		if !records[ip] {
			is.Orphans = append(is.Orphans, ip)
		}
	}
	is.Changed = len(l.Invalid) == 0 && strings.Join(l.Valid, ",") != raw
	return is
}

// Write 输出影响清单：每个用户的项数、非法项分类与长度（不输出 IP 原文）。每行以 prefix 开头。
func (r Report) Write(w io.Writer, prefix string) {
	p := func(format string, args ...any) { _, _ = fmt.Fprintf(w, prefix+format+"\n", args...) }
	p("disconnect_ip 不为空的用户：%d 个；disconnect_ip 表记录：%d 条（其中 %d 条对应的 IP 已不在用户字段中，到期时照常删除）",
		r.Users, r.Records, r.StaleRecords)
	p("非法项：%d 项；没有对应记录的合法项（不会按时自动解除）：%d 项；涉及用户：%d 个", r.InvalidItems, r.OrphanItems, len(r.Issues))
	for _, is := range r.Issues {
		kinds := map[string]int{}
		for _, s := range is.Invalid {
			kinds[Kind(s)]++
		}
		ks := make([]string, 0, len(kinds))
		for k, n := range kinds {
			ks = append(ks, k+"×"+strconv.Itoa(n))
		}
		sort.Strings(ks)
		lens := make([]string, 0, len(is.Invalid))
		for _, s := range is.Invalid {
			lens = append(lens, strconv.Itoa(len(s)))
		}
		line := fmt.Sprintf("用户 %d：合法 %d 项（其中没有对应记录 %d 项）、非法 %d 项", is.ID, len(is.Valid), len(is.Orphans), len(is.Invalid))
		if len(ks) > 0 {
			line += "（" + strings.Join(ks, "、") + "；长度 " + strings.Join(lens, ",") + "）"
		}
		if is.Changed {
			line += "；合法项写法不规范或有重复"
		}
		p("%s", line)
	}
}

// Repaired 返回修复后的字段：只保留合法项（标准写法、去重）；dropOrphans 时也去掉没有对应记录的项。
func (is UserIssue) Repaired(dropOrphans bool) string {
	keep := make([]string, 0, len(is.Valid))
	for _, ip := range is.Valid {
		if dropOrphans && slices.Contains(is.Orphans, ip) {
			continue
		}
		keep = append(keep, ip)
	}
	return strings.Join(keep, ",")
}
