package app

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// 用户购买明细（后台用户列表的「查套餐」，对应较新版 SSPanel-UIM 的 Admin\UserController::bought 系列）：
// 查看某个用户的购买记录，为用户添加套餐（不扣余额）或购买套餐（从该用户余额扣款），删除购买记录。

func (a *App) adminUserBoughtRoutes(r chi.Router) {
	r.Get("/user/{id}/bought", a.adminUserBought)
	r.Post("/user/{id}/bought/ajax", a.adminUserBoughtAjax)
	r.Post("/user/{id}/bought", a.adminUserBoughtAdd)
	r.Delete("/user/{id}/bought", a.adminUserBoughtDelete)
}

// adminBoughtUser 读取路径中的用户；不存在时返回 404（页面）或提示（接口）。
func (a *App) adminBoughtUser(r *http.Request) (store.User, error) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 32)
	if err != nil {
		return store.User{}, errMsg("用户不存在")
	}
	u, err := a.Q.GetUser(r.Context(), uint32(id))
	if isNoRows(err) {
		return store.User{}, errMsg("用户不存在")
	}
	return u, err
}

func (a *App) adminUserBought(w http.ResponseWriter, r *http.Request) {
	u, err := a.adminBoughtUser(r)
	if _, isMsg := err.(errMsg); isMsg { //nolint:errorlint // errMsg 只在本包内直接返回
		a.Srv.NotFound(w, r)
		return
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}
	shops, err := a.Q.ListShopsOnSale(r.Context())
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	cfg := newTable("bought/ajax", []string{"op", "name", "valid", "reset_time"},
		"op", "操作", "id", "ID", "name", "商品名称", "valid", "是否有效期内", "auto_renew", "自动续费时间",
		"reset_time", "流量重置时间", "buy_time", "套餐购买时间", "exp_time", "套餐过期时间", "content", "商品详细内容").
		// 是否有效期内、流量重置时间、套餐过期时间由程序按套餐内容计算，商品详细内容显示的是整理后的文字，
		// 数据库中没有对应的值可以排序，不提供排序（其余列按数据库中的真实值排序）。
		noSort("valid", "reset_time", "exp_time", "content")
	a.userPage(w, r, "admin/user/bought", map[string]any{"table_config": cfg, "edit_user": u, "shops": shops})
}

// boughtJSONError 返回接口错误：errMsg 原样提示，其他错误记录日志后提示系统错误。
func (a *App) boughtJSONError(w http.ResponseWriter, r *http.Request, err error) {
	if m, isMsg := err.(errMsg); isMsg { //nolint:errorlint // errMsg 只在本包内直接返回
		a.json(w, fail(string(m)))
		return
	}
	a.jsonError(w, r, err)
}

func (a *App) adminUserBoughtAjax(w http.ResponseWriter, r *http.Request) {
	u, err := a.adminBoughtUser(r)
	if err != nil {
		a.boughtJSONError(w, r, err)
		return
	}
	now := time.Now()
	pending, err := a.dailyJobPendingToday(r.Context(), now)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.dataTable(w, r, tableSpec{
		Query: "SELECT bought.id AS op, bought.id, COALESCE(shop.name, '') AS name, '' AS valid, bought.renew AS auto_renew, " +
			"'' AS reset_time, bought.datetime AS buy_time, '' AS exp_time, COALESCE(shop.content, '') AS content " +
			"FROM bought LEFT JOIN shop ON shop.id = bought.shopid WHERE bought.userid = ?",
		Args:    []any{u.ID},
		Columns: []string{"op", "id", "name", "auto_renew", "buy_time", "content"},
		NoOrder: []string{"content"},
		HTML:    []string{"op"},
		Format: func(row map[string]string) map[string]string {
			return a.boughtRow(row, now, pending)
		},
	})
}

// boughtRow 计算一条购买记录的显示内容。有效期、过期时间与流量重置时间只对设置了周期重置
// （reset、reset_value、reset_exp）的套餐有意义，其他套餐显示“-”（与原版相同）；流量重置时间与首页规则相同。
func (a *App) boughtRow(row map[string]string, now time.Time, pending bool) map[string]string {
	id := row["id"]
	out := map[string]string{
		"op":         callButton("btn-brand-accent", "delete_modal_show", id, "删除", false),
		"valid":      "-",
		"exp_time":   "-",
		"reset_time": "-",
		"buy_time":   a.fmtUnix(row["buy_time"]),
	}
	if row["name"] == "" && row["content"] == "" {
		out["name"] = "（商品已删除）"
	}
	switch {
	case row["auto_renew"] == "0":
		out["auto_renew"] = "不自动续费"
	case !a.Cfg.Users.EnableAutoRenew:
		out["auto_renew"] = "本站未开启自动续费"
	default:
		out["auto_renew"] = a.fmtUnix(row["auto_renew"])
	}
	sm := model.NewShop(store.Shop{Content: row["content"]})
	out["content"] = sm.Content()
	buy, _ := strconv.ParseInt(row["buy_time"], 10, 64)
	p := planReset{Datetime: buy, Reset: sm.Num("reset"), Value: sm.Num("reset_value"), Exp: sm.Num("reset_exp")}
	if p.Reset == 0 || p.Value == 0 || p.Exp == 0 {
		return out
	}
	end := buy + int64(p.Exp*86400)
	out["exp_time"] = a.fmtUnix(strconv.FormatInt(end, 10))
	out["valid"] = "已过期"
	if now.Unix() < end {
		out["valid"] = "有效"
	}
	if next, ok := nextTrafficReset(now, a.Loc, 0, []planReset{p}, pending); ok {
		out["reset_time"] = next.Format("2006-01-02")
	}
	return out
}

// adminUserBoughtAdd 为用户添加（type=0，不扣余额，订单价格记为 0）或购买（type=1，从余额扣除套餐价格）套餐。
// 与用户自己购买相同：关闭该用户其他套餐的自动续费，不记录续费时间。
func (a *App) adminUserBoughtAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, err := a.adminBoughtUser(r)
	if err != nil {
		a.boughtJSONError(w, r, err)
		return
	}
	shopID, _ := strconv.ParseUint(param(r, "shop"), 10, 64)
	s, err := a.Q.GetShopOnSale(ctx, shopID)
	if err != nil {
		a.json(w, fail("请选择套餐"))
		return
	}
	var price, charge = s.Price, s.Price
	switch param(r, "type") {
	case "0":
		price, charge = 0, 0
	case "1":
	default:
		a.json(w, fail("请选择类型"))
		return
	}
	if err := a.applyShop(ctx, u.ID, s, false, price, charge, "", time.Now()); err != nil {
		a.boughtJSONError(w, r, err)
		return
	}
	if charge > 0 {
		a.json(w, ok("套餐购买成功，已从该用户余额扣除 "+charge.String()+" 元"))
		return
	}
	a.json(w, ok("套餐添加成功"))
}

// adminUserBoughtDelete 删除一条购买记录（与原版相同，只删除记录，不撤销已生效的等级、流量与有效期）。
// 删除的是该用户最新的订单时，同时关闭其余订单的自动续费：只有最新订单可以续费，较旧的订单不应因此接着续费。
func (a *App) adminUserBoughtDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, err := a.adminBoughtUser(r)
	if err != nil {
		a.boughtJSONError(w, r, err)
		return
	}
	id, _ := strconv.ParseUint(param(r, "id"), 10, 64)
	err = a.tx(ctx, func(q *store.Queries) error { return deleteUserBought(ctx, q, u.ID, id) })
	if err != nil {
		a.boughtJSONError(w, r, err)
		return
	}
	a.json(w, ok("删除成功"))
}

func deleteUserBought(ctx context.Context, q *store.Queries, uid uint32, id uint64) error {
	if _, err := q.GetUserForUpdate(ctx, uid); err != nil {
		return err
	}
	latest, err := q.GetLatestBoughtID(ctx, uid)
	if err != nil && !isNoRows(err) {
		return err
	}
	n, err := q.DeleteBoughtForUser(ctx, store.DeleteBoughtForUserParams{ID: id, Userid: uid})
	if err != nil {
		return err
	}
	if n == 0 {
		return errMsg("购买记录不存在")
	}
	if id == latest {
		return q.DisableUserRenew(ctx, uid)
	}
	return nil
}
