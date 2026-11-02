package app

import (
	"crypto/rand"
	"fmt"
	"html/template"
	"math/big"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/auth"
	"github.com/SadNoo/SS-No-PHP/internal/captcha"
	"github.com/SadNoo/SS-No-PHP/internal/config"
	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/server"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/sub"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

func (a *App) userRoutes(r chi.Router) {
	r.Get("/", a.userIndex)
	r.Post("/checkin", a.checkin)
	r.Get("/node", a.userNode)
	r.Get("/node/{id}", a.nodeInfo)
	r.Get("/node/{id}/ajax", a.nodeAjax)
	r.Get("/announcement", a.announcement)
	r.Get("/donate", a.donate)
	r.Get("/trafficlog", a.trafficLog)
	r.Get("/sys", a.simplePage("user/sys"))
	r.Get("/disable", a.simplePage("user/disable"))
	r.Get("/logout", a.userLogout)
	// K23：以下操作会修改数据，原版为 GET 链接，可被其他网站诱导触发，改为 POST。
	r.Post("/backtoadmin", a.backToAdmin)
	r.Post("/url_reset", a.resetURL)
	a.settingsRoutes(r)
	a.shopRoutes(r)
	r.Get("/contact", a.simplePage("user/contact"))
	a.retiredRoutes(r)
	a.detectRoutes(r)
	a.userPaymentRoutes(r)
}

// groupLink 是首页「群组」的一项，URL 已按白名单校验，可直接作为链接输出。
type groupLink struct {
	Title, Text string
	URL         template.URL
	MinClass    int
}

// groupLinks 返回配置中的群组入口。启动时已校验，这里再次过滤，不合格的项不显示。
func (a *App) groupLinks() []groupLink {
	out := make([]groupLink, 0, len(a.Cfg.Contact.GroupLinks))
	for _, g := range a.Cfg.Contact.GroupLinks {
		if config.ValidGroupLinkURL(g.URL) {
			out = append(out, groupLink{Title: g.Title, Text: g.Text, URL: template.URL(g.URL), MinClass: g.MinClass}) //nolint:gosec // 协议已按白名单校验（https / http / tg / mailto）
		}
	}
	return out
}

// userPage 渲染用户中心页面，并附上所有页面共用的变量（原版 Services\View）。
func (a *App) userPage(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	if a.Auth.AdminUID(r.Context()) != 0 {
		data["can_backtoadmin"] = 1
	}
	a.render(w, r, page, data)
}

func (a *App) simplePage(page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { a.userPage(w, r, page, nil) }
}

// userIndex 对应 UserController::index。
func (a *App) userIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	now := time.Now()

	token, err := a.Sub.SubToken(ctx, u.ID, 0)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	data := map[string]any{
		"subscription_base":    a.Cfg.App.SubURL + token,
		"subscription_lines":   a.subscriptionLines(token),
		"subscription_clients": subscriptionClients,
		"protocol_subs":        protocolSubscriptions,
		"group_links":          a.groupLinks(),
		"display_ios_class":    a.Cfg.Display.DisplayIOSClass,
		"display_ios_topup":    a.Cfg.Display.DisplayIOSTopup,
		"ios_account":          a.Cfg.Display.IOSAccount,
		"ios_password":         a.Cfg.Display.IOSPassword,
		"mergeSub":             boolStr(a.Cfg.Users.MergeSub),
		"baseUrl":              a.Cfg.App.BaseURL,
		"ann":                  nil,
	}
	captchaData(a.Captcha.Prepare(ctx, captcha.Checkin), data)

	if ann, err := a.Q.LatestAnnouncement(ctx); err == nil {
		data["ann"] = ann
	} else if !isNoRows(err) {
		a.serverError(w, r, err)
		return
	}

	if data["v2_url_all"], err = a.Sub.AllVMessURL(ctx, u.User); err != nil {
		a.serverError(w, r, err)
		return
	}
	// 「按协议订阅」中 SS2022 的「全部 URL」：与订阅 list=ss 相同的 SS2022 链接原文，不附带流量信息。
	ssReq, err := sub.ParseRequest(url.Values{"list": {"ss"}, "extend": {"0"}})
	if err == nil {
		data["ss_url_all"], err = a.Sub.Render(ctx, u.User, ssReq, now)
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	if data["next_reset"], err = a.nextTrafficResetText(ctx, u.User, now); err != nil {
		a.serverError(w, r, err)
		return
	}

	if data["online_ip_count"], err = a.onlineIPCount(r, u.ID, now); err != nil {
		a.serverError(w, r, err)
		return
	}
	topUp, err := a.Q.SumUserCodeNumber(ctx, u.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	data["top_up"] = topUp
	a.userPage(w, r, "user/index", data)
}

// onlineIPCount 对应 User::online_ip_count：最近 90 秒内连接过的不同 IP 数（不含节点自身的 IP）。
func (a *App) onlineIPCount(r *http.Request, uid uint32, now time.Time) (int, error) {
	ips, err := a.Q.ListAliveIPsSince(r.Context(), store.ListAliveIPsSinceParams{Userid: uid, Datetime: now.Unix() - 90})
	if err != nil {
		return 0, err
	}
	nodeIPs, err := a.nodeIPSet(r)
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	for _, x := range ips {
		ip := util.RealIP(x.Ip)
		if !nodeIPs[ip] {
			seen[ip] = true
		}
	}
	return len(seen), nil
}

// nodeIPSet 节点 IP 集合（原版 Node::where('node_ip', $ip) 精确比较整个字段）。
func (a *App) nodeIPSet(r *http.Request) (map[string]bool, error) {
	list, err := a.Q.ListNodeIPs(r.Context())
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(list))
	for _, ip := range list {
		set[ip] = true
	}
	return set, nil
}

// checkin 对应 UserController::doCheckIn。
func (a *App) checkin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	if err := r.ParseForm(); err != nil {
		a.json(w, fail("非法输入"))
		return
	}
	if !a.Captcha.Verify(ctx, captcha.Checkin, r.Form, server.ClientIP(r)) {
		a.json(w, fail("系统无法接受您的验证结果，请刷新页面后重试。"))
		return
	}
	now := time.Now()
	if u.ExpireIn.Before(now) {
		a.json(w, fail("您的账户已过期，无法签到。"))
		return
	}
	if !u.IsAbleToCheckin(now) {
		a.json(w, fail("您似乎已经签到过了..."))
		return
	}
	traffic := randomInt(int64(a.Cfg.Users.CheckinMin), int64(a.Cfg.Users.CheckinMax))
	y, m, d := now.In(a.Loc).Date()
	todayStart := time.Date(y, m, d, 0, 0, 0, 0, a.Loc).Unix()
	// 原版先判断再写入，并发请求可以重复签到；这里把“今天未签到”作为更新条件（K24）。
	n, err := a.Q.CheckinUser(ctx, store.CheckinUserParams{
		TransferEnable: traffic * util.MB, LastCheckInTime: now.Unix(), ID: u.ID, LastCheckInTime_2: todayStart,
	})
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	if n == 0 {
		a.json(w, fail("您似乎已经签到过了..."))
		return
	}
	nu, err := a.Q.GetUser(ctx, u.ID)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	mu := model.NewUser(nu, a.Loc)
	a.json(w, Ret{
		"msg":           fmt.Sprintf("获得了 %d MB流量.", traffic),
		"unflowtraffic": nu.TransferEnable,
		"traffic":       util.FlowAutoShow(float64(nu.TransferEnable)),
		"trafficInfo": map[string]string{
			"todayUsedTraffic": mu.TodayUsedTraffic(),
			"lastUsedTraffic":  mu.LastUsedTraffic(),
			"unUsedTraffic":    mu.UnusedTraffic(),
		},
		"ret": 1,
	})
}

// randomInt 返回 [lo, hi] 内的随机整数（原版 random_int）。
func randomInt(lo, hi int64) int64 {
	if hi <= lo {
		return lo
	}
	n, err := rand.Int(rand.Reader, big.NewInt(hi-lo+1))
	if err != nil {
		return lo
	}
	return lo + n.Int64()
}

// announcement 对应 UserController::announcement。
func (a *App) announcement(w http.ResponseWriter, r *http.Request) {
	anns, err := a.Q.ListAnnouncements(r.Context())
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	u := auth.User(r)
	rows := make([]map[string]any, 0, len(anns))
	for _, x := range anns {
		rows = append(rows, map[string]any{"ID": x.ID, "Date": u.FormatTime(x.Date), "Content": template.HTML(x.Content)}) //nolint:gosec // 公告由管理员编写，原版同样不转义
	}
	a.userPage(w, r, "user/announcement", map[string]any{"anns": rows})
}

// donate 对应 UserController::donate。
func (a *App) donate(w http.ResponseWriter, r *http.Request) {
	if !a.Cfg.Display.EnableDonate {
		// 原版 exit(0)：返回空白页面。
		return
	}
	ctx := r.Context()
	total, err := a.Q.CountDonateCodes(ctx)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p := newPager(r, "/user/donate", 15, total)
	codes, err := a.Q.ListDonateCodes(ctx, store.ListDonateCodesParams{Limit: p.Limit(), Offset: p.Offset()})
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	ids := make([]uint32, 0, len(codes))
	for _, c := range codes {
		ids = append(ids, c.Userid)
	}
	users, err := a.usersByID(r, ids)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	u := auth.User(r)
	rows := make([]map[string]any, 0, len(codes))
	for _, c := range codes {
		row := map[string]any{"ID": c.ID, "Type": c.Type, "Number": c.Number, "Code": c.Code, "Usedatetime": u.FormatTime(c.Usedatetime), "User": nil}
		if cu, ok := users[c.Userid]; ok {
			row["User"] = cu
		}
		rows = append(rows, row)
	}
	p.Items = rows
	in, err := a.Q.SumUsedCodesByType(ctx, -1)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	out, err := a.Q.SumUsedCodesByType(ctx, -2)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.userPage(w, r, "user/donate", map[string]any{"codes": p, "total_in": in, "total_out": out})
}

// usersByID 批量读取用户。
func (a *App) usersByID(r *http.Request, ids []uint32) (map[uint32]store.User, error) {
	out := map[uint32]store.User{}
	if len(ids) == 0 {
		return out, nil
	}
	list, err := a.Q.ListUsersByIDs(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	for _, x := range list {
		out[x.ID] = x
	}
	return out, nil
}

// nodeMap 读取全部节点，按 ID 索引。
func (a *App) nodeMap(r *http.Request) (map[uint32]store.SsNode, error) {
	list, err := a.Q.ListNodes(r.Context())
	if err != nil {
		return nil, err
	}
	out := make(map[uint32]store.SsNode, len(list))
	for _, n := range list {
		out[n.ID] = n
	}
	return out, nil
}

// trafficLog 对应 UserController::trafficLog：最近 3 天的流量记录。
func (a *App) trafficLog(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r)
	logs, err := a.Q.ListUserTrafficLogsSince(r.Context(), store.ListUserTrafficLogsSinceParams{UserID: u.ID, LogTime: time.Now().Unix() - 3*86400})
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	nodes, err := a.nodeMap(r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	rows := make([]map[string]any, 0, len(logs))
	for _, x := range logs {
		rows = append(rows, map[string]any{
			"LogTime":      x.LogTime,
			"TotalUsedRaw": util.PHPRound(float64(x.U+x.D)/1024, 2), // 原版 number_format(..., 2, '.', '')，输出到脚本中作为数字
			"NodeName":     nodes[x.NodeID].Name,                    // 节点已删除时为空（原版 node() 返回 null）
		})
	}
	a.userPage(w, r, "user/trafficlog", map[string]any{"logs": rows})
}

// userLogout 对应 UserController::logout。
func (a *App) userLogout(w http.ResponseWriter, r *http.Request) {
	_ = a.Auth.Logout(r.Context())
	http.Redirect(w, r, "/", http.StatusFound)
}

// backToAdmin 对应 UserController::backtoadmin：管理员“以用户身份登录”后返回管理员身份。
func (a *App) backToAdmin(w http.ResponseWriter, r *http.Request) {
	local, err := a.Auth.BackToAdmin(r.Context())
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if local == "" {
		local = "/"
	}
	http.Redirect(w, r, local, http.StatusFound)
}

// resetURL 对应 UserController::resetURL：删除订阅令牌，下次访问时生成新的。
func (a *App) resetURL(w http.ResponseWriter, r *http.Request) {
	if err := a.Q.DeleteLinksByUser(r.Context(), auth.User(r).ID); err != nil {
		a.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/user", http.StatusFound)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
