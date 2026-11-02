package app

import (
	"context"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/auth"
	"github.com/SadNoo/SS-No-PHP/internal/dec"
	"github.com/SadNoo/SS-No-PHP/internal/notify"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// adminRoutes 注册管理后台路由（原版 config/routes.php 中的 /admin 分组）。
func (a *App) adminRoutes(r chi.Router) {
	r.Get("/", a.adminIndex)
	r.Get("/sys", a.adminIndex)
	r.Get("/profile", a.profile)
	r.Get("/logout", a.userLogout)
	r.Get("/trafficlog", a.adminTablePage("admin/trafficlog", newTable("trafficlog/ajax", nil,
		"id", "ID", "user_id", "用户ID", "user_name", "用户名", "node_name", "使用节点", "rate", "倍率",
		"origin_traffic", "实际使用流量", "traffic", "结算流量", "log_time", "记录时间")))
	r.Post("/trafficlog/ajax", a.adminTrafficLogAjax)

	a.adminNodeRoutes(r)
	a.adminTicketRoutes(r)
	a.adminRelayRoutes(r)
	a.adminShopRoutes(r)
	a.adminUserRoutes(r)

	// 公告
	r.Get("/announcement", a.adminTablePage("admin/announcement/index", newTable("announcement/ajax", []string{"op", "id", "date", "content"},
		"op", "操作", "id", "ID", "date", "日期", "content", "内容")))
	r.Get("/announcement/create", a.adminPage("admin/announcement/create"))
	r.Post("/announcement", a.adminAnnAdd)
	r.Get("/announcement/{id}/edit", a.adminAnnEdit)
	r.Put("/announcement/{id}", a.adminAnnUpdate)
	r.Delete("/announcement", a.adminAnnDelete)
	r.Post("/announcement/ajax", a.adminAnnAjax)

	// 审计
	r.Get("/detect", a.adminTablePage("admin/detect/index", newTable("detect/ajax", nil,
		"op", "操作", "id", "ID", "name", "名称", "text", "介绍", "regex", "正则表达式", "type", "类型")))
	r.Get("/detect/log", a.adminTablePage("admin/detect/log", newTable("log/ajax", nil,
		"id", "ID", "user_id", "用户ID", "user_name", "用户名", "node_id", "节点ID", "node_name", "节点名",
		"rule_id", "规则ID", "rule_name", "规则名", "rule_text", "规则描述", "rule_regex", "规则正则表达式",
		"rule_type", "规则类型", "datetime", "时间")))
	r.Get("/detect/create", a.adminPage("admin/detect/add"))
	r.Post("/detect", a.adminDetectSave)
	r.Get("/detect/{id}/edit", a.adminDetectEdit)
	r.Put("/detect/{id}", a.adminDetectSave)
	r.Delete("/detect", a.adminDetectDelete)
	r.Post("/detect/ajax", a.adminDetectAjax)
	r.Post("/detect/log/ajax", a.adminDetectLogAjax)

	// 命令下发
	r.Get("/auto", a.adminTablePage("admin/auto/index", newTable("auto/ajax", []string{"op", "id", "datetime", "type", "value"},
		"id", "ID", "datetime", "时间", "type", "类型", "value", "内容")))
	r.Get("/auto/create", a.adminPage("admin/auto/add"))
	r.Post("/auto", a.adminAutoAdd)
	r.Delete("/auto", a.adminAutoDelete)
	r.Post("/auto/ajax", a.adminAutoAjax)

	// IP
	r.Get("/block", a.adminTablePage("admin/ip/block", newTable("block/ajax", nil,
		"id", "ID", "name", "节点名称", "ip", "IP", "location", "归属地", "datetime", "时间")))
	r.Get("/unblock", a.adminTablePage("admin/ip/unblock", newTable("unblock/ajax", nil,
		"id", "ID", "userid", "用户ID", "user_name", "用户名", "ip", "IP", "location", "归属地", "datetime", "时间")))
	r.Post("/unblock", a.adminDoUnblock)
	r.Get("/login", a.adminTablePage("admin/ip/login", newTable("login/ajax", nil,
		"id", "ID", "userid", "用户ID", "user_name", "用户名", "ip", "IP", "location", "归属地", "datetime", "时间", "type", "类型")))
	r.Get("/alive", a.adminTablePage("admin/ip/alive", newTable("alive/ajax", nil,
		"id", "ID", "userid", "用户ID", "user_name", "用户名", "nodeid", "节点ID", "node_name", "节点名", "ip", "IP",
		"location", "归属地", "datetime", "时间", "is_node", "是否为中转连接")))
	r.Post("/block/ajax", a.adminBlockAjax)
	r.Post("/unblock/ajax", a.adminUnblockAjax)
	r.Post("/login/ajax", a.adminLoginAjax)
	r.Post("/alive/ajax", a.adminAliveAjax)

	// 充值码
	r.Get("/code", a.adminTablePage("admin/code/index", newTable("code/ajax", nil,
		"id", "ID", "code", "内容", "type", "类型", "number", "操作", "isused", "是否已经使用", "userid", "用户ID",
		"user_name", "用户名", "usedatetime", "使用时间")))
	r.Get("/code/create", a.adminPage("admin/code/add"))
	r.Post("/code", a.adminCodeAdd)
	r.Get("/donate/create", a.adminPage("admin/code/add_donate"))
	r.Post("/donate", a.adminDonateAdd)
	r.Post("/code/ajax", a.adminCodeAjax)

	// 优惠码、邀请
	r.Get("/coupon", a.adminTablePage("admin/coupon", newTable("coupon/ajax", nil,
		"id", "ID", "code", "优惠码", "expire", "过期时间", "shop", "限定商品ID", "credit", "额度", "onetime", "次数")))
	r.Post("/coupon", a.adminCouponAdd)
	r.Post("/coupon/ajax", a.adminCouponAjax)
	r.Get("/invite", a.adminTablePage("admin/invite", newTable("payback/ajax", nil,
		"id", "ID", "total", "原始金额", "event_user_id", "发起用户ID", "event_user_name", "发起用户名",
		"ref_user_id", "获利用户ID", "ref_user_name", "获利用户名", "ref_get", "获利金额", "datetime", "时间")))
	r.Post("/invite", a.adminAddInvite)
	r.Post("/payback/ajax", a.adminPaybackAjax)
}

func (a *App) adminPage(page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { a.userPage(w, r, page, nil) }
}

func (a *App) adminTablePage(page string, cfg tableConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.userPage(w, r, page, map[string]any{"table_config": cfg})
	}
}

// tg 在后台向 Telegram 管理群发送通知（原版 Telegram::Send / SendMarkdown）。
func (a *App) tg(text string, markdown bool) {
	t := notify.Telegram{Enable: a.Cfg.Telegram.Enable, Token: a.Cfg.Telegram.Token, ChatID: a.Cfg.Telegram.ChatID}
	if !t.Enable {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var err error
		if markdown {
			err = t.SendMarkdown(ctx, text)
		} else {
			err = t.Send(ctx, text)
		}
		if err != nil {
			a.Log.Warn("Telegram 通知发送失败", "err", err)
		}
	}()
}

// fmtUnix 把 Unix 时间（字符串）格式化为业务时区的 Y-m-d H:i:s（原版 date('Y-m-d H:i:s', $x)）。
func (a *App) fmtUnix(s string) string {
	v, _ := strconv.ParseInt(s, 10, 64)
	return util.ToDateTime(v, a.Loc)
}

// fmtDBTime 把数据库中的 UTC 时间字符串转为业务时区显示（保留 1989、2099 等特殊值不变）。
func (a *App) fmtDBTime(s string) string {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC)
	if err != nil {
		return s
	}
	if t.Year() < 2000 || t.Year() >= 2099 {
		return s
	}
	return t.In(a.Loc).Format("2006-01-02 15:04:05")
}

// nodeStatsAlive 是后台首页节点在线统计的心跳时限（秒，与原版相同）：心跳时间晚于当前时间减去这个值（不含相等）为在线。
const nodeStatsAlive = 90

// siteStats 对应原版 Services\Analytics（后台首页统计）。
type siteStats struct {
	users                                       store.AdminUserStatsRow
	nodes                                       store.AdminNodeStatsRow
	totalUser, checkin, todayCheckin, unused    int64
	online                                      map[int]int64
	rawToday, rawLast, rawUnused, rawTotal, sum int64
}

func (s *siteStats) GetTotalUser() int64             { return s.totalUser }
func (s *siteStats) GetCheckinUser() int64           { return s.checkin }
func (s *siteStats) GetTodayCheckinUser() int64      { return s.todayCheckin }
func (s *siteStats) GetUnusedUser() int64            { return s.unused }
func (s *siteStats) GetOnlineUser(sec int) int64     { return s.online[sec] }
func (s *siteStats) GetTotalNodes() int64            { return s.nodes.Total }
func (s *siteStats) GetAliveNodes() int64            { return s.nodes.Alive }
func (s *siteStats) GetTrafficUsage() string         { return util.FlowAutoShow(float64(s.sum)) }
func (s *siteStats) GetTodayTrafficUsage() string    { return util.FlowAutoShow(float64(s.rawToday)) }
func (s *siteStats) GetRawTodayTrafficUsage() int64  { return s.rawToday }
func (s *siteStats) GetLastTrafficUsage() string     { return util.FlowAutoShow(float64(s.rawLast)) }
func (s *siteStats) GetRawLastTrafficUsage() int64   { return s.rawLast }
func (s *siteStats) GetUnusedTrafficUsage() string   { return util.FlowAutoShow(float64(s.rawUnused)) }
func (s *siteStats) GetRawUnusedTrafficUsage() int64 { return s.rawUnused }
func (s *siteStats) GetTotalTraffic() string         { return util.FlowAutoShow(float64(s.rawTotal)) }
func (s *siteStats) GetRawTotalTraffic() int64       { return s.rawTotal }

// adminIndex 对应 AdminController::index。
func (a *App) adminIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	y, m, d := now.In(a.Loc).Date()
	us, err := a.Q.AdminUserStats(ctx, store.AdminUserStatsParams{
		Today:       time.Date(y, m, d, 0, 0, 0, 0, a.Loc).Unix(),
		Online60:    now.Unix() - 60,
		Online3600:  now.Unix() - 3600,
		Online86400: now.Unix() - 86400,
	})
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	ns, err := a.Q.AdminNodeStats(ctx, store.AdminNodeStatsParams{AliveAfter: now.Unix() - nodeStatsAlive, Sorts: activeNodeSortValues()})
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	s := &siteStats{
		users: us, nodes: ns, totalUser: us.Total, checkin: us.Checkin, todayCheckin: us.TodayCheckin, unused: us.Unused,
		online:    map[int]int64{60: us.Online60, 3600: us.Online3600, 86400: us.Online86400},
		sum:       us.SumU + us.SumD,
		rawToday:  us.SumU + us.SumD - us.SumLastDay,
		rawLast:   us.SumLastDay,
		rawUnused: us.SumTransfer - us.SumU - us.SumD,
		rawTotal:  us.SumTransfer,
	}
	a.userPage(w, r, "admin/index", map[string]any{"sts": s})
}

// adminTrafficLogAjax 对应 AdminController::ajax_trafficLog。
func (a *App) adminTrafficLogAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query: "SELECT log.id, log.user_id, user.user_name, node.name AS node_name, log.rate, (log.u + log.d) AS origin_traffic, log.traffic, log.log_time " +
			"FROM user_traffic_log AS log, user, ss_node AS node WHERE log.user_id = user.id AND log.node_id = node.id",
		Columns: []string{"id", "user_id", "user_name", "node_name", "rate", "origin_traffic", "traffic", "log_time"},
		Format: func(row map[string]string) map[string]string {
			v, _ := strconv.ParseFloat(row["origin_traffic"], 64)
			return map[string]string{"log_time": a.fmtUnix(row["log_time"]), "origin_traffic": util.FlowAutoShow(v)}
		},
	})
}

// ---- 公告（Admin\AnnController）----

func (a *App) adminAnnAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query:   "SELECT id AS op, id, `date`, content FROM announcement",
		Columns: []string{"op", "id", "date", "content"},
		HTML:    []string{"op", "content"}, // 公告由管理员编写，原样显示
		RowID:   true,
		Format: func(row map[string]string) map[string]string {
			return map[string]string{
				"op":   editButton("/admin/announcement/"+row["id"]+"/edit") + "\n" + callButton("btn-brand-accent", "delete_modal_show", row["id"], "删除", false),
				"date": a.fmtDBTime(row["date"]),
			}
		},
	})
}

// adminAnnAdd 对应 AnnController::add：发布公告，可选按等级分批发送邮件（每次请求发送一页，前端循环调用）。
func (a *App) adminAnnAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	content, markdown := param(r, "content"), param(r, "markdown")
	page, _ := strconv.Atoi(param(r, "page"))
	issend, pushBear := param(r, "issend") == "1", param(r, "PushBear") == "1"
	vip, _ := strconv.ParseInt(param(r, "vip"), 10, 32)
	subject := a.Cfg.App.Name + "-公告"
	if page == 1 {
		if err := a.Q.InsertAnnouncement(ctx, store.InsertAnnouncementParams{Date: time.Now().UTC(), Content: content, Markdown: markdown}); err != nil {
			a.json(w, fail("添加失败"))
			return
		}
	}
	if pushBear && page == 1 {
		// 原版 PushBear（pushbear.ftqq.com）服务已关闭，这里仅保留选项，不再推送。
		a.Log.Info("PushBear 服务已停止，跳过推送")
	}
	if issend {
		limit := max(a.Cfg.Mail.SendPageLimit, 1)
		users, err := a.Q.ListUsersForMail(ctx, store.ListUsersForMailParams{Class: int32(vip), Limit: int32(limit), Offset: int32(max(page-1, 0) * limit)}) //nolint:gosec // 分页参数
		if err != nil {
			a.jsonError(w, r, err)
			return
		}
		for _, u := range users {
			if !strings.Contains(u.Email, "@") {
				continue
			}
			// 公告内容由管理员编写，原样作为 HTML 放入邮件。
			if err := a.Mail.Send(ctx, u.Email, subject, "warn", map[string]any{"user": u, "text": template.HTML(content)}); err != nil { //nolint:gosec // 管理员内容
				a.Log.Warn("发送公告邮件失败", "to", u.Email, "err", err)
			}
		}
		if len(users) == limit {
			a.json(w, Ret{"ret": 2, "msg": page + 1})
			return
		}
	}
	a.tg("新公告："+"\n"+markdown, true)
	msg := "公告添加成功"
	switch {
	case issend && pushBear:
		msg = "公告添加成功，邮件发送和PushBear推送成功"
	case issend:
		msg = "公告添加成功，邮件发送成功"
	case pushBear:
		msg = "公告添加成功，PushBear推送成功"
	}
	a.json(w, ok(msg))
}

func (a *App) adminAnnEdit(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(chi.URLParam(r, "id"), 10, 32)
	ann, err := a.Q.GetAnnouncement(r.Context(), uint32(id))
	if isNoRows(err) {
		a.Srv.NotFound(w, r)
		return
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.userPage(w, r, "admin/announcement/edit", map[string]any{"ann": ann})
}

func (a *App) adminAnnUpdate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(chi.URLParam(r, "id"), 10, 32)
	n, err := a.Q.UpdateAnnouncement(r.Context(), store.UpdateAnnouncementParams{
		Content: param(r, "content"), Markdown: param(r, "markdown"), Date: time.Now().UTC(), ID: uint32(id),
	})
	if err != nil || n == 0 {
		a.json(w, fail("修改失败"))
		return
	}
	a.tg("公告更新："+"\n"+param(r, "markdown"), true)
	a.json(w, ok("修改成功"))
}

func (a *App) adminAnnDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(param(r, "id"), 10, 32)
	if n, err := a.Q.DeleteAnnouncement(r.Context(), uint32(id)); err != nil || n == 0 {
		a.json(w, fail("删除失败"))
		return
	}
	a.json(w, ok("删除成功"))
}

// ---- 审计规则（Admin\DetectController）----

func detectTypeName(t string) string {
	if t == "1" {
		return "数据包明文匹配"
	}
	return "数据包十六进制匹配"
}

func (a *App) adminDetectAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query:   "SELECT id AS op, id, name, text, regex, type FROM detect_list",
		Columns: []string{"op", "id", "name", "text", "regex", "type"},
		HTML:    []string{"op"},
		Format: func(row map[string]string) map[string]string {
			return map[string]string{
				"op":   editButton("/admin/detect/"+row["id"]+"/edit") + "\n" + callButton("btn-brand-accent", "delete_modal_show", row["id"], "删除", false),
				"type": detectTypeName(row["type"]),
			}
		},
	})
}

func (a *App) adminDetectLogAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query: "SELECT detect_log.id, user_id, user.user_name, node_id, node.name AS node_name, list.id AS rule_id, list.name AS rule_name, " +
			"list.text AS rule_text, list.regex AS rule_regex, list.type AS rule_type, detect_log.datetime " +
			"FROM detect_log, user, ss_node AS node, detect_list AS list WHERE user.id = detect_log.user_id AND node.id = detect_log.node_id AND list.id = detect_log.list_id",
		Columns: []string{"id", "user_id", "user_name", "node_id", "node_name", "rule_id", "rule_name", "rule_text", "rule_regex", "rule_type", "datetime"},
		Format: func(row map[string]string) map[string]string {
			return map[string]string{"rule_type": detectTypeName(row["rule_type"]), "datetime": a.fmtUnix(row["datetime"])}
		},
	})
}

func (a *App) adminDetectEdit(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	rule, err := a.Q.GetDetectRule(r.Context(), id)
	if isNoRows(err) {
		a.Srv.NotFound(w, r)
		return
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.userPage(w, r, "admin/detect/edit", map[string]any{"rule": rule})
}

// adminDetectSave 对应 DetectController::add / update。
func (a *App) adminDetectSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name, text, regex := param(r, "name"), param(r, "text"), param(r, "regex")
	typ, _ := strconv.ParseInt(param(r, "type"), 10, 32)
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		id, _ := strconv.ParseUint(idStr, 10, 64)
		if n, err := a.Q.UpdateDetectRule(ctx, store.UpdateDetectRuleParams{Name: name, Text: text, Regex: regex, Type: int32(typ), ID: id}); err != nil || n == 0 {
			a.json(w, fail("修改失败"))
			return
		}
		a.tg("规则更新："+"\n"+name, true)
		a.json(w, ok("修改成功"))
		return
	}
	if err := a.Q.InsertDetectRule(ctx, store.InsertDetectRuleParams{Name: name, Text: text, Regex: regex, Type: int32(typ)}); err != nil {
		a.json(w, fail("添加失败"))
		return
	}
	a.tg("有新的审计规则："+name, true)
	a.json(w, ok("添加成功"))
}

func (a *App) adminDetectDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(param(r, "id"), 10, 64)
	if n, err := a.Q.DeleteDetectRule(r.Context(), id); err != nil || n == 0 {
		a.json(w, fail("删除失败"))
		return
	}
	a.json(w, ok("删除成功"))
}

// ---- 命令下发（Admin\AutoController）----

func (a *App) adminAutoAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query:   "SELECT id, `datetime`, type, value FROM auto",
		Columns: []string{"id", "datetime", "type", "value"},
		Format: func(row map[string]string) map[string]string {
			t := "命令被执行"
			if row["type"] == "1" {
				t = "命令下发"
			}
			return map[string]string{"datetime": a.fmtUnix(row["datetime"]), "type": t}
		},
	})
}

func (a *App) adminAutoAdd(w http.ResponseWriter, r *http.Request) {
	if err := a.Q.InsertAuto(r.Context(), store.InsertAutoParams{Type: 1, Value: param(r, "content"), Sign: param(r, "sign"), Datetime: time.Now().Unix()}); err != nil {
		a.json(w, fail("添加失败"))
		return
	}
	a.json(w, ok("添加成功"))
}

func (a *App) adminAutoDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(param(r, "id"), 10, 64)
	if n, err := a.Q.DeleteAuto(r.Context(), id); err != nil || n == 0 {
		a.json(w, fail("删除失败"))
		return
	}
	a.json(w, ok("删除成功"))
}

// ---- IP（Admin\IpController）----

func (a *App) adminBlockAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query:   "SELECT blockip.id, node.name, blockip.ip, blockip.ip AS location, `datetime` FROM blockip, ss_node AS node WHERE blockip.nodeid = node.id",
		Columns: []string{"id", "name", "ip", "location", "datetime"},
		Format: func(row map[string]string) map[string]string {
			return map[string]string{"datetime": a.fmtUnix(row["datetime"]), "location": a.Geo.Lookup(row["location"])}
		},
	})
}

func (a *App) adminUnblockAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query:   "SELECT unblockip.id, userid, user.user_name, unblockip.ip, unblockip.ip AS location, `datetime` FROM unblockip, user WHERE unblockip.userid = user.id",
		Columns: []string{"id", "userid", "user_name", "ip", "location", "datetime"},
		Format: func(row map[string]string) map[string]string {
			return map[string]string{"datetime": a.fmtUnix(row["datetime"]), "location": a.Geo.Lookup(row["location"])}
		},
	})
}

func (a *App) adminLoginAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query: "SELECT login_ip.id, login_ip.userid, user.user_name, login_ip.ip, login_ip.ip AS location, login_ip.datetime, login_ip.type " +
			"FROM login_ip, user WHERE login_ip.userid = user.id",
		Columns: []string{"id", "userid", "user_name", "ip", "location", "datetime", "type"},
		Format: func(row map[string]string) map[string]string {
			t := "失败"
			if row["type"] == "0" {
				t = "成功"
			}
			return map[string]string{"datetime": a.fmtUnix(row["datetime"]), "location": a.Geo.Lookup(row["location"]), "type": t}
		},
	})
}

func (a *App) adminAliveAjax(w http.ResponseWriter, r *http.Request) {
	nodeIPs, err := a.nodeIPSet(r)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.dataTable(w, r, tableSpec{
		Query: "SELECT alive_ip.id, alive_ip.userid, user.user_name, alive_ip.nodeid, ss_node.name AS node_name, alive_ip.ip, alive_ip.ip AS location, " +
			"alive_ip.datetime, alive_ip.id AS is_node FROM alive_ip, user, ss_node " +
			"WHERE alive_ip.userid = user.id AND alive_ip.nodeid = ss_node.id AND `datetime` > ?",
		Args:    []any{time.Now().Unix() - 60},
		Columns: []string{"id", "userid", "user_name", "nodeid", "node_name", "ip", "location", "datetime", "is_node"},
		Format: func(row map[string]string) map[string]string {
			ip := util.RealIP(row["ip"])
			isNode := "否"
			if nodeIPs[ip] {
				isNode = "是"
			}
			return map[string]string{"datetime": a.fmtUnix(row["datetime"]), "ip": ip, "location": a.Geo.Lookup(ip), "is_node": isNode}
		},
	})
}

// adminDoUnblock 对应 IpController::doUnblock。
func (a *App) adminDoUnblock(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := strings.TrimSpace(param(r, "ip"))
	err := a.tx(ctx, func(q *store.Queries) error {
		if err := deleteBlocksForIP(ctx, q, ip); err != nil { // 与用户中心相同：同一 IP 的所有写法一并删除
			return err
		}
		return q.InsertUnblockIP(ctx, store.InsertUnblockIPParams{Userid: auth.User(r).ID, Ip: limitLen(ip, 255), Datetime: time.Now().Unix()})
	})
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("发送解封命令解封 "+ip+" 成功"))
}

// ---- 充值码（Admin\CodeController）----

func (a *App) adminCodeAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query: "SELECT code.id, code.code, code.type, code.number, code.isused, code.userid, " +
			"COALESCE((SELECT user_name FROM user WHERE user.id = code.userid), '未使用') AS user_name, code.usedatetime FROM code",
		Columns: []string{"id", "code", "type", "number", "isused", "userid", "user_name", "usedatetime"},
		Format: func(row map[string]string) map[string]string {
			out := map[string]string{}
			switch row["type"] {
			case "-1":
				out["number"], out["type"] = "充值 "+row["number"]+" 元", "充值金额"
			case "-2":
				out["number"], out["type"] = "支出 "+row["number"]+" 元", "财务支出"
			default:
				out["number"], out["type"] = "已经废弃", "已经废弃"
			}
			out["isused"] = "未使用"
			if row["isused"] == "1" {
				out["isused"] = "已使用"
			}
			if row["userid"] == "0" {
				out["userid"] = "未使用"
			}
			if row["usedatetime"] > "2000-01-01 00:00:00" {
				out["usedatetime"] = a.fmtDBTime(row["usedatetime"])
			} else {
				out["usedatetime"] = "未使用"
			}
			return out
		},
	})
}

// codeUnusedTime 是未使用充值码的 usedatetime（原版 '1989:06:04 02:30:00'）。
var codeUnusedTime = time.Date(1989, 6, 4, 2, 30, 0, 0, time.UTC)

// adminCodeAdd 对应 CodeController::add：批量生成余额充值码。
func (a *App) adminCodeAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nStr := param(r, "amount")
	n, err := strconv.Atoi(nStr)
	if !util.IsInt(nStr) || err != nil || n < 0 || n > 10000 {
		a.json(w, fail("非法请求"))
		return
	}
	number, err := dec.Parse(param(r, "number"))
	if err != nil {
		a.json(w, fail("非法请求"))
		return
	}
	err = a.tx(ctx, func(q *store.Queries) error {
		for i := 0; i < n; i++ {
			code := strconv.FormatInt(time.Now().Unix(), 10) + util.RandomChar(32)
			if err := q.InsertCode(ctx, store.InsertCodeParams{Code: code, Type: -1, Number: number, Isused: 0, Userid: 0, Usedatetime: codeUnusedTime}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("充值码添加成功"))
}

// adminDonateAdd 对应 CodeController::donate_add：记录捐赠收入或财务支出。
func (a *App) adminDonateAdd(w http.ResponseWriter, r *http.Request) {
	amount, err := dec.Parse(param(r, "amount"))
	typ, terr := strconv.ParseInt(param(r, "type"), 10, 32)
	if err != nil || terr != nil {
		a.json(w, fail("非法请求"))
		return
	}
	if err := a.Q.InsertCode(r.Context(), store.InsertCodeParams{
		Code: param(r, "code"), Type: int32(typ), Number: amount, Isused: 1, Userid: auth.User(r).ID, Usedatetime: time.Now().UTC(),
	}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("添加成功"))
}

// ---- 优惠码、邀请（AdminController）----

func (a *App) adminCouponAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query:   "SELECT id, code, expire, shop, credit, onetime FROM coupon",
		Columns: []string{"id", "code", "expire", "shop", "credit", "onetime"},
		Format: func(row map[string]string) map[string]string {
			return map[string]string{"expire": a.fmtUnix(row["expire"])}
		},
	})
}

// adminCouponAdd 对应 AdminController::addCoupon。generate_type：1 指定优惠码，2 随机，3 指定前缀 + 随机。
func (a *App) adminCouponAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	genType, final := param(r, "generate_type"), param(r, "prefix")
	if final == "" && (genType == "1" || genType == "3") {
		a.json(w, fail("优惠码不能为空"))
		return
	}
	if genType == "1" {
		if n, err := a.Q.CountCouponCode(ctx, final); err != nil {
			a.jsonError(w, r, err)
			return
		} else if n != 0 {
			a.json(w, fail("优惠码已存在"))
			return
		}
	} else {
		for i := 0; ; i++ {
			code := util.RandomChar(8)
			if genType == "3" {
				code = final + code
			}
			n, err := a.Q.CountCouponCode(ctx, code)
			if err != nil {
				a.jsonError(w, r, err)
				return
			}
			if n == 0 {
				final = code
				break
			}
			if i > 20 {
				a.json(w, fail("生成优惠码失败"))
				return
			}
		}
	}
	onetime, _ := strconv.ParseInt(param(r, "onetime"), 10, 32)
	credit, _ := strconv.ParseInt(param(r, "credit"), 10, 32)
	hours, _ := strconv.ParseFloat(param(r, "expire"), 64)
	if err := a.Q.InsertCoupon(ctx, store.InsertCouponParams{
		Code: final, Onetime: int32(onetime), Expire: time.Now().Unix() + int64(hours*3600), Shop: param(r, "shop"), Credit: int32(credit),
	}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("优惠码添加成功"))
}

func (a *App) adminPaybackAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query: "SELECT payback.id, payback.total, payback.userid AS event_user_id, event_user.user_name AS event_user_name, " +
			"payback.ref_by AS ref_user_id, ref_user.user_name AS ref_user_name, payback.ref_get, payback.datetime " +
			"FROM payback, user AS event_user, user AS ref_user WHERE event_user.id = payback.userid AND ref_user.id = payback.ref_by",
		Columns: []string{"id", "total", "event_user_id", "event_user_name", "ref_user_id", "ref_user_name", "ref_get", "datetime"},
		Format: func(row map[string]string) map[string]string {
			return map[string]string{"datetime": a.fmtUnix(row["datetime"])}
		},
	})
}

// adminAddInvite 对应 AdminController::addInvite：给指定用户（ID 或邮箱）增加邀请次数。
// 原版填 0 时会因变量未定义而报错，这里提示检查输入。
func (a *App) adminAddInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	numStr := param(r, "num")
	num, err := strconv.ParseInt(numStr, 10, 32)
	if !util.IsInt(numStr) || err != nil {
		a.json(w, fail("非法请求"))
		return
	}
	uid := strings.TrimSpace(param(r, "uid"))
	var u store.User
	if strings.Contains(uid, "@") {
		u, err = a.Q.GetUserByEmail(ctx, uid)
	} else {
		id, perr := strconv.ParseUint(uid, 10, 32)
		if perr != nil || id == 0 {
			err = errMsg("用户不存在")
		} else {
			u, err = a.Q.GetUser(ctx, uint32(id))
		}
	}
	if err != nil {
		a.json(w, fail("邀请次数添加失败，检查用户id或者用户邮箱是否输入正确"))
		return
	}
	if err := a.Q.SetUserInviteNum(ctx, store.SetUserInviteNumParams{InviteNum: int32(num), ID: u.ID}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("邀请次数添加成功"))
}
