package migrate_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/migrate"
	"github.com/SadNoo/SS-No-PHP/internal/testdb"
)

// TestUpDownUp 确保每个变更都能回退并重新执行。
func TestUpDownUp(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	m, err := migrate.New(pool, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	status, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for range status {
		if err := m.Down(ctx); err != nil {
			t.Fatalf("回退失败: %v", err)
		}
	}
	if err := m.Up(ctx); err != nil {
		t.Fatalf("重新执行失败: %v", err)
	}
	if pending, err := m.Pending(ctx); err != nil || pending {
		t.Fatalf("执行后仍有待执行变更: %v %v", pending, err)
	}
}
