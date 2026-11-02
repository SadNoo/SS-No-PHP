package app

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/auth"
	"github.com/SadNoo/SS-No-PHP/internal/dec"
	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/server"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

func (a *App) adminUserRoutes(r chi.Router) {
	r.Get("/user", a.adminUserList(newTable("user/ajax", []string{"op", "id", "user_name", "remark", "email"},
		"op", "操作", "id", "ID", "user_name", "用户名", "remark", "备注", "email", "邮箱", "money", "金钱",
		"im_type", "联络方式类型", "im_value", "联络方式详情", "node_group", "群组", "expire_in", "账户过期时间",
		"class", "等级", "class_expire", "等级过期时间", "passwd", "连接密码", "port", "连接端口", "method", "加密方式",
		"protocol", "连接协议", "obfs", "连接混淆方式", "online_ip_count", "在线IP数", "last_ss_time", "上次使用时间",
		"used_traffic", "已用流量/GB", "enable_traffic", "总流量/GB", "last_checkin_time", "上次签到时间",
		"today_traffic", "今日流量/MB", "enable", "是否启用", "reg_date", "注册时间", "reg_ip", "注册IP",
		"auto_reset_day", "自动重置流量日", "auto_reset_bandwidth", "自动重置流量/GB", "ref_by", "邀请人ID",
		"ref_by_user_name", "邀请人用户名", "top_up", "累计充值")))
	r.Get("/user/{id}/edit", a.adminUserEdit)
	a.adminUserBoughtRoutes(r)
	r.Put("/user/{id}", a.adminUserUpdate)
	r.Delete("/user", a.adminUserDelete)
	r.Post("/user/changetouser", a.adminChangeToUser)
	r.Post("/user/ajax", a.adminUserAjax)
	r.Post("/user/create", a.adminCreateUser)
	r.Post("/user/buy", a.adminUserBuy)
}

// adminUserAjax 对应 Admin\UserController::ajax。原版把排序字段直接拼进 SQL（orderByRaw），存在 SQL 注入（K02）；
// 这里排序与搜索只能使用下面列出的列。
func (a *App) adminUserAjax(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	a.dataTable(w, r, tableSpec{
		Query: "SELECT user.id AS op, user.id, user.user_name, user.remark, user.email, user.money, user.im_type, user.im_value, " +
			"user.node_group, user.expire_in, user.class, user.class_expire, user.passwd, user.port, user.method, user.protocol, user.obfs, " +
			"(user.u + user.d) AS used_traffic, user.transfer_enable AS enable_traffic, (user.u + user.d - user.last_day_t) AS today_traffic, " +
			"user.enable, user.reg_date, user.reg_ip, user.auto_reset_day, user.auto_reset_bandwidth, user.ref_by, " +
			"user.t AS last_ss_time, user.last_check_in_time AS last_checkin_time, COALESCE(oi.c, 0) AS online_ip_count, " +
			"COALESCE(tu.total, 0) AS top_up, " +
			"CASE WHEN user.ref_by = 0 THEN '系统邀请' ELSE COALESCE(ru.user_name, '邀请人已经被删除') END AS ref_by_user_name FROM user " +
			// 在线 IP 数与用户中心相同：最近 90 秒、去掉 ::ffff: 前缀后去重、排除节点自身的 IP。
			// 只汇总最近 90 秒的记录（datetime 索引）再按用户关联，可以按此列排序。
			"LEFT JOIN (SELECT userid, COUNT(DISTINCT REPLACE(ip, '::ffff:', '')) AS c FROM alive_ip WHERE `datetime` >= ? " +
			"AND REPLACE(ip, '::ffff:', '') NOT IN (SELECT node_ip FROM ss_node WHERE node_ip <> '') GROUP BY userid) AS oi ON oi.userid = user.id " +
			// 累计充值与原版 User::get_top_up 相同：该用户名下所有充值码金额之和。
			"LEFT JOIN (SELECT userid, SUM(number) AS total FROM code GROUP BY userid) AS tu ON tu.userid = user.id " +
			"LEFT JOIN user AS ru ON ru.id = user.ref_by",
		Args: []any{now.Unix() - 90},
		Columns: []string{"id", "user_name", "remark", "email", "money", "im_type", "im_value", "node_group", "expire_in", "class",
			"class_expire", "passwd", "port", "method", "protocol", "obfs", "online_ip_count", "last_ss_time", "used_traffic", "enable_traffic",
			"last_checkin_time", "today_traffic", "enable", "reg_date", "reg_ip", "auto_reset_day", "auto_reset_bandwidth", "ref_by",
			"ref_by_user_name", "top_up"},
		HTML: []string{"op", "im_value"},
		Format: func(row map[string]string) map[string]string {
			id := row["id"]
			out := map[string]string{
				"op": editButton("/admin/user/"+id+"/edit") + "\n" +
					callButton("btn-brand-accent", "delete_modal_show", id, "删除", false) + "\n" +
					linkButton("/admin/user/"+id+"/bought", "查套餐") + "\n" +
					callButton("btn-brand", "changetouser_modal_show", id, "切换为该用户", false),
				"expire_in": a.fmtDBTime(row["expire_in"]), "class_expire": a.fmtDBTime(row["class_expire"]),
				"reg_date": a.fmtDBTime(row["reg_date"]),
			}
			esc := escapeUnless(row["im_value"], false)
			out["im_value"] = esc
			switch row["im_type"] {
			case "1":
				out["im_type"] = "微信"
			case "2":
				out["im_type"] = "QQ"
			case "3":
				out["im_type"] = "Google+"
			default:
				out["im_type"] = "Telegram"
				out["im_value"] = `<a href="https://telegram.me/` + esc + `">` + esc + `</a>`
			}
			used, _ := strconv.ParseFloat(row["used_traffic"], 64)
			te, _ := strconv.ParseFloat(row["enable_traffic"], 64)
			today, _ := strconv.ParseFloat(row["today_traffic"], 64)
			out["used_traffic"] = util.PHPFloat(used / util.GB)
			out["enable_traffic"] = util.PHPFloat(te / util.GB)
			out["today_traffic"] = util.PHPFloat(today / util.MB)
			out["enable"] = "禁用"
			if row["enable"] == "1" {
				out["enable"] = "可用"
			}
			t, _ := strconv.ParseInt(row["last_ss_time"], 10, 64)
			lc, _ := strconv.ParseInt(row["last_checkin_time"], 10, 64)
			mu := model.NewUser(store.User{T: t, LastCheckInTime: lc}, a.Loc)
			out["last_ss_time"], out["last_checkin_time"] = mu.LastSsTime(), mu.LastCheckInTime()
			top, _ := strconv.ParseFloat(row["top_up"], 64)
			out["top_up"] = util.PHPFloat(top)
			return out
		},
	})
}

func (a *App) adminUserEdit(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(chi.URLParam(r, "id"), 10, 32)
	u, err := a.Q.GetUser(r.Context(), uint32(id))
	if isNoRows(err) {
		a.Srv.NotFound(w, r)
		return
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.userPage(w, r, "admin/user/edit", map[string]any{"edit_user": model.NewUser(u, a.Loc)})
}

// parseBizTime 解析后台提交的时间（业务时区 Y-m-d H:i:s），1989、2099 等特殊值按原样保存。
func (a *App) parseBizTime(s string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(s), a.Loc)
	if err != nil {
		return t, err
	}
	if t.Year() < 2000 || t.Year() >= 2099 {
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC), nil
	}
	return t.UTC(), nil
}

// adminUserUpdate 对应 Admin\UserController::update。
func (a *App) adminUserUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseUint(chi.URLParam(r, "id"), 10, 32)
	u, err := a.Q.GetUser(ctx, uint32(id))
	if err != nil {
		a.json(w, fail("修改失败"))
		return
	}
	classExpire, err1 := a.parseBizTime(param(r, "class_expire"))
	expireIn, err2 := a.parseBizTime(param(r, "expire_in"))
	money, err3 := dec.Parse(param(r, "money"))
	speed, err4 := dec.Parse(param(r, "node_speedlimit"))
	resetBW, err5 := dec.Parse(param(r, "auto_reset_bandwidth"))
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
		a.json(w, fail("修改失败：时间或数值格式错误"))
		return
	}
	// 等级大于 0 必须有真实的等级到期时间（套餐到期）；“无等级期限”是不允许的异常数据。
	if atoi32(param(r, "class")) > 0 && classExpire.Year() <= 1989 {
		a.json(w, fail("修改失败：等级大于 0 时必须填写等级到期时间"))
		return
	}
	// 同时在线 IP 数必须是非负整数（0 不限）。原来无法识别的输入会被当作 0，即“不限”。
	if c, err := strconv.ParseUint(strings.TrimSpace(param(r, "node_connector")), 10, 31); err != nil || c > 10000 {
		a.json(w, fail("修改失败：用户同时连接 IP 数应为 0～10000 的整数（0 表示不限）"))
		return
	}
	pass := u.Pass
	newPass := param(r, "pass") != ""
	if newPass {
		if pass, err = auth.HashPassword(param(r, "pass")); err != nil {
			a.jsonError(w, r, err)
			return
		}
	}
	te, _ := strconv.ParseFloat(param(r, "transfer_enable"), 64)
	port := atoi32(param(r, "port"))
	refBy, _ := strconv.ParseUint(param(r, "ref_by"), 10, 32)
	lines := func(k string) string {
		return strings.ReplaceAll(strings.ReplaceAll(param(r, k), "\r\n", ","), "\n", ",")
	}
	err = a.tx(ctx, func(q *store.Queries) error {
		if newPass {
			if err := q.DeleteLinksByUser(ctx, u.ID); err != nil {
				return err
			}
		}
		if err := q.UpdateRelayPortForUser(ctx, store.UpdateRelayPortForUserParams{NewPort: port, UserID: u.ID, OldPort: u.Port}); err != nil {
			return err
		}
		return q.AdminUpdateUser(ctx, store.AdminUpdateUserParams{
			Email: strings.TrimSpace(param(r, "email")), Pass: pass, AutoResetDay: atoi32(param(r, "auto_reset_day")), AutoResetBandwidth: resetBW,
			Port: port, Passwd: param(r, "passwd"), Protocol: param(r, "protocol"), ProtocolParam: param(r, "protocol_param"),
			Obfs: param(r, "obfs"), ObfsParam: param(r, "obfs_param"), IsMultiUser: atoi32(param(r, "is_multi_user")),
			TransferEnable: int64(te * util.GB), InviteNum: atoi32(param(r, "invite_num")), Method: param(r, "method"),
			NodeSpeedlimit: speed, NodeConnector: atoi32(strings.TrimSpace(param(r, "node_connector"))), Enable: int8(atoi32(param(r, "enable"))), //nolint:gosec // 0 或 1
			IsAdmin: int8(atoi32(param(r, "is_admin"))), GaEnable: atoi32(param(r, "ga_enable")), NodeGroup: atoi32(param(r, "group")), //nolint:gosec // 0 或 1
			RefBy:                     uint32(refBy), Remark: //nolint:gosec // ParseUint 已限制为 32 位
			param(r, "remark"), Money: money, Class: atoi32(param(r, "class")),
			ClassExpire: classExpire, ExpireIn: expireIn, ForbiddenIp: lines("forbidden_ip"), ForbiddenPort: lines("forbidden_port"), ID: u.ID,
		})
	})
	if err != nil {
		a.Log.Warn("修改用户失败", "id", u.ID, "err", err)
		a.json(w, fail("修改失败"))
		return
	}
	a.radiusRename(ctx, u, strings.TrimSpace(param(r, "email")), param(r, "passwd"))
	a.json(w, ok("修改成功"))
}

func (a *App) adminUserDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseUint(param(r, "id"), 10, 32)
	u, err := a.Q.GetUser(ctx, uint32(id))
	if err != nil {
		a.json(w, fail("删除失败"))
		return
	}
	if err := a.killUser(ctx, u); err != nil {
		a.Log.Warn("删除用户失败", "id", u.ID, "err", err)
		a.json(w, fail("删除失败"))
		return
	}
	a.json(w, ok("删除成功"))
}

// adminChangeToUser 对应 Admin\UserController::changetouser：以该用户身份登录。
// 原版从请求参数中读取管理员 ID，这里使用当前登录的管理员。
func (a *App) adminChangeToUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseUint(param(r, "userid"), 10, 32)
	target, err := a.Q.GetUser(ctx, uint32(id))
	if err != nil {
		a.json(w, fail("非法请求"))
		return
	}
	if err := a.Auth.SwitchToUser(ctx, auth.User(r).User, target, param(r, "local")); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("切换成功"))
}

// adminCreateUser 对应 Admin\UserController::createNewUser：按邮箱快速创建用户，随机生成登录密码并邮件告知。
func (a *App) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	email := strings.ToLower(strings.TrimSpace(param(r, "userEmail")))
	if email == "" {
		a.json(w, fail("邮箱无效"))
		return
	}
	if n, err := a.Q.CountUsersByEmail(ctx, email); err != nil {
		a.jsonError(w, r, err)
		return
	} else if n > 0 {
		a.json(w, fail("邮箱已经被注册了"))
		return
	}
	pass := util.RandomChar(8)
	hash, err := auth.HashPassword(pass)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	port, err := a.availablePort(ctx)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	params := a.newUserParams(email, email, hash, server.ClientIP(r), port, 2, email, time.Time{})
	params.Money = 0
	if _, err := a.Q.CreateUser(ctx, params); err != nil {
		a.jsonError(w, r, err)
		return
	}
	res := Ret{"ret": 1, "msg": "新用户注册成功 用户名: " + email + " 随机初始密码: " + pass, "email_error": "success"}
	text := "您好，管理员已经为您生成账户，用户名: " + email + "，登录密码为：" + pass + "，感谢您的支持。 "
	if err := a.Mail.Send(ctx, email, a.Cfg.App.Name+"-新用户注册通知", "newuser", map[string]any{"user": params, "text": text}); err != nil {
		res["email_error"] = err.Error()
	}
	a.json(w, res)
}

// adminUserBuy 对应 Admin\UserController::buy：为用户添加套餐（不扣余额）。原版缺少 Shop 类的引用，调用即报错。
func (a *App) adminUserBuy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, err := a.Q.GetUserByEmail(ctx, strings.TrimSpace(param(r, "userEmail")))
	if err != nil {
		a.json(w, fail("未找到该用户"))
		return
	}
	shopID, _ := strconv.ParseUint(param(r, "shopId"), 10, 64)
	s, err := a.Q.GetShopOnSale(ctx, shopID)
	if err != nil {
		a.json(w, fail("请选择套餐"))
		return
	}
	now := time.Now()
	err = a.applyShop(ctx, u.ID, s, param(r, "autorenew") == "1", s.Price, 0, "", now)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("套餐添加成功"))
}

// adminUserList 是用户列表页，另显示付费用户数（等级大于 0 且等级未过期）。
func (a *App) adminUserList(cfg tableConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		paid, err := a.Q.CountPaidUsers(r.Context(), time.Now().UTC())
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		a.userPage(w, r, "admin/user/index", map[string]any{"table_config": cfg, "paid_users": paid})
	}
}

// applyShop 为用户记录订单并使套餐生效。price 是订单记录的价格；charge 大于 0 时从余额扣除（余额不足返回 errMsg）。
// 与用户购买相同：始终关闭该用户其他套餐的自动续费，只有开启自动续费（enable_auto_renew）时才记录续费时间。
func (a *App) applyShop(ctx context.Context, uid uint32, s store.Shop, autoRenew bool, price, charge dec.D, coupon string, now time.Time) error {
	return a.tx(ctx, func(q *store.Queries) error {
		cur, err := q.GetUserForUpdate(ctx, uid)
		if err != nil {
			return err
		}
		if charge > 0 {
			if cur.Money < charge {
				return errMsg("喵喵喵~ 该用户余额不足，当前余额 " + cur.Money.String() + " 元，套餐价格 " + charge.String() + " 元。")
			}
			if _, err := q.DeductUserMoney(ctx, store.DeductUserMoneyParams{Amount: charge, ID: uid}); err != nil {
				return err
			}
			cur.Money -= charge
		}
		if err := q.DisableUserRenew(ctx, uid); err != nil {
			return err
		}
		renew := int64(0)
		if a.Cfg.Users.EnableAutoRenew && autoRenew && s.AutoRenew != 0 {
			renew = now.Unix() + int64(s.AutoRenew)*86400
		}
		if err := q.InsertBought(ctx, store.InsertBoughtParams{Userid: uid, Shopid: s.ID, Datetime: now.Unix(), Renew: renew, Coupon: coupon, Price: price}); err != nil {
			return err
		}
		model.NewShop(s).Apply(&cur, false, a.Cfg.Users.EnableBoughtReset, a.Cfg.Users.EnableBoughtExtend, now)
		return q.UpdateUserAfterBuy(ctx, afterBuyParams(cur))
	})
}
