package app

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// retiredRoutes 处理已停用的用户功能：面板中转（外部中转写在节点地址中）、延迟检测、
// 站内使用教程（改用客户端订阅教程）、用户工单（改为联系方式页面），以及修改加密方式和协议混淆。
// 旧链接跳到替代页面，提交请求返回失败；数据库中原有的数据不改动。
func (a *App) retiredRoutes(r chi.Router) {
	for path, to := range map[string]string{
		"/tutorial":     "/client-subscription-guide.html",
		"/lookingglass": "/user",
		"/relay":        "/user", "/relay/create": "/user", "/relay/{id}/edit": "/user",
		"/ticket": "/user/contact", "/ticket/create": "/user/contact", "/ticket/{id}/view": "/user/contact",
	} {
		r.Get(path, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, to, http.StatusFound) })
	}
	relay := a.retired("不支持面板中转")
	r.Post("/relay", relay)
	r.Put("/relay/{id}", relay)
	r.Delete("/relay", relay)
	ticket := a.retired("不支持发起工单，请通过联系方式联系管理员")
	r.Post("/ticket", ticket)
	r.Put("/ticket/{id}", ticket)
	unsupported := a.retired("不支持修改")
	r.Post("/method", unsupported)
	r.Post("/ssr", unsupported)
}

// retired 返回固定失败信息。
func (a *App) retired(msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { a.json(w, fail(msg)) }
}
