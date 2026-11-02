// Package testdb 为集成测试提供独立的临时数据库。
//
// 需要环境变量 NIUNIU_TEST_MYSQL_DSN（go-sql-driver 格式、不带库名、账号需有建库权限），例如：
//
//	root:devroot@tcp(127.0.0.1:3306)/?tls=skip-verify
//
// 未设置时，依赖数据库的测试会被跳过。
package testdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/SadNoo/SS-No-PHP/internal/migrate"
)

// New 创建一个全新的数据库并执行全部表结构变更，测试结束时自动删除。
func New(t testing.TB) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NIUNIU_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 NIUNIU_TEST_MYSQL_DSN，跳过数据库集成测试")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("NIUNIU_TEST_MYSQL_DSN 格式错误: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "niuniu_test_" + hex.EncodeToString(b)

	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", name)); err != nil {
		t.Fatalf("创建测试数据库: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), fmt.Sprintf("DROP DATABASE `%s`", name))
		_ = admin.Close()
	})

	cfg.DBName = name
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Params = map[string]string{"time_zone": "'+00:00'"}
	pool, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	m, err := migrate.New(pool, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Up(ctx); err != nil {
		t.Fatalf("执行表结构变更: %v", err)
	}
	return pool
}

// Empty 创建一个空数据库（不执行表结构变更，会话使用服务器默认时区），用于模拟 Radius 等外部数据库。
func Empty(t testing.TB) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NIUNIU_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 NIUNIU_TEST_MYSQL_DSN，跳过数据库集成测试")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("NIUNIU_TEST_MYSQL_DSN 格式错误: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "niuniu_ext_" + hex.EncodeToString(b)
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(context.Background(), fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4", name)); err != nil {
		t.Fatalf("创建测试数据库: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), fmt.Sprintf("DROP DATABASE `%s`", name))
		_ = admin.Close()
	})
	cfg.DBName = name
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	pool, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}
