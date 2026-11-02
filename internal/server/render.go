package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/view"
)

// Page 是传给所有页面模板的公共数据。各页面的专属数据放在 Data 中。
type Page struct {
	Site  *Server
	Nonce string
	Now   time.Time
	// User 是当前登录用户（*model.User）；未登录时为访客对象，IsLogin 为 false。
	User any
	Data any
}

// Render 渲染页面。渲染失败时记录日志并返回 500 页面。
func (s *Server) Render(w http.ResponseWriter, r *http.Request, status int, layout, page string, data any) {
	p := Page{Site: s, Nonce: Nonce(r), Now: time.Now().In(s.Loc), Data: data, User: model.Guest(s.Loc)}
	if u := CurrentUser(r); u != nil {
		p.User = u
	}
	if err := s.View.Render(w, status, layout, page, p); err != nil {
		s.Log.Error("渲染页面失败", "page", page, "err", err)
		http.Error(w, "页面渲染失败", http.StatusInternalServerError)
	}
}

// JSON 输出 JSON 响应。
func (s *Server) JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.Log.Warn("输出 JSON 失败", "err", err)
	}
}

// NotFound 渲染 404 页面。
func (s *Server) NotFound(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, http.StatusNotFound, "error", "error/404", nil)
}

// MethodNotAllowed 渲染 405 页面。
func (s *Server) MethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, http.StatusMethodNotAllowed, "error", "error/405", nil)
}

// ServerError 渲染 500 页面。
func (s *Server) ServerError(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, http.StatusInternalServerError, "error", "error/500", nil)
}

// WithUser 把当前登录用户放入请求上下文，由鉴权中间件调用。
func WithUser(r *http.Request, u any) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxUser, u))
}

// CurrentUser 返回当前登录用户，未登录时返回 nil。
func CurrentUser(r *http.Request) any {
	return r.Context().Value(ctxUser)
}

// baseFuncs 是所有模板都可以使用的函数。
func baseFuncs(s *Server) template.FuncMap {
	funcs := view.PHPFuncs(s.Loc)
	funcs["year"] = func() int { return time.Now().In(s.Loc).Year() }
	funcs["cfg"] = func(key string) string { return LegacyConfig(&s.Cfg, key) }
	return funcs
}
