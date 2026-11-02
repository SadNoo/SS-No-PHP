package app

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (a *App) homeRoutes(r chi.Router) {
	r.Get("/", a.indexOld)
	r.Get("/indexold", a.indexOld)
	r.Get("/tos", a.tos)
}

// indexOld 对应 HomeController::indexold（原版首页路由 / 指向它）。
func (a *App) indexOld(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "home/indexold", map[string]any{})
}

// tos 对应 HomeController::tos。
func (a *App) tos(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "home/tos", map[string]any{})
}
