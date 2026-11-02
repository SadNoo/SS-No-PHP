package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/testdb"
)

func newUser(t *testing.T, q *store.Queries, email string, port int32) int64 {
	t.Helper()
	id, err := q.CreateUser(context.Background(), store.CreateUserParams{
		UserName: "u", Email: email, Pass: "x", Passwd: "p", Port: port,
		RegDate: time.Now().UTC(), RegIp: "127.0.0.1",
		ExpireIn: time.Date(2099, 6, 4, 0, 5, 0, 0, time.UTC), ClassExpire: time.Date(1989, 6, 4, 0, 5, 0, 0, time.UTC),
		Method: "rc4-md5", Theme: "material", Plan: "A", Enable: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestAddUserTrafficConcurrent 验证并发上报流量不会丢失（K01）。
func TestAddUserTrafficConcurrent(t *testing.T) {
	pool := testdb.New(t)
	q := store.New(pool)
	id := newUser(t, q, "a@example.com", 10001)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := q.AddUserTraffic(context.Background(), store.AddUserTrafficParams{U: 10, D: 5, T: 1, ID: uint32(id)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	u, err := q.GetUser(context.Background(), uint32(id))
	if err != nil {
		t.Fatal(err)
	}
	if u.U != 200 || u.D != 100 {
		t.Fatalf("流量累加错误: u=%d d=%d", u.U, u.D)
	}
}

func TestEmailUnique(t *testing.T) {
	pool := testdb.New(t)
	q := store.New(pool)
	newUser(t, q, "dup@example.com", 10001)
	_, err := q.CreateUser(context.Background(), store.CreateUserParams{
		UserName: "u", Email: "dup@example.com", Pass: "x", Passwd: "p", Port: 10002,
		RegDate: time.Now().UTC(), RegIp: "127.0.0.1", ExpireIn: time.Now(), ClassExpire: time.Now(),
		Method: "rc4-md5", Theme: "material", Plan: "A", Enable: 1,
	})
	if err == nil {
		t.Fatal("重复邮箱应当被唯一索引拒绝")
	}
}

// TestGetNodeByExactIP 验证节点 IP 精确匹配（K04）。
func TestGetNodeByExactIP(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	_, err := pool.ExecContext(ctx, "INSERT INTO ss_node (name, server, method, node_ip) VALUES ('a','a','m','1.2.3.45'), ('b','b','m','1.2.3.4,5.6.7.8')")
	if err != nil {
		t.Fatal(err)
	}
	q := store.New(pool)
	n, err := q.GetNodeByExactIP(ctx, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "b" {
		t.Fatalf("应匹配节点 b，实际匹配 %s", n.Name)
	}
	if _, err := q.GetNodeByExactIP(ctx, "1.2.3"); err == nil {
		t.Fatal("前缀不应匹配")
	}
}
