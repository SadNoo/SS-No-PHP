package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type ctxKey int

const (
	ctxClientIP ctxKey = iota
	ctxNonce
	ctxUser
)

// ClientIP 返回经可信代理校正后的客户端 IP。
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(ctxClientIP).(string); ok {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Nonce 返回本次请求的 CSP nonce，模板中的内联脚本必须带上它。
func Nonce(r *http.Request) string {
	n, _ := r.Context().Value(ctxNonce).(string)
	return n
}

// RealIP 计算真实客户端 IP。
//
// 旧版直接采信 X-Forwarded-For 的第一个值，任何人都可以伪造 IP（已知问题修复）。
// 这里只有当直连地址属于可信代理时才读取 X-Forwarded-For，并从右往左跳过可信代理，
// 取第一个不可信的地址作为客户端 IP。
type RealIP struct {
	trusted []netip.Prefix
}

// NewRealIP 解析可信代理列表，支持单个 IP 或 CIDR。
func NewRealIP(proxies []string) (*RealIP, error) {
	ri := &RealIP{}
	for _, p := range proxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			addr, err := netip.ParseAddr(p)
			if err != nil {
				return nil, fmt.Errorf("可信代理地址无效 %q: %w", p, err)
			}
			ri.trusted = append(ri.trusted, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			return nil, fmt.Errorf("可信代理网段无效 %q: %w", p, err)
		}
		ri.trusted = append(ri.trusted, prefix.Masked())
	}
	return ri, nil
}

func (ri *RealIP) isTrusted(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, p := range ri.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Resolve 根据直连地址和 X-Forwarded-For 计算客户端 IP。
func (ri *RealIP) Resolve(remoteAddr, xff string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if !ri.isTrusted(host) || xff == "" {
		return host
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := strings.TrimSpace(parts[i])
		if _, err := netip.ParseAddr(ip); err != nil {
			// 格式不对的值说明链路被篡改，退回直连地址最安全。
			return host
		}
		if !ri.isTrusted(ip) {
			return ip
		}
	}
	return host
}

// Middleware 把客户端 IP 放入请求上下文。
func (ri *RealIP) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ri.Resolve(r.RemoteAddr, r.Header.Get("X-Forwarded-For"))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxClientIP, ip)))
	})
}

// SecurityHeaders 设置安全响应头，并为每个请求生成 CSP nonce。
func SecurityHeaders(extraScriptSrc, extraFrameSrc []string, hsts bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b := make([]byte, 18)
			if _, err := rand.Read(b); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			nonce := base64.StdEncoding.EncodeToString(b)
			scriptSrc := append([]string{"'self'", "'nonce-" + nonce + "'"}, extraScriptSrc...)
			frameSrc := append([]string{"'self'"}, extraFrameSrc...)
			h := w.Header()
			h.Set("Content-Security-Policy", strings.Join([]string{
				"default-src 'self'",
				"script-src " + strings.Join(scriptSrc, " "),
				"style-src 'self' 'unsafe-inline'",
				"img-src 'self' data:",
				"font-src 'self' data:",
				"connect-src 'self'",
				"frame-src " + strings.Join(frameSrc, " "),
				// 节点详情页在用户中心内以 iframe 打开（原版设计），只允许本站嵌入。
				"frame-ancestors 'self'",
				"base-uri 'self'",
				"form-action 'self'",
				"object-src 'none'",
			}, "; "))
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "SAMEORIGIN")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=31536000")
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxNonce, nonce)))
		})
	}
}

// statusRecorder 记录响应状态码和字节数，供访问日志使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog 记录每个请求。不记录查询参数，避免把订阅 token 等敏感信息写进日志。
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			log.Info("http",
				"method", r.Method,
				"path", redactPath(r.URL.Path),
				"status", rec.status,
				"bytes", rec.bytes,
				"ip", ClientIP(r),
				"duration", time.Since(start).Round(time.Microsecond),
			)
		})
	}
}

// redactPath 隐藏路径里的令牌，例如 /link/<token> 和 /password/token/<token>。
func redactPath(p string) string {
	for _, prefix := range []string{"/link/", "/password/token/", "/api/token/"} {
		if strings.HasPrefix(p, prefix) && len(p) > len(prefix) {
			return prefix + "***"
		}
	}
	return p
}

// Recoverer 捕获处理函数中的 panic，记录日志并返回通用错误页，不向用户暴露任何内部信息。
//
// 旧版使用 Whoops 中间件，出错时会把代码、配置等信息显示在页面上（已知问题修复）。
func Recoverer(log *slog.Logger, onPanic http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
						panic(v)
					}
					log.Error("处理请求时发生 panic", "panic", v, "path", r.URL.Path, "stack", string(debug.Stack()))
					onPanic.ServeHTTP(w, r)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimiter 是按键（通常是客户端 IP）限流的令牌桶集合，长时间不活跃的键会被清理。
type RateLimiter struct {
	mu      sync.Mutex
	limit   rate.Limit
	burst   int
	entries map[string]*limiterEntry
}

type limiterEntry struct {
	l    *rate.Limiter
	seen time.Time
}

// NewRateLimiter 创建限流器：每个键平均每 every 允许一次，最多突发 burst 次。
func NewRateLimiter(every time.Duration, burst int) *RateLimiter {
	return &RateLimiter{limit: rate.Every(every), burst: burst, entries: map[string]*limiterEntry{}}
}

// Allow 判断 key 本次请求是否放行。
func (rl *RateLimiter) Allow(key string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if len(rl.entries) > 10000 {
		for k, e := range rl.entries {
			if now.Sub(e.seen) > 10*time.Minute {
				delete(rl.entries, k)
			}
		}
	}
	e, ok := rl.entries[key]
	if !ok {
		e = &limiterEntry{l: rate.NewLimiter(rl.limit, rl.burst)}
		rl.entries[key] = e
	}
	e.seen = now
	return e.l.AllowN(now, 1)
}

// Middleware 按客户端 IP 限流，超出时返回 429。
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.Allow(ClientIP(r)) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "请求过于频繁，请稍后再试", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
