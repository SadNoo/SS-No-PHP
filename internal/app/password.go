package app

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/auth"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

func (a *App) passwordRoutes(r chi.Router) {
	r.Get("/reset", a.passwordReset)
	r.With(a.mailLimit.Middleware).Post("/reset", a.passwordHandleReset)
	r.Get("/token/{token}", a.passwordToken)
	r.With(a.loginLimit.Middleware).Post("/token/{token}", a.passwordHandleToken)
}

// passwordReset 对应 PasswordController::reset。
func (a *App) passwordReset(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "password/reset", map[string]any{})
}

// passwordHandleReset 对应 PasswordController::handleReset 与 Password::sendResetEmail。
func (a *App) passwordHandleReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	email := trimmed(r, "email")
	// K19：原版对不存在的邮箱提示“此邮箱不存在”，可被用来探测已注册邮箱；现在统一返回相同提示。
	res := ok("重置邮件已经发送,请检查邮箱.")
	_, err := a.Q.GetUserByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		a.json(w, res)
		return
	}
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	token := util.RandomChar(64)
	now := time.Now().Unix()
	if err := a.Q.InsertPasswordReset(ctx, store.InsertPasswordResetParams{Email: email, Token: token, InitTime: now, ExpireTime: now + 86400}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	resetURL := a.Cfg.App.BaseURL + "/password/token/" + token
	if err := a.Mail.Send(ctx, email, a.Cfg.App.Name+"重置密码", "password_reset", map[string]any{"resetUrl": resetURL}); err != nil {
		a.Log.Warn("发送重置密码邮件失败", "email", email, "err", err)
	}
	a.json(w, res)
}

// passwordToken 对应 PasswordController::token。
func (a *App) passwordToken(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "password/token", map[string]any{"token": chi.URLParam(r, "token")})
}

// passwordHandleToken 对应 PasswordController::handleToken。
func (a *App) passwordHandleToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	password := param(r, "password")
	if password != param(r, "repasswd") {
		a.json(w, fail("两次输入不符合"))
		return
	}
	if len(password) < 8 {
		a.json(w, fail("密码太短啦"))
		return
	}
	expired := fail("链接已经失效,请重新获取")
	t, err := a.Q.GetPasswordReset(ctx, chi.URLParam(r, "token"))
	if err != nil || t.ExpireTime < time.Now().Unix() {
		a.json(w, expired)
		return
	}
	u, err := a.Q.GetUserByEmail(ctx, t.Email)
	if err != nil {
		a.json(w, expired)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	err = a.tx(ctx, func(q *store.Queries) error {
		if err := q.ResetUserPassword(ctx, store.ResetUserPasswordParams{Pass: hash, ID: u.ID}); err != nil {
			return err
		}
		// 原版重置后清除订阅链接（clean_link），让旧订阅地址失效。
		if err := q.DeleteLinksByUser(ctx, u.ID); err != nil {
			return err
		}
		// K18：原版重置链接在 24 小时内可以反复使用；现在使用一次后即作废该邮箱的全部重置链接。
		return q.DeletePasswordResetByEmail(ctx, t.Email)
	})
	if err != nil {
		a.Log.Error("重置密码失败", "uid", u.ID, "err", err)
		a.json(w, fail("重置失败,请重试"))
		return
	}
	a.json(w, ok("重置成功"))
}
