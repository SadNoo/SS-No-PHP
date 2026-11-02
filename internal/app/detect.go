package app

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/auth"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

func (a *App) detectRoutes(r chi.Router) {
	r.Get("/detect", a.detectIndex)
	r.Get("/detect/log", a.detectLog)
}

// detectIndex 对应 UserController::detect_index：审计规则列表。
func (a *App) detectIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	total, err := a.Q.CountDetectRules(ctx)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p := newPager(r, "/user/detect", 15, total)
	list, err := a.Q.ListDetectRules(ctx, store.ListDetectRulesParams{Limit: p.Limit(), Offset: p.Offset()})
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p.Items = list
	a.userPage(w, r, "user/detect_index", map[string]any{"rules": p})
}

// detectLog 对应 UserController::detect_log：当前用户触发审计规则的记录。
func (a *App) detectLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	total, err := a.Q.CountUserDetectLogs(ctx, u.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p := newPager(r, "/user/detect/log", 15, total)
	logs, err := a.Q.ListUserDetectLogs(ctx, store.ListUserDetectLogsParams{UserID: u.ID, Limit: p.Limit(), Offset: p.Offset()})
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	ids := make([]uint64, 0, len(logs))
	for _, l := range logs {
		ids = append(ids, l.ListID)
	}
	rules := map[uint64]store.DetectList{}
	if len(ids) > 0 {
		list, err := a.Q.ListDetectRulesByIDs(ctx, ids)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		for _, x := range list {
			rules[x.ID] = x
		}
	}
	nodes, err := a.nodeMap(r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	rows := make([]map[string]any, 0, len(logs))
	for _, l := range logs {
		row := map[string]any{
			"ID": l.ID, "NodeID": l.NodeID, "NodeName": nodes[l.NodeID].Name, "ListID": l.ListID,
			"Datetime": util.ToDateTime(l.Datetime, a.Loc), "DetectRule": nil,
		}
		// 原版：规则已删除的记录不显示（DetectRule() 返回 null）。
		if rule, ok := rules[l.ListID]; ok {
			row["DetectRule"] = rule
		}
		rows = append(rows, row)
	}
	p.Items = rows
	a.userPage(w, r, "user/detect_log", map[string]any{"logs": p})
}
