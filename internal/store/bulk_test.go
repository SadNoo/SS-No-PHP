package store_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/testdb"
)

// TestAddUsersTrafficLocksOnlyTargets 验证批量累加流量只锁要更新的用户行。
// 如果分块语句扫描并锁住范围外的行（全索引扫描时 REPEATABLE READ 会锁住扫描到的每一行），
// 后面的分块会回头锁住 ID 更小的行，多个节点同时上报时互相死锁（第三方验收复现）。
// 这里直接读取本事务持有的行锁，要求与目标用户集合完全一致，从而保证整个事务按 ID 升序加锁。
func TestAddUsersTrafficLocksOnlyTargets(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	digits := "(SELECT 0 d UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9)"
	if _, err := pool.ExecContext(ctx, `INSERT INTO user (user_name,email,pass,passwd,reg_date,im_value,pac,remark,forbidden_ip,forbidden_port,disconnect_ip,class_expire,expire_in,enable,port)
		SELECT CONCAT('l',n), CONCAT('l',n,'@example.com'),'x','p',UTC_TIMESTAMP(),'','','','','','','1989-06-04 00:05:00','2099-06-04 00:05:00',1,20000+n
		FROM (SELECT a.d*1000+b.d*100+c.d*10+e.d AS n FROM `+digits+` a, `+digits+` b, `+digits+` c, `+digits+` e) s WHERE n < 1505`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(ctx, "ANALYZE TABLE user"); err != nil { // 按真实行数选择执行计划
		t.Fatal(err)
	}
	var all []uint32
	rows, err := pool.QueryContext(ctx, "SELECT id FROM user ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		all = append(all, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()

	cases := map[string][]uint32{
		"连续 1105 人（跨 3 个分块）":     all[341 : 341+1105],
		"每隔 3 人（ID 稀疏，分块范围覆盖全表）": {},
	}
	for i := 0; i < len(all); i += 3 {
		cases["每隔 3 人（ID 稀疏，分块范围覆盖全表）"] = append(cases["每隔 3 人（ID 稀疏，分块范围覆盖全表）"], all[i])
	}
	for name, ids := range cases {
		t.Run(name, func(t *testing.T) {
			tx, err := pool.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			var deltas []store.UserTrafficDelta
			for k := len(ids) - 1; k >= 0; k-- { // 逆序传入，函数内部排序
				deltas = append(deltas, store.UserTrafficDelta{ID: ids[k], U: 1, D: 3})
			}
			if err := store.New(pool).WithTx(tx).AddUsersTraffic(ctx, 1, deltas); err != nil {
				t.Fatal(err)
			}
			lr, err := tx.QueryContext(ctx, `SELECT LOCK_DATA FROM performance_schema.data_locks
				WHERE OBJECT_SCHEMA = DATABASE() AND OBJECT_NAME = 'user' AND LOCK_TYPE = 'RECORD' AND LOCK_DATA <> 'supremum pseudo-record'
				  AND THREAD_ID = PS_CURRENT_THREAD_ID()`) // 本连接（本事务）持有的锁；不用 information_schema.innodb_trx，它有约 100 ms 的缓存
			if err != nil {
				t.Fatal(err)
			}
			var locked []uint32
			for lr.Next() {
				var id uint32
				if err := lr.Scan(&id); err != nil {
					t.Fatal(err)
				}
				locked = append(locked, id)
			}
			if err := lr.Err(); err != nil {
				t.Fatal(err)
			}
			_ = lr.Close()
			slices.Sort(locked)
			locked = slices.Compact(locked)
			want := slices.Clone(ids)
			slices.Sort(want)
			if !slices.Equal(locked, want) {
				t.Fatalf("应只锁 %d 个目标用户，实际锁住 %d 行（%v）", len(want), len(locked), span(locked))
			}
			var u, d int64
			if err := tx.QueryRowContext(ctx, "SELECT SUM(u), SUM(d) FROM user").Scan(&u, &d); err != nil || u != int64(len(ids)) || d != int64(3*len(ids)) {
				t.Fatalf("累加结果不正确: u=%d d=%d %v", u, d, err)
			}
		})
	}
}

func span(ids []uint32) string {
	if len(ids) == 0 {
		return "无"
	}
	return fmt.Sprintf("%d～%d", ids[0], ids[len(ids)-1])
}
