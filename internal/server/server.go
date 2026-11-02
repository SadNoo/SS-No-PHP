// Package server 组装 HTTP 服务：中间件、会话、路由和页面渲染。
package server

import (
	"context"
	"database/sql"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/mysqlstore"
	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/config"
	"github.com/SadNoo/SS-No-PHP/internal/view"
	"github.com/SadNoo/SS-No-PHP/web"
)

// Server 持有处理请求所需的全部依赖。各业务模块通过 Mount 挂载自己的路由。
type Server struct {
	Cfg      config.Config
	DB       *sql.DB
	Log      *slog.Logger
	Sessions *scs.SessionManager
	View     *view.Renderer
	Loc      *time.Location

	realIP *RealIP
	router chi.Router

	extraScriptSrc, extraFrameSrc []string
}

// Module 是一个业务模块，负责注册自己的路由。
type Module interface {
	Routes(r chi.Router)
}

// Options 是创建 Server 时的可选项。
type Options struct {
	// Funcs 是额外的模板函数。
	Funcs template.FuncMap
	// TemplatesFS 为空时使用打包进程序的模板（开发时可传入磁盘目录以便热更新）。
	TemplatesFS fs.FS
	// ExtraScriptSrc / ExtraFrameSrc 加入 CSP 白名单的第三方脚本和 iframe 来源（验证码、Telegram 登录等）。
	ExtraScriptSrc, ExtraFrameSrc []string
}

// New 创建 Server。
func New(cfg config.Config, pool *sql.DB, log *slog.Logger, opts Options) (*Server, error) {
	ri, err := NewRealIP(cfg.HTTP.TrustedProxies)
	if err != nil {
		return nil, err
	}

	sm := scs.New()
	sm.Store = mysqlstore.NewWithCleanupInterval(pool, 30*time.Minute)
	sm.Lifetime = cfg.Security.SessionLifetime.D()
	sm.IdleTimeout = cfg.Security.SessionIdleTimeout.D()
	sm.Cookie.Name = "niuniu_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.Secure = cfg.Security.CookieSecure
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Path = "/"
	// 默认不持久化 Cookie（关闭浏览器即失效），登录时勾选“记住我”才持久化。
	sm.Cookie.Persist = false

	s := &Server{Cfg: cfg, DB: pool, Log: log, Sessions: sm, Loc: cfg.Location(), realIP: ri}

	tfs := opts.TemplatesFS
	if tfs == nil {
		if tfs, err = fs.Sub(web.Templates, "templates"); err != nil {
			return nil, err
		}
	}
	funcs := baseFuncs(s)
	for k, v := range opts.Funcs {
		funcs[k] = v
	}
	if s.View, err = view.New(tfs, funcs, opts.TemplatesFS != nil, log); err != nil {
		return nil, err
	}
	s.extraScriptSrc, s.extraFrameSrc = opts.ExtraScriptSrc, opts.ExtraFrameSrc
	return s, nil
}

// Init 组装全局中间件和基础路由，必须在 Mount 之前调用。顺序很重要：
// 真实 IP → 访问日志 → panic 恢复 → 安全响应头 → 跨站请求防护 → 会话 → afterSession（如加载登录用户）。
func (s *Server) Init(afterSession ...func(http.Handler) http.Handler) {
	r := chi.NewRouter()
	r.Use(s.realIP.Middleware)
	r.Use(AccessLog(s.Log))
	r.Use(Recoverer(s.Log, http.HandlerFunc(s.ServerError)))
	r.Use(SecurityHeaders(s.extraScriptSrc, s.extraFrameSrc, strings.HasPrefix(s.Cfg.App.BaseURL, "https://")))
	r.Use(crossOrigin(s.Cfg.App.BaseURL, s.Log))
	r.Use(s.Sessions.LoadAndSave)
	r.Use(afterSession...)

	r.NotFound(s.NotFound)
	r.MethodNotAllowed(s.MethodNotAllowed)

	r.Get("/healthz", s.healthz)
	r.Get("/404", s.NotFound)
	r.Get("/405", s.MethodNotAllowed)
	r.Get("/500", s.ServerError)
	s.mountStatic(r)
	s.router = r
}

// crossOrigin 拦截跨站发起的非安全请求（POST/PUT/DELETE 等）。
//
// 旧版虽然依赖了 slim/csrf，但从未启用，所有表单都没有 CSRF 防护（已知问题修复）。
// 这里使用 Go 标准库的 CrossOriginProtection：它根据浏览器发送的 Sec-Fetch-Site / Origin 头判断，
// 不需要在表单里放令牌；节点后端、支付回调等非浏览器请求不带这些头，不受影响。
func crossOrigin(baseURL string, log *slog.Logger) func(http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	if baseURL != "" {
		if err := cop.AddTrustedOrigin(baseURL); err != nil {
			log.Warn("base_url 不是有效的来源，未加入可信来源", "base_url", baseURL, "err", err)
		}
	}
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Warn("拦截跨站请求", "method", r.Method, "path", r.URL.Path, "ip", ClientIP(r), "origin", r.Header.Get("Origin"))
		http.Error(w, "跨站请求已被拒绝", http.StatusForbidden)
	}))
	return cop.Handler
}

// Mount 挂载业务模块。
func (s *Server) Mount(mods ...Module) {
	for _, m := range mods {
		m.Routes(s.router)
	}
}

// Handler 返回最终的 HTTP 处理器。
func (s *Server) Handler() http.Handler { return s.router }

// Run 启动 HTTP 服务，ctx 取消时优雅关闭。
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.Cfg.HTTP.Listen,
		Handler:           s.router,
		ReadTimeout:       s.Cfg.HTTP.ReadTimeout.D(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      s.Cfg.HTTP.WriteTimeout.D(),
		IdleTimeout:       s.Cfg.HTTP.IdleTimeout.D(),
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(s.Log.Handler(), slog.LevelWarn),
	}
	errCh := make(chan error, 1)
	go func() {
		s.Log.Info("HTTP 服务启动", "listen", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		s.Log.Info("正在关闭 HTTP 服务")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.DB.PingContext(ctx); err != nil {
		s.Log.Error("健康检查：数据库不可用", "err", err)
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

// mountStatic 按旧版路径提供静态资源。
func (s *Server) mountStatic(r chi.Router) {
	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(static)
	cached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 不提供目录列表。
		if strings.HasSuffix(r.URL.Path, "/") {
			s.NotFound(w, r)
			return
		}
		if info, err := fs.Stat(static, strings.TrimPrefix(r.URL.Path, "/")); err != nil || info.IsDir() {
			s.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		files.ServeHTTP(w, r)
	})
	for _, p := range []string{"/theme/*", "/assets/*", "/images/*", "/vendor/*"} {
		r.Get(p, cached)
	}
	r.Get("/favicon.ico", cached)
	r.Get("/robots.txt", cached)
	r.Get("/paolu.html", cached)
	r.Get("/client-subscription-guide.html", cached)
}
