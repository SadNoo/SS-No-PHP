package store

// 批量写入（手写，sqlc 不支持 MySQL 多行语句）。节点一次上报成百上千个用户时，
// 逐条执行每个用户都要一次往返；这里每 bulkChunk 行合并为一条语句。

import (
	"cmp"
	"context"
	"slices"
	"strings"
)

// bulkChunk 是每条批量语句包含的行数，参数个数与语句长度都远低于 MySQL 的限制。
const bulkChunk = 500

// UserTrafficDelta 是一个用户本次要累加的流量（已乘倍率）。
type UserTrafficDelta struct {
	ID uint32
	U  int64
	D  int64
}

// AddUsersTraffic 原子累加多个用户的已用流量，并把最后使用时间设为 t（与 AddUserTrafficRows 相同，K01）。
//
// 加锁顺序：多个节点同时上报、包含相同用户时，每个事务都必须按用户 ID 升序加锁，否则会互相死锁。
// 只加 ORDER BY id 不够：分块较大时优化器可能选择全索引扫描，REPEATABLE READ 下扫描到的每一行都会被锁住，
// 后面的分块会回头锁住 ID 更小的行（第三方验收复现）。因此每个分块：
//   - FORCE INDEX (PRIMARY)：按主键逐个定位，只锁要更新的行；
//   - id BETWEEN 本块最小 AND 最大：即使执行计划变化，也只能在本块范围内扫描加锁。
//
// 各分块按 ID 升序且范围不重叠，整个事务因此按 ID 升序加锁。rows 中的用户 ID 不能重复。
func (q *Queries) AddUsersTraffic(ctx context.Context, t int64, rows []UserTrafficDelta) error {
	rows = slices.Clone(rows)
	slices.SortFunc(rows, func(a, b UserTrafficDelta) int { return cmp.Compare(a.ID, b.ID) })
	for start := 0; start < len(rows); start += bulkChunk {
		part := rows[start:min(start+bulkChunk, len(rows))]
		var sb strings.Builder
		args := make([]any, 0, len(part)*5+3)
		sb.WriteString("UPDATE `user` FORCE INDEX (PRIMARY) SET u = u + CASE id")
		for _, r := range part {
			sb.WriteString(" WHEN ? THEN ?")
			args = append(args, r.ID, r.U)
		}
		sb.WriteString(" END, d = d + CASE id")
		for _, r := range part {
			sb.WriteString(" WHEN ? THEN ?")
			args = append(args, r.ID, r.D)
		}
		sb.WriteString(" END, t = ? WHERE id BETWEEN ? AND ? AND id IN (")
		args = append(args, t, part[0].ID, part[len(part)-1].ID)
		for i, r := range part {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteByte('?')
			args = append(args, r.ID)
		}
		sb.WriteString(") ORDER BY id")
		if _, err := q.db.ExecContext(ctx, sb.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

// InsertTrafficLogs 写入多条流量记录（与 InsertTrafficLog 相同的字段）。
func (q *Queries) InsertTrafficLogs(ctx context.Context, rows []InsertTrafficLogParams) error {
	return insertRows(ctx, q.db, "INSERT INTO user_traffic_log (user_id, u, d, node_id, rate, traffic, log_time) VALUES ", "(?,?,?,?,?,?,?)", len(rows), func(i int) []any {
		r := rows[i]
		return []any{r.UserID, r.U, r.D, r.NodeID, r.Rate, r.Traffic, r.LogTime}
	})
}

// InsertAliveIPs 写入多条在线 IP 记录（与 InsertAliveIP 相同的字段）。
func (q *Queries) InsertAliveIPs(ctx context.Context, rows []InsertAliveIPParams) error {
	return insertRows(ctx, q.db, "INSERT INTO alive_ip (nodeid, userid, ip, `datetime`) VALUES ", "(?,?,?,?)", len(rows), func(i int) []any {
		r := rows[i]
		return []any{r.Nodeid, r.Userid, r.Ip, r.Datetime}
	})
}

// insertRows 把 n 行按 bulkChunk 分组，每组执行一条多行 INSERT。
func insertRows(ctx context.Context, db DBTX, prefix, row string, n int, values func(int) []any) error {
	for start := 0; start < n; start += bulkChunk {
		end := min(start+bulkChunk, n)
		var sb strings.Builder
		sb.WriteString(prefix)
		var args []any
		for i := start; i < end; i++ {
			if i > start {
				sb.WriteByte(',')
			}
			sb.WriteString(row)
			args = append(args, values(i)...)
		}
		if _, err := db.ExecContext(ctx, sb.String(), args...); err != nil {
			return err
		}
	}
	return nil
}
