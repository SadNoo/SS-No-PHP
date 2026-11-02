package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/server"
	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// 会话中保存的键。
const (
	keyUID         = "uid"
	keyFingerprint = "pfp"
	keyIP          = "ip"
	keyAdminUID    = "admin_uid"   // 管理员切换为用户身份时，记录原管理员 ID（对应原版 backtoadmin）
	keyAdminLocal  = "admin_local" // 切换前所在的后台页面，返回时跳回（原版 old_local）
)

// Manager 管理登录状态（K12：原版把用户 ID 和密码哈希签名放在 Cookie 里，无法单独让某个登录失效；
// 这里改为服务端会话，Cookie 只保存随机令牌）。
type Manager struct {
	SM     *scs.SessionManager
	Q      *store.Queries
	Loc    *time.Location
	Log    *slog.Logger
	BindIP bool // enable_login_bind_ip：会话与登录时的 IP 绑定
}

// Login 登录：更换会话令牌（防止会话固定攻击），记录用户 ID 和密码指纹。
// remember 为 true 时 Cookie 持久化（“记住我”），否则关闭浏览器即失效。
func (m *Manager) Login(ctx context.Context, u store.User, ip string, remember bool) error {
	if err := m.SM.RenewToken(ctx); err != nil {
		return err
	}
	m.SM.Put(ctx, keyUID, int64(u.ID))
	m.SM.Put(ctx, keyFingerprint, model.PassFingerprint(u.Pass))
	m.SM.Put(ctx, keyIP, ip)
	m.SM.RememberMe(ctx, remember)
	return nil
}

// Logout 退出登录并销毁会话。
func (m *Manager) Logout(ctx context.Context) error { return m.SM.Destroy(ctx) }

// SwitchToUser 管理员以用户身份登录（原版 changetouser），保留原管理员 ID 以便返回。
// local 是返回时跳转的后台页面，只接受本站路径。
func (m *Manager) SwitchToUser(ctx context.Context, admin, target store.User, local string) error {
	if err := m.SM.RenewToken(ctx); err != nil {
		return err
	}
	if !strings.HasPrefix(local, "/") || strings.HasPrefix(local, "//") || strings.Contains(local, "\\") {
		local = "/admin/user"
	}
	m.SM.Put(ctx, keyAdminUID, int64(admin.ID))
	m.SM.Put(ctx, keyAdminLocal, local)
	m.SM.Put(ctx, keyUID, int64(target.ID))
	m.SM.Put(ctx, keyFingerprint, model.PassFingerprint(target.Pass))
	return nil
}

// AdminUID 返回切换身份前的管理员 ID，未切换时返回 0。
func (m *Manager) AdminUID(ctx context.Context) int64 { return m.SM.GetInt64(ctx, keyAdminUID) }

// BackToAdmin 切回管理员身份（原版 backtoadmin），返回应跳转的页面；未处于切换状态时返回空字符串。
func (m *Manager) BackToAdmin(ctx context.Context) (string, error) {
	adminID := m.SM.GetInt64(ctx, keyAdminUID)
	if adminID == 0 {
		return "", nil
	}
	admin, err := m.Q.GetUser(ctx, uint32(adminID)) //nolint:gosec // ID 来自会话
	if errors.Is(err, sql.ErrNoRows) || (err == nil && admin.IsAdmin == 0) {
		return "", m.Logout(ctx)
	}
	if err != nil {
		return "", err
	}
	local := m.SM.GetString(ctx, keyAdminLocal)
	if err := m.SM.RenewToken(ctx); err != nil {
		return "", err
	}
	m.SM.Remove(ctx, keyAdminUID)
	m.SM.Remove(ctx, keyAdminLocal)
	m.SM.Put(ctx, keyUID, int64(admin.ID))
	m.SM.Put(ctx, keyFingerprint, model.PassFingerprint(admin.Pass))
	return local, nil
}

// LoadUser 从会话中取出当前用户并放入请求上下文。
//
// 与原版一致：用户被删除、修改过密码（密码指纹变化）、开启 IP 绑定且 IP 变化时，视为未登录。
func (m *Manager) LoadUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		uid := m.SM.GetInt64(ctx, keyUID)
		if uid <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		u, err := m.Q.GetUser(ctx, uint32(uid)) //nolint:gosec // ID 来自会话
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				m.Log.Error("读取登录用户失败", "uid", uid, "err", err)
			}
			_ = m.SM.Destroy(ctx)
			next.ServeHTTP(w, r)
			return
		}
		if m.SM.GetString(ctx, keyFingerprint) != model.PassFingerprint(u.Pass) ||
			(m.BindIP && m.SM.GetString(ctx, keyIP) != server.ClientIP(r)) {
			_ = m.SM.Destroy(ctx)
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, server.WithUser(r, model.NewUser(u, m.Loc)))
	})
}

// User 返回当前登录用户，未登录时返回 nil。
func User(r *http.Request) *model.User {
	u, _ := server.CurrentUser(r).(*model.User)
	return u
}

// 账户被禁用时仍可访问的页面（原版 Auth 中间件的 $enablePages）。
var disabledAllowed = map[string]bool{"/user/disable": true, "/user/backtoadmin": true, "/user/logout": true}

// RequireLogin 对应原版 Auth 中间件：未登录跳转登录页；账户被禁用时只能访问少数页面。
func RequireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := User(r)
		if u == nil {
			http.Redirect(w, r, "/auth/login", http.StatusFound)
			return
		}
		if !u.IsEnabled() && !disabledAllowed[r.URL.Path] {
			http.Redirect(w, r, "/user/disable", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireGuest 对应原版 Guest 中间件：已登录用户跳转用户中心。
func RequireGuest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if User(r) != nil {
			http.Redirect(w, r, "/user", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin 对应原版 Admin 中间件。
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := User(r)
		if u == nil {
			http.Redirect(w, r, "/auth/login", http.StatusFound)
			return
		}
		if !u.IsAdmin() {
			http.Redirect(w, r, "/user", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RefreshFingerprint 修改密码后更新当前会话的密码指纹：当前登录保持有效，其他设备上的登录失效（K12）。
func (m *Manager) RefreshFingerprint(ctx context.Context, passHash string) error {
	if err := m.SM.RenewToken(ctx); err != nil {
		return err
	}
	m.SM.Put(ctx, keyFingerprint, model.PassFingerprint(passHash))
	return nil
}
