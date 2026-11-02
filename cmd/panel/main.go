// Command panel 是 niuniupanel 的唯一程序入口。
//
// 用法：
//
//	panel [-config 配置文件] serve            启动服务（默认）
//	panel [-config 配置文件] migrate status   查看表结构变更状态
//	panel [-config 配置文件] migrate up       执行待执行的表结构变更
//	panel [-config 配置文件] migrate down     回退最近一次表结构变更
//	panel [-config 配置文件] xcat <命令>      运维命令与手动执行定时任务（与原版 php xcat 对应）
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // 内置时区数据，不依赖系统的 tzdata（容器等精简环境）

	"github.com/SadNoo/SS-No-PHP/internal/app"
	"github.com/SadNoo/SS-No-PHP/internal/auth"
	"github.com/SadNoo/SS-No-PHP/internal/captcha"
	"github.com/SadNoo/SS-No-PHP/internal/config"
	"github.com/SadNoo/SS-No-PHP/internal/db"
	"github.com/SadNoo/SS-No-PHP/internal/legacy"
	"github.com/SadNoo/SS-No-PHP/internal/logx"
	"github.com/SadNoo/SS-No-PHP/internal/mail"
	"github.com/SadNoo/SS-No-PHP/internal/migrate"
	"github.com/SadNoo/SS-No-PHP/internal/radius"
	"github.com/SadNoo/SS-No-PHP/internal/server"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/web"
	"github.com/go-sql-driver/mysql"
)

// version 在编译时通过 -ldflags "-X main.version=..." 注入。
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", os.Getenv(config.EnvPrefix+"_CONFIG"), "配置文件路径（也可用环境变量 NIUNIU_CONFIG 指定）")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "niuniupanel %s\n\n用法: panel [-config 配置文件] <命令>\n\n命令:\n", version)
		fmt.Fprintln(os.Stderr, "  serve            启动服务（默认）")
		fmt.Fprintln(os.Stderr, "  migrate status   查看表结构变更状态")
		fmt.Fprintln(os.Stderr, "  migrate up       执行待执行的表结构变更")
		fmt.Fprintln(os.Stderr, "  migrate down     回退最近一次表结构变更")
		fmt.Fprintln(os.Stderr, "  xcat <命令>      运维命令与手动执行定时任务（panel xcat 查看全部）")
		fmt.Fprintln(os.Stderr, "  import-legacy    从 SSPanel-UIM 旧数据库导入数据（panel import-legacy -h 查看用法）")
		fmt.Fprintln(os.Stderr, "  version          显示版本")
	}
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		args = []string{"serve"}
	}
	if args[0] == "version" {
		fmt.Println(version)
		return nil
	}

	path, skipped, err := config.ResolvePath(*configPath, os.LookupEnv)
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	log := logx.New(cfg.Log, os.Stderr)
	slog.SetDefault(log)
	if skipped {
		log.Info("可选的配置文件不存在，只使用环境变量", "path", *configPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close() }()

	switch args[0] {
	case "serve":
		return serve(ctx, cfg, pool, log)
	case "migrate":
		return runMigrate(ctx, cfg, pool, log, args[1:])
	case "import-legacy":
		return importLegacy(ctx, cfg, pool, log, args[1:])
	case "xcat":
		_, a, err := build(cfg, pool, log)
		if err != nil {
			return err
		}
		return a.XCat(ctx, args[1:], os.Stdout)
	default:
		flag.Usage()
		return fmt.Errorf("未知命令: %s", args[0])
	}
}

func serve(ctx context.Context, cfg config.Config, pool *sql.DB, log *slog.Logger) error {
	log.Info("niuniupanel 启动", "version", version)
	if cfg.App.Debug {
		log.Warn("调试模式已开启，正式环境请关闭 app.debug")
	}
	m, err := migrate.New(pool, cfg.Migrate.BackupCommand, log)
	if err != nil {
		return err
	}
	if cfg.Migrate.AutoUp {
		if err := m.Up(ctx); err != nil {
			return fmt.Errorf("自动执行表结构变更失败，服务不会启动: %w", err)
		}
	} else if pending, err := m.Pending(ctx); err != nil {
		return err
	} else if pending {
		return errors.New("存在未执行的表结构变更，请先执行 panel migrate up，或开启 migrate.auto_up")
	}

	srv, a, err := build(cfg, pool, log)
	if err != nil {
		return err
	}
	a.StartScheduler(ctx)
	return srv.Run(ctx)
}

// build 创建 HTTP 服务与业务对象（serve 与 xcat 共用）。
func build(cfg config.Config, pool *sql.DB, log *slog.Logger) (*server.Server, *app.App, error) {
	scripts, frames := cspSources(cfg)
	srv, err := server.New(cfg, pool, log, server.Options{ExtraScriptSrc: scripts, ExtraFrameSrc: frames})
	if err != nil {
		return nil, nil, err
	}
	am := &auth.Manager{SM: srv.Sessions, Q: store.New(pool), Loc: srv.Loc, Log: log, BindIP: cfg.Display.EnableLoginBindIP}
	tfs, err := fs.Sub(web.Templates, "templates")
	if err != nil {
		return nil, nil, err
	}
	mailer, err := mail.New(cfg, tfs, log)
	if err != nil {
		return nil, nil, err
	}
	srv.Init(am.LoadUser)
	a := app.New(srv, am, mailer, captcha.New(cfg.Captcha, srv.Sessions, log))
	if cfg.Radius.Enable {
		// Radius 库连不上时面板照常运行，只是不同步 Radius。
		rc := cfg.Radius.DB
		rc.ServerTimeZone = true
		ctx, cancel := context.WithTimeout(context.Background(), rc.ConnectTimeout.D()+time.Second)
		rdb, err := db.Open(ctx, rc)
		cancel()
		if err != nil {
			log.Error("连接 Radius 数据库失败，Radius 同步不可用", "err", err)
		} else {
			a.Radius = &radius.Client{DB: rdb, Secret: cfg.Radius.Secret}
		}
	}
	srv.Mount(a)
	return srv, a, nil
}

// cspSources 根据启用的第三方服务生成 CSP 白名单。
func cspSources(cfg config.Config) (scripts, frames []string) {
	if cfg.Captcha.EnableLogin || cfg.Captcha.EnableReg || cfg.Captcha.EnableCheckin {
		scripts = append(scripts, captcha.ScriptSources(cfg.Captcha)...)
		frames = append(frames, captcha.FrameSources(cfg.Captcha)...)
	}
	if cfg.Telegram.Enable {
		scripts = append(scripts, "https://telegram.org")
		frames = append(frames, "https://oauth.telegram.org")
	}
	if cfg.Contact.EnableMylivechat {
		scripts = append(scripts, "https://mylivechat.com", "https://*.mylivechat.com")
		frames = append(frames, "https://mylivechat.com", "https://*.mylivechat.com")
	}
	return scripts, frames
}

func runMigrate(ctx context.Context, cfg config.Config, pool *sql.DB, log *slog.Logger, args []string) error {
	m, err := migrate.New(pool, cfg.Migrate.BackupCommand, log)
	if err != nil {
		return err
	}
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "status":
		list, err := m.Status(ctx)
		if err != nil {
			return err
		}
		for _, st := range list {
			applied := "未执行"
			if !st.AppliedAt.IsZero() {
				applied = "已执行 " + st.AppliedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Printf("%05d  %-40s  %s\n", st.Source.Version, st.Source.Path, applied)
		}
		return nil
	case "up":
		return m.Up(ctx)
	case "down":
		return m.Down(ctx)
	default:
		return fmt.Errorf("未知的 migrate 子命令: %s（可用: status / up / down）", sub)
	}
}

// importLegacy 从 SSPanel-UIM 旧数据库导入数据（先执行表结构变更）。
func importLegacy(ctx context.Context, cfg config.Config, pool *sql.DB, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("import-legacy", flag.ContinueOnError)
	source := fs.String("source", "", "旧库连接串（go-sql-driver 格式），也可用环境变量 NIUNIU_IMPORT_DSN 提供（推荐，避免密码出现在进程列表中），\n例如 user:pass@tcp(127.0.0.1:3306)/sspanel")
	tz := fs.String("tz", cfg.App.TimeZone, "旧库时间字段使用的时区（原版 timeZone，PRC 即 Asia/Shanghai）")
	skipLogs := fs.Bool("skip-logs", false, "跳过流量记录、在线 IP、登录记录等日志类表")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dsn := *source
	if dsn == "" {
		dsn = os.Getenv(config.EnvPrefix + "_IMPORT_DSN")
	}
	if dsn == "" {
		fs.Usage()
		return errors.New("请用 -source 或环境变量 NIUNIU_IMPORT_DSN 指定旧数据库")
	}
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		return fmt.Errorf("时区 %q 无效: %w", *tz, err)
	}
	mc, err := mysql.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("旧库连接串格式错误: %w", err)
	}
	mc.ParseTime = false // 时间按原始文本读取后再换算
	mc.Collation = "utf8mb4_unicode_ci"
	connector, err := mysql.NewConnector(mc)
	if err != nil {
		return err
	}
	src := sql.OpenDB(connector)
	defer func() { _ = src.Close() }()
	if err := src.PingContext(ctx); err != nil {
		return fmt.Errorf("连接旧库: %w", err)
	}
	m, err := migrate.New(pool, cfg.Migrate.BackupCommand, log)
	if err != nil {
		return err
	}
	if err := m.Up(ctx); err != nil {
		return err
	}
	im := &legacy.Importer{Src: src, Dst: pool, Loc: loc, SkipLogs: *skipLogs, Out: os.Stdout}
	if err := im.Run(ctx); err != nil {
		return err
	}
	fmt.Println("导入完成。请参照 docs/migration.md 完成后续步骤（配置 legacy_salt、节点密钥等）。")
	return nil
}
