// Package app 实现面板的全部业务页面和接口，对应原版 app/Controllers。
// 每个文件对应原版的一个控制器或一组相关功能，函数注释中标出原版方法名。
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-sql-driver/mysql"

	"github.com/SadNoo/SS-No-PHP/internal/auth"
	"github.com/SadNoo/SS-No-PHP/internal/captcha"
	"github.com/SadNoo/SS-No-PHP/internal/cloudflare"
	"github.com/SadNoo/SS-No-PHP/internal/config"
	"github.com/SadNoo/SS-No-PHP/internal/ipgeo"
	"github.com/SadNoo/SS-No-PHP/internal/mail"
	"github.com/SadNoo/SS-No-PHP/internal/payment/alipay"
	"github.com/SadNoo/SS-No-PHP/internal/radius"
	"github.com/SadNoo/SS-No-PHP/internal/server"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/sub"
	"github.com/SadNoo/SS-No-PHP/internal/telegram"
)

// App 持有业务处理所需的依赖。
type App struct {
	Srv     *server.Server
	Cfg     *config.Config
	DB      *sql.DB
	Q       *store.Queries
	Auth    *auth.Manager
	Mail    mail.Sender
	Captcha *captcha.Service
	Sub     *sub.Service
	Geo     *ipgeo.DB
	Log     *slog.Logger
	Loc     *time.Location
	F2F     *alipay.Client     // 支付宝当面付，未启用时为 nil
	TGBot   *telegram.Bot      // 测试时替换 Telegram 接口地址；为 nil 时按配置创建
	Radius  *radius.Client     // FreeRADIUS 数据库，未启用时为 nil
	CF      *cloudflare.Client // 测试时替换 Cloudflare 接口地址；为 nil 时按配置创建

	nodeAuth        nodeAuthState                                            // 节点来源 IP 校验状态（nodeauth.go）
	blockedIPWarn   blockedIPWarnState                                       // 节点接口跳过非法封禁 IP 的告警（blockedip.go）
	disconnectHooks disconnectRepairHooks                                    // 修复 disconnect_ip 的测试钩子（xcat_disconnect.go）
	lookupIP        func(ctx context.Context, host string) ([]string, error) // 测试时替换域名解析

	// 限流器：原版没有任何频率限制，登录可被暴力破解、验证码和重置邮件可被滥发（K20）。
	loginLimit *server.RateLimiter
	regLimit   *server.RateLimiter
	mailLimit  *server.RateLimiter
	subLimit   *server.RateLimiter
}

// New 创建 App。
func New(srv *server.Server, am *auth.Manager, mailer mail.Sender, cs *captcha.Service) *App {
	geo, err := ipgeo.Open(srv.Cfg.Display.QQWryPath)
	if err != nil {
		srv.Log.Warn("读取纯真 IP 库失败，将不显示 IP 归属地", "path", srv.Cfg.Display.QQWryPath, "err", err)
		geo = &ipgeo.DB{}
	}
	a := &App{
		Geo: geo,
		Srv: srv, Cfg: &srv.Cfg, DB: srv.DB, Q: store.New(srv.DB), Auth: am,
		Mail: mailer, Captcha: cs, Log: srv.Log, Loc: srv.Loc,
		Sub: &sub.Service{
			Q: store.New(srv.DB), AppName: srv.Cfg.App.Name, SubURL: srv.Cfg.App.SubURL, MergeSub: srv.Cfg.Users.MergeSub,
			MuRegex: srv.Cfg.Register.MuRegex, MuSuffix: srv.Cfg.Register.MuSuffix, Loc: srv.Loc,
			ProxyDomains: srv.Cfg.App.SubProxyDomains,
		},
		loginLimit: server.NewRateLimiter(6*time.Second, 10),
		regLimit:   server.NewRateLimiter(time.Minute, 5),
		mailLimit:  server.NewRateLimiter(time.Minute, 3),
		subLimit:   server.NewRateLimiter(2*time.Second, 30),
	}
	a.initPayment()
	return a
}

// Routes 注册全部路由，结构与原版 config/routes.php 一致。
func (a *App) Routes(r chi.Router) {
	a.homeRoutes(r)
	r.Route("/auth", func(r chi.Router) {
		r.Use(auth.RequireGuest)
		a.authRoutes(r)
	})
	r.Route("/password", func(r chi.Router) {
		r.Use(auth.RequireGuest)
		a.passwordRoutes(r)
	})
	// 订阅地址：令牌即凭证，按 IP 限流防止枚举（K22）。
	r.With(a.subLimit.Middleware).Get("/link/{token}", a.linkContent)
	a.nodeAPIRoutes(r)
	a.paymentRoutes(r)
	r.Post("/telegram_callback", a.telegramCallback)
	r.Route("/admin", func(r chi.Router) {
		r.Use(auth.RequireLogin, auth.RequireAdmin)
		a.adminRoutes(r)
	})
	r.Route("/user", func(r chi.Router) {
		r.Use(auth.RequireLogin)
		a.userRoutes(r)
	})
}

// render 渲染原版风格的整页模板（模板自身 include 头尾）。
func (a *App) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	a.Srv.Render(w, r, http.StatusOK, "", page, data)
}

// Ret 是原版接口常用的返回格式 {"ret": 0/1, "msg": "..."}。
type Ret map[string]any

// ok / fail 构造返回值。
func ok(msg string) Ret   { return Ret{"ret": 1, "msg": msg} }
func fail(msg string) Ret { return Ret{"ret": 0, "msg": msg} }

// json 输出 JSON。
func (a *App) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		a.Log.Warn("输出 JSON 失败", "err", err)
	}
}

// param 对应 Slim 的 $request->getParam：先取表单，再取查询参数。
// 标准库只解析 POST/PUT/PATCH 的请求体，原版的 DELETE 请求（如关闭自动续费）也在请求体中带参数，这里一并解析。
func param(r *http.Request, key string) string {
	if r.Method == http.MethodDelete && r.PostForm == nil {
		r.Method = http.MethodPost
		_ = r.ParseForm()
		r.Method = http.MethodDelete
	}
	return r.FormValue(key)
}

// trimmed 取参数并去掉首尾空白。
func trimmed(r *http.Request, key string) string { return strings.TrimSpace(r.FormValue(key)) }

// tx 在事务中执行 fn。
func (a *App) tx(ctx context.Context, fn func(q *store.Queries) error) error {
	return a.txOpts(ctx, nil, fn)
}

// disconnectTxAttempts 是 disconnectTx 遇到死锁时整个事务最多执行的次数。
const disconnectTxAttempts = 3

// disconnectTx 执行写入 disconnect_ip（用户字段或记录表）的事务：每分钟检查新增限制、到期解除、
// xcat repairDisconnectIP、删除用户。
//
//   - 隔离级别 READ COMMITTED：加锁读取只锁存在的记录，不锁索引间隙。REPEATABLE READ 下读取不存在的记录会锁住间隙，
//     不同用户的记录可能落在同一间隙中：修复事务读过用户 A 的（空）记录后，会挡住检查为用户 B 插入记录，
//     修复事务随后又要等 B 的用户行锁，形成死锁（第三方 P2 复验）。
//   - 一致性不依赖间隙锁：这些事务都先锁用户行（FOR UPDATE）再读写该用户的记录，新增记录只发生在持有用户行锁的事务中，
//     锁内读到的就是最新提交的数据，期间其他事务也不能改动该用户的字段与记录。
//   - 仍然遇到死锁（1213）时回滚并重新执行整个事务（fn 必须从头重新读取、计算，不能沿用上一次的结果），
//     最多 disconnectTxAttempts 次；其他错误与最后一次的死锁原样返回，不忽略。
func (a *App) disconnectTx(ctx context.Context, fn func(q *store.Queries) error) error {
	for attempt := 1; ; attempt++ {
		err := a.txOpts(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, fn)
		if err == nil || !isDeadlock(err) || attempt >= disconnectTxAttempts || ctx.Err() != nil {
			return err
		}
		a.Log.Warn("disconnect_ip 事务死锁，已回滚并重试", "attempt", attempt)
	}
}

// txOpts 按指定选项在事务中执行 fn。
func (a *App) txOpts(ctx context.Context, opts *sql.TxOptions, fn func(q *store.Queries) error) error {
	t, err := a.DB.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	if err := fn(a.Q.WithTx(t)); err != nil {
		_ = t.Rollback()
		return err
	}
	return t.Commit()
}

// isDeadlock 判断是否为 InnoDB 死锁（事务已被回滚）。
func isDeadlock(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1213
}

// serverError 记录错误并返回通用错误。
func (a *App) serverError(w http.ResponseWriter, r *http.Request, err error) {
	a.Log.Error("处理请求失败", "path", r.URL.Path, "err", err)
	a.Srv.ServerError(w, r)
}

// jsonError 记录错误并返回 JSON 形式的通用错误（用于原版返回 JSON 的接口）。
func (a *App) jsonError(w http.ResponseWriter, r *http.Request, err error) {
	a.Log.Error("处理请求失败", "path", r.URL.Path, "err", err)
	a.json(w, fail("系统错误，请稍后重试"))
}

// isNoRows 判断是否为“查询无结果”。
func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
