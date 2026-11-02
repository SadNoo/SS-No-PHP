package app

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/sub"
)

// linkContent 对应 LinkController::GetContent：/link/{token}?mu=N 返回订阅内容。
//
// 原版令牌不存在时返回空响应（HTTP 200），这里保持一致。
func (a *App) linkContent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")
	l, err := a.Q.GetLinkByToken(ctx, token)
	if err != nil || l.Type != 11 {
		return
	}
	u, err := a.Q.GetUser(ctx, l.Userid)
	if err != nil {
		return
	}
	req, err := sub.ParseRequest(r.URL.Query())
	if err != nil {
		if errors.Is(err, sub.ErrSSRRetired) || errors.Is(err, sub.ErrLegacySSRetired) {
			http.Error(w, err.Error(), http.StatusGone)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	content, err := a.Sub.Render(ctx, u, req, time.Now())
	if err != nil {
		a.Log.Error("生成订阅失败", "uid", u.ID, "format", req.Format, "err", err)
		http.Error(w, "生成订阅失败", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	if kind, _ := sub.UserExpiry(u); kind == sub.ExpiryInvalid {
		a.Log.Warn("用户等级大于 0 但没有等级到期时间（异常数据），订阅不显示到期时间", "uid", u.ID, "class", u.Class)
	}
	h.Set("Subscription-Userinfo", sub.UserinfoHeader(u))
	// 文件名按客户端软件命名（Clash.yaml、Surge5.conf、sing-box.json、Shadowrocket.txt 等），Content-Type 对应内容格式。
	// 以附件下载并禁止类型猜测，浏览器不会把订阅内容当作网页显示。
	filename, contentType := sub.ResponseFile(req)
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	h.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write([]byte(content)) //nolint:gosec // 附件下载（Content-Disposition: attachment、nosniff），不会作为 HTML 显示。
}
