// Package migrate 管理表结构变更。
//
// 变更文件位于 db/migrations，编译时打包进程序。执行时先获取 MySQL 命名锁，
// 保证同一时间只有一个进程在执行变更；任何一个变更失败都会返回错误，调用方应停止启动。
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/SadNoo/SS-No-PHP/db"
	idb "github.com/SadNoo/SS-No-PHP/internal/db"
)

const lockName = "niuniupanel:migrate"

// Runner 执行表结构变更。
type Runner struct {
	pool          *sql.DB
	provider      *goose.Provider
	backupCommand string
	log           *slog.Logger
}

// New 创建 Runner。backupCommand 不为空时，会在执行待执行变更之前运行。
func New(pool *sql.DB, backupCommand string, log *slog.Logger) (*Runner, error) {
	migrations, err := fs.Sub(db.Migrations, "migrations")
	if err != nil {
		return nil, err
	}
	p, err := goose.NewProvider(goose.DialectMySQL, pool, migrations)
	if err != nil {
		return nil, fmt.Errorf("初始化表结构变更: %w", err)
	}
	return &Runner{pool: pool, provider: p, backupCommand: backupCommand, log: log}, nil
}

// Status 返回每个变更的执行状态。
func (r *Runner) Status(ctx context.Context) ([]*goose.MigrationStatus, error) {
	return r.provider.Status(ctx)
}

// Pending 返回是否有待执行的变更。
func (r *Runner) Pending(ctx context.Context) (bool, error) {
	return r.provider.HasPending(ctx)
}

// Up 执行全部待执行的变更。
func (r *Runner) Up(ctx context.Context) error {
	return r.locked(ctx, func() error {
		pending, err := r.provider.HasPending(ctx)
		if err != nil {
			return err
		}
		if !pending {
			r.log.Info("表结构已是最新")
			return nil
		}
		if err := r.backup(ctx); err != nil {
			return err
		}
		results, err := r.provider.Up(ctx)
		for _, res := range results {
			r.logResult(res)
		}
		if err != nil {
			return fmt.Errorf("执行表结构变更失败: %w", err)
		}
		return nil
	})
}

// Down 回退最近一次变更。
func (r *Runner) Down(ctx context.Context) error {
	return r.locked(ctx, func() error {
		if err := r.backup(ctx); err != nil {
			return err
		}
		res, err := r.provider.Down(ctx)
		if res != nil {
			r.logResult(res)
		}
		if errors.Is(err, goose.ErrNoNextVersion) {
			r.log.Info("没有可以回退的变更")
			return nil
		}
		return err
	})
}

func (r *Runner) locked(ctx context.Context, fn func() error) error {
	lock, err := idb.AcquireLock(ctx, r.pool, lockName, 60*time.Second)
	if err != nil {
		return fmt.Errorf("获取表结构变更锁: %w", err)
	}
	defer func() {
		if err := lock.Release(context.WithoutCancel(ctx)); err != nil {
			r.log.Warn("释放表结构变更锁失败", "err", err)
		}
	}()
	return fn()
}

func (r *Runner) backup(ctx context.Context) error {
	if r.backupCommand == "" {
		r.log.Warn("未配置 migrate.backup_command，执行表结构变更前不会自动备份")
		return nil
	}
	r.log.Info("执行变更前备份", "command", r.backupCommand)
	// backup_command 来自管理员维护的配置文件，不来自用户输入。
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", r.backupCommand) //nolint:gosec // 命令来自可信配置
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("变更前备份失败，已取消变更: %w", err)
	}
	return nil
}

func (r *Runner) logResult(res *goose.MigrationResult) {
	attrs := []any{"version", res.Source.Version, "file", res.Source.Path, "direction", res.Direction, "duration", res.Duration}
	if res.Error != nil {
		r.log.Error("表结构变更失败", append(attrs, "err", res.Error)...)
		return
	}
	r.log.Info("表结构变更完成", attrs...)
}
