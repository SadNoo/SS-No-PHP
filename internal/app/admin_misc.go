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
	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/sub"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// ---- 工单（Admin\TicketController）----

func (a *App) adminTicketRoutes(r chi.Router) {
	r.Get("/ticket", a.adminTablePage("admin/ticket/index", newTable("ticket/ajax", nil,
		"op", "操作", "id", "ID", "datetime", "时间", "title", "标题", "userid", "用户ID", "user_name", "用户名", "status", "状态")))
	r.Get("/ticket/{id}/view", a.adminTicketView)
	r.Put("/ticket/{id}", a.adminTicketUpdate)
	r.Post("/ticket/ajax", a.adminTicketAjax)
}

func (a *App) adminTicketAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query:   "SELECT ticket.id AS op, ticket.id, ticket.datetime, ticket.title, ticket.userid, user.user_name, ticket.status FROM ticket, user WHERE ticket.userid = user.id AND ticket.rootid = 0",
		Columns: []string{"op", "id", "datetime", "title", "userid", "user_name", "status"},
		HTML:    []string{"op"},
		Format: func(row map[string]string) map[string]string {
			st := "关闭"
			if row["status"] == "1" {
				st = "开启"
			}
			return map[string]string{
				"op":       `<a class="btn btn-brand" href="/admin/ticket/` + template.HTMLEscapeString(row["id"]) + `/view">查看</a>`,
				"datetime": a.fmtUnix(row["datetime"]), "status": st,
			}
		},
	})
}

func (a *App) adminTicketView(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	main, err := a.Q.GetRootTicket(ctx, id)
	if isNoRows(err) {
		a.Srv.NotFound(w, r)
		return
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}
	total, err := a.Q.CountTicketThread(ctx, store.CountTicketThreadParams{ID: id})
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p := newPager(r, "/admin/ticket/"+strconv.FormatUint(id, 10)+"/view", 5, total)
	list, err := a.Q.ListTicketThread(ctx, store.ListTicketThreadParams{ID: id, Limit: p.Limit(), Offset: p.Offset()})
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	ids := make([]uint32, 0, len(list))
	for _, t := range list {
		ids = append(ids, t.Userid)
	}
	users, err := a.usersByID(r, ids)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	rows := make([]map[string]any, 0, len(list))
	for _, t := range list {
		var tu *model.User
		if su, ok := users[t.Userid]; ok {
			tu = model.NewUser(su, a.Loc)
		}
		rows = append(rows, map[string]any{
			"ID": t.ID, "User": tu, "Datetime": util.ToDateTime(t.Datetime, a.Loc),
			"Content": template.HTML(sanitizeTicket(t.Content)), //nolint:gosec // 已经过 HTML 过滤
		})
	}
	p.Items = rows
	a.userPage(w, r, "admin/ticket/view", map[string]any{"ticketset": p, "id": id, "owner_id": main.Userid})
}

// adminTicketUpdate 对应 Admin\TicketController::update：管理员回复工单，并邮件通知工单所有者。
func (a *App) adminTicketUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	content := param(r, "content")
	status, valid := zeroOne(param(r, "status"))
	if content == "" || !valid {
		a.json(w, fail("请填全"))
		return
	}
	if badTicketWords(content) {
		a.json(w, fail("请求中有不正当的词语。"))
		return
	}
	id, _ := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	main, err := a.Q.GetRootTicket(ctx, id)
	if err != nil {
		a.json(w, fail("工单不存在"))
		return
	}
	if owner, err := a.Q.GetUser(ctx, main.Userid); err == nil {
		link := template.HTMLEscapeString(a.Cfg.App.BaseURL + "/user/ticket/" + strconv.FormatUint(main.ID, 10) + "/view")
		text := template.HTML(`您好，有人回复了<a href="` + link + `">工单</a>，请您查看。`) //nolint:gosec // 链接已转义
		go func() {                                                          //nolint:gosec // 邮件在后台发送，不随请求结束而取消
			mctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := a.Mail.Send(mctx, owner.Email, a.Cfg.App.Name+"-工单被回复", "warn", map[string]any{"user": owner, "text": text}); err != nil {
				a.Log.Warn("发送工单回复邮件失败", "err", err)
			}
		}()
	}
	err = a.tx(ctx, func(q *store.Queries) error {
		if err := q.InsertTicket(ctx, store.InsertTicketParams{
			Title: sanitizeTicket(main.Title), Content: sanitizeTicket(content), Rootid: main.ID, Userid: auth.User(r).ID, Datetime: time.Now().Unix(),
		}); err != nil {
			return err
		}
		return q.UpdateTicketStatus(ctx, store.UpdateTicketStatusParams{Status: status, ID: main.ID})
	})
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("提交成功"))
}

// ---- 中转规则（Admin\RelayController）----

func (a *App) adminRelayRoutes(r chi.Router) {
	r.Get("/relay", a.adminTablePage("admin/relay/index", newTable("relay/ajax", nil,
		"op", "操作", "id", "ID", "user_id", "用户ID", "user_name", "用户名", "source_node_name", "起源节点",
		"dist_node_name", "目标节点", "port", "端口", "priority", "优先级")))
	r.Get("/relay/create", a.adminRelayForm)
	r.Post("/relay", a.adminRelaySave)
	r.Get("/relay/{id}/edit", a.adminRelayForm)
	r.Put("/relay/{id}", a.adminRelaySave)
	r.Delete("/relay", a.adminRelayDelete)
	r.Get("/relay/path_search/{id}", a.adminRelayPathSearch)
	r.Post("/relay/ajax", a.adminRelayAjax)
}

func (a *App) adminRelayAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query: "SELECT relay.id AS op, relay.id, relay.user_id, COALESCE(user.user_name, '') AS user_name, " +
			"COALESCE(source_node.name, '') AS source_node_name, COALESCE(dist_node.name, '') AS dist_node_name, relay.port, relay.priority " +
			"FROM relay LEFT JOIN user ON user.id = relay.user_id LEFT JOIN ss_node AS source_node ON source_node.id = relay.source_node_id " +
			"LEFT JOIN ss_node AS dist_node ON dist_node.id = relay.dist_node_id",
		Columns: []string{"op", "id", "user_id", "user_name", "source_node_name", "dist_node_name", "port", "priority"},
		HTML:    []string{"op"},
		RowID:   true,
		Format: func(row map[string]string) map[string]string {
			out := map[string]string{
				"op": editButton("/admin/relay/"+row["id"]+"/edit") + "\n" + callButton("btn-brand-accent", "delete_modal_show", row["id"], "删除", false),
			}
			if row["user_id"] == "0" {
				out["user_name"] = "全体用户"
			}
			return out
		},
	})
}

// adminRelayForm 对应 Admin\RelayController::create / edit。
func (a *App) adminRelayForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, err := a.Q.ListNodesByName(ctx)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	type opt struct {
		ID   uint32
		Name string
	}
	var sources, dists []opt
	idStr := chi.URLParam(r, "id")
	data := map[string]any{}
	if idStr != "" {
		id, _ := strconv.ParseUint(idStr, 10, 64)
		rule, err := a.Q.GetRelayRule(ctx, id)
		if err != nil {
			return // 原版 exit(0)
		}
		data["rule"] = rule
	}
	for _, n := range all {
		switch n.Sort {
		case sub.SortRelay, sub.SortV2RayRelay:
			name := n.Name
			if n.Sort == sub.SortV2RayRelay {
				name += " 正在使用V2ray后端 "
			}
			sources = append(sources, opt{n.ID, name})
		}
		switch n.Sort {
		case sub.SortSS, sub.SortRelay, sub.SortV2Ray, sub.SortV2RayRelay:
			name := n.Name
			if n.Sort == sub.SortV2Ray || n.Sort == sub.SortV2RayRelay {
				name += " 如果是V2ray后端 请设置成"
				if idStr == "" { // 原版添加页与编辑页的文字相差一个空格
					name += " "
				}
				name += sub.SSV2Array(n.Server).Str("port")
			} else {
				name += " 如果是V2ray后端 请不要设置，用户页面设置 "
			}
			dists = append(dists, opt{n.ID, name})
		}
	}
	data["source_nodes"], data["dist_nodes"] = sources, dists
	page := "admin/relay/add"
	if idStr != "" {
		page = "admin/relay/edit"
	}
	a.userPage(w, r, page, data)
}

// adminRelaySave 对应 Admin\RelayController::add / update。
// 原版添加时选择“不进行中转”（-1）会因找不到节点而失败，这里与用户端一致允许阻断规则。
func (a *App) adminRelaySave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodes, err := a.Q.ListNodes(ctx)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	byID := map[int32]store.SsNode{}
	for _, n := range nodes {
		byID[int32(n.ID)] = n //nolint:gosec // 节点 ID 范围很小
	}
	idStr := chi.URLParam(r, "id")
	var existing *store.Relay
	if idStr != "" {
		id, _ := strconv.ParseUint(idStr, 10, 64)
		rule, err := a.Q.GetRelayRule(ctx, id)
		if err != nil {
			return
		}
		existing = &rule
	}
	sourceID, distID := atoi32(param(r, "source_node")), atoi32(param(r, "dist_node"))
	source, found := byID[sourceID]
	if !found && (existing == nil || sourceID != 0) {
		if existing == nil {
			a.json(w, fail("起源节点错误。"))
		} else {
			a.json(w, fail("起源节点 ID 错误。"))
		}
		return
	}
	if existing == nil && source.Sort == sub.SortV2RayRelay {
		if list, err := a.Q.ListRelayBySource(ctx, sourceID); err != nil {
			a.jsonError(w, r, err)
			return
		} else if len(list) > 0 {
			a.json(w, fail("v2ray中转一个起点一个rule"))
			return
		}
	}
	dist, found := byID[distID]
	if distID == -1 {
		dist, found = store.SsNode{NodeIp: "0.0.0.0", Sort: sub.SortRelay}, true
	}
	if !found {
		if existing == nil {
			a.json(w, fail("目标节点错误。"))
		} else {
			a.json(w, fail("目标节点 ID 错误。"))
		}
		return
	}
	userID, _ := strconv.ParseUint(param(r, "user_id"), 10, 32)
	rule := store.Relay{
		UserID: uint32(userID), SourceNodeID: sourceID, DistNodeID: distID, DistIp: relayDistIP(source, dist.NodeIp),
		Port: atoi32(param(r, "port")), Priority: atoi32(param(r, "priority")),
	}
	var ruleset []store.Relay
	if userID == 0 {
		ruleset, err = a.Q.ListAllRelayRules(ctx)
	} else {
		ruleset, err = a.Q.ListRelayRulesVisible(ctx, uint32(userID))
	}
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	editID := uint64(0)
	if existing != nil {
		rule.ID, editID = existing.ID, existing.ID
	}
	if cid := relayConflict(rule, ruleset, editID, rule.SourceNodeID, byID, map[uint64]bool{}); cid != 0 {
		if cid == -1 {
			a.json(w, fail("您即将添加的规则可能会造成冲突！"))
		} else {
			a.json(w, fail("您即将添加的规则与规则 ID:"+strconv.FormatInt(cid, 10)+" 冲突！"))
		}
		return
	}
	if existing == nil {
		err = a.Q.AdminInsertRelayRule(ctx, store.AdminInsertRelayRuleParams{
			UserID: rule.UserID, SourceNodeID: rule.SourceNodeID, DistNodeID: rule.DistNodeID, DistIp: rule.DistIp, Port: rule.Port, Priority: rule.Priority,
		})
	} else {
		_, err = a.Q.AdminUpdateRelayRule(ctx, store.AdminUpdateRelayRuleParams{
			UserID: rule.UserID, SourceNodeID: rule.SourceNodeID, DistNodeID: rule.DistNodeID, DistIp: rule.DistIp, Port: rule.Port, Priority: rule.Priority, ID: rule.ID,
		})
	}
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	if existing == nil {
		a.json(w, ok("添加成功"))
	} else {
		a.json(w, ok("修改成功"))
	}
}

func (a *App) adminRelayDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(param(r, "id"), 10, 64)
	n, err := a.Q.DeleteRelayRule(r.Context(), id)
	if err != nil {
		a.json(w, fail("删除失败"))
		return
	}
	if n == 0 {
		return
	}
	a.json(w, ok("删除成功"))
}

// adminRelayPathSearch 对应 Admin\RelayController::path_search：查看某个用户的中转链路。
func (a *App) adminRelayPathSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseUint(chi.URLParam(r, "id"), 10, 32)
	u, err := a.Q.GetUser(ctx, uint32(id))
	if isNoRows(err) {
		a.userPage(w, r, "admin/relay/search", map[string]any{"pathset": []*relayPath{}})
		return
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}
	all, err := a.Q.ListNodesByName(ctx)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	byID := map[int32]store.SsNode{}
	for _, n := range all {
		byID[int32(n.ID)] = n //nolint:gosec // 节点 ID 范围很小
	}
	rules, err := a.Q.ListRelayRulesVisible(ctx, u.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	paths, err := a.relayPaths(r, u, all, byID, rules, time.Now())
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.userPage(w, r, "admin/relay/search", map[string]any{"pathset": paths})
}

// ---- 商品与订单（Admin\ShopController）----

func (a *App) adminShopRoutes(r chi.Router) {
	r.Get("/shop", a.adminTablePage("admin/shop/index", newTable("shop/ajax", nil,
		"op", "操作", "id", "ID", "name", "商品名称", "price", "价格", "content", "商品内容", "auto_renew", "自动续费",
		"auto_reset_bandwidth", "续费时是否重置流量", "status", "状态", "period_sales", "周期销量")))
	r.Post("/shop/ajax", a.adminShopAjax)
	r.Get("/bought", a.adminTablePage("admin/shop/bought", newTable("bought/ajax", nil,
		"op", "操作", "id", "ID", "datetime", "购买日期", "content", "内容", "price", "价格", "user_id", "用户ID",
		"user_name", "用户名", "renew", "自动续费时间", "auto_reset_bandwidth", "续费时是否重置流量")))
	r.Delete("/bought", a.adminBoughtDelete)
	r.Post("/bought/ajax", a.adminBoughtAjax)
	r.Get("/shop/create", a.adminPage("admin/shop/create"))
	r.Post("/shop", a.adminShopSave)
	r.Get("/shop/{id}/edit", a.adminShopEdit)
	r.Put("/shop/{id}", a.adminShopSave)
	r.Delete("/shop", a.adminShopDelete)
}

func (a *App) adminShopAjax(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a.dataTable(w, r, tableSpec{
		Query:   "SELECT id AS op, id, name, price, content, auto_renew, auto_reset_bandwidth, status, id AS period_sales FROM shop",
		Columns: []string{"op", "id", "name", "price", "content", "auto_renew", "auto_reset_bandwidth", "status", "period_sales"},
		HTML:    []string{"op"},
		Format: func(row map[string]string) map[string]string {
			id, _ := strconv.ParseUint(row["id"], 10, 64)
			sm := model.NewShop(store.Shop{ID: id, Content: row["content"]})
			op := editButton("/admin/shop/"+row["id"]+"/edit") + "\n"
			if row["status"] == "0" {
				op += `<a class="btn btn-brand-accent" disabled>下架</a>`
			} else {
				op += `<a class="btn btn-brand-accent" id="row_delete_` + template.HTMLEscapeString(row["id"]) + `" href="javascript:void(0);" data-call="delete_modal_show" data-args='["` + template.HTMLEscapeString(row["id"]) + `"]'>下架</a>`
			}
			renew := "不自动续费"
			if row["auto_renew"] != "0" {
				renew = row["auto_renew"] + " 天后续费"
			}
			reset := "自动重置"
			if row["auto_reset_bandwidth"] == "0" {
				reset = "不自动重置"
			}
			status := "下架"
			if row["status"] == "1" {
				status = "上架"
			}
			// 周期销量：sales_period 天内的购买数；为 expire 时取商品的等级有效期。
			period, _ := strconv.ParseFloat(a.Cfg.Display.SalesPeriod, 64)
			if a.Cfg.Display.SalesPeriod == "expire" {
				period = sm.Num("class_expire")
			}
			sales, err := a.Q.CountShopSalesSince(ctx, store.CountShopSalesSinceParams{Shopid: id, Datetime: time.Now().Unix() - int64(period*86400)})
			if err != nil {
				a.Log.Warn("统计商品销量失败", "err", err)
			}
			return map[string]string{
				"op": op, "content": sm.Content(), "auto_renew": renew, "auto_reset_bandwidth": reset, "status": status,
				"period_sales": strconv.FormatInt(sales, 10),
			}
		},
	})
}

func (a *App) adminBoughtAjax(w http.ResponseWriter, r *http.Request) {
	a.dataTable(w, r, tableSpec{
		Query: "SELECT bought.id AS op, bought.id AS id, bought.datetime, shop.content AS content, bought.price, user.id AS user_id, " +
			"user.user_name, renew, shop.auto_reset_bandwidth FROM bought, user, shop WHERE bought.shopid = shop.id AND bought.userid = user.id",
		Columns: []string{"op", "id", "datetime", "content", "price", "user_id", "user_name", "renew", "auto_reset_bandwidth"},
		HTML:    []string{"op"},
		Format: func(row map[string]string) map[string]string {
			op := `<a class="btn btn-brand-accent" disabled>中止</a>`
			if row["renew"] != "0" {
				op = `<a class="btn btn-brand-accent" id="row_delete_` + template.HTMLEscapeString(row["id"]) + `" href="javascript:void(0);" data-call="delete_modal_show" data-args='["` + template.HTMLEscapeString(row["id"]) + `"]'>中止</a>`
			}
			renew := "不自动续费"
			if row["renew"] != "0" {
				renew = a.fmtUnix(row["renew"]) + " 续费"
			}
			reset := "自动重置"
			if row["auto_reset_bandwidth"] == "0" {
				reset = "不自动重置"
			}
			return map[string]string{
				"op": op, "content": model.NewShop(store.Shop{Content: row["content"]}).Content(), "renew": renew,
				"auto_reset_bandwidth": reset, "datetime": a.fmtUnix(row["datetime"]),
			}
		},
	})
}

func (a *App) adminBoughtDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(param(r, "id"), 10, 64)
	if _, err := a.Q.DisableBoughtRenewByID(r.Context(), id); err != nil {
		a.json(w, fail("退订失败"))
		return
	}
	a.json(w, ok("退订成功"))
}

func (a *App) adminShopEdit(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	s, err := a.Q.GetShop(r.Context(), id)
	if isNoRows(err) {
		a.Srv.NotFound(w, r)
		return
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.userPage(w, r, "admin/shop/edit", map[string]any{"shop": model.NewShop(s)})
}

// shopContent 按原版顺序组装商品内容（值为表单提交的字符串，与原版 json_encode 结果一致）。
func shopContent(r *http.Request) string {
	m := sub.NewOMap()
	nonZero := func(k string) {
		v := param(r, k)
		if model.PHPNumber([]byte(`"`+strings.ReplaceAll(v, `"`, ``)+`"`)) != 0 {
			m.Set(k, v)
		}
	}
	for _, k := range []string{"bandwidth", "expire", "class", "class_expire", "reset", "reset_value", "reset_exp"} {
		nonZero(k)
	}
	m.Set("speedlimit", param(r, "speedlimit"))
	m.Set("connector", param(r, "connector"))
	if v := param(r, "content_extra"); v != "" {
		m.Set("content_extra", v)
	}
	return sub.EncodeJSON(m, 0)
}

// adminShopSave 对应 ShopController::add / update。
func (a *App) adminShopSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	price, err := dec.Parse(param(r, "price"))
	if err != nil {
		a.json(w, fail("价格无效"))
		return
	}
	name, content := param(r, "name"), shopContent(r)
	autoRenew, resetBW := atoi32(param(r, "auto_renew")), atoi32(param(r, "auto_reset_bandwidth"))
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		id, _ := strconv.ParseUint(idStr, 10, 64)
		err := a.tx(ctx, func(q *store.Queries) error {
			old, err := q.GetShop(ctx, id)
			if err != nil {
				return err
			}
			// 原版：关闭“续费时重置流量”时，停止该商品所有订单的自动续费。
			if old.AutoResetBandwidth == 1 && resetBW == 0 {
				if err := q.DisableShopRenew(ctx, id); err != nil {
					return err
				}
			}
			_, err = q.UpdateShop(ctx, store.UpdateShopParams{Name: name, Price: price, Content: content, AutoRenew: autoRenew, AutoResetBandwidth: resetBW, ID: id})
			return err
		})
		if err != nil {
			a.json(w, fail("保存失败"))
			return
		}
		a.json(w, ok("保存成功"))
		return
	}
	if err := a.Q.InsertShop(ctx, store.InsertShopParams{Name: name, Price: price, Content: content, AutoRenew: autoRenew, AutoResetBandwidth: resetBW}); err != nil {
		a.json(w, fail("添加失败"))
		return
	}
	a.json(w, ok("添加成功"))
}

// adminShopDelete 对应 ShopController::deleteGet：下架商品并停止相关订单的自动续费。
func (a *App) adminShopDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseUint(param(r, "id"), 10, 64)
	err := a.tx(ctx, func(q *store.Queries) error {
		if n, err := q.SetShopStatus(ctx, store.SetShopStatusParams{Status: 0, ID: id}); err != nil {
			return err
		} else if n == 0 {
			if _, err := q.GetShop(ctx, id); err != nil {
				return err
			}
		}
		return q.DisableShopRenew(ctx, id)
	})
	if err != nil {
		a.json(w, fail("下架失败"))
		return
	}
	a.json(w, ok("下架成功"))
}
