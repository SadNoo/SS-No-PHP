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
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

func (a *App) shopRoutes(r chi.Router) {
	r.Get("/shop", a.shop)
	r.Post("/coupon_check", a.couponCheck)
	r.Post("/buy", a.buy)
	r.Get("/bought", a.bought)
	r.Delete("/bought", a.deleteBought)
	r.Get("/code", a.code)
	r.Post("/code", a.codePost)
	r.Get("/code_check", a.codeCheck)
	r.Get("/invite", a.invite)
	r.Post("/buy_invite", a.buyInvite)
	r.Post("/custom_invite", a.customInvite)
	r.Post("/inviteurl_reset", a.resetInviteURL) // K23：原版为 GET
}

// shop 对应 UserController::shop。
func (a *App) shop(w http.ResponseWriter, r *http.Request) {
	list, err := a.Q.ListShopsOnSale(r.Context())
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	shops := make([]*model.Shop, 0, len(list))
	for _, s := range list {
		shops = append(shops, model.NewShop(s))
	}
	a.userPage(w, r, "user/shop", map[string]any{"shops": shops, "purchase_notice": a.purchaseNotice()})
}

// couponMatches 对应 Coupon::order：优惠码是否适用于该商品（shop 为空表示全部商品）。
func couponMatches(c store.Coupon, shopID uint64) bool {
	if c.Shop == "" {
		return true
	}
	for _, id := range strings.Split(c.Shop, ",") {
		if strings.TrimSpace(id) == strconv.FormatUint(shopID, 10) {
			return true
		}
	}
	return false
}

// shopParam 读取请求中的商品（只接受上架中的商品）。
func (a *App) shopParam(r *http.Request) (*store.Shop, error) {
	id, err := strconv.ParseUint(param(r, "shop"), 10, 64)
	if err != nil {
		return nil, nil
	}
	s, err := a.Q.GetShopOnSale(r.Context(), id)
	if isNoRows(err) {
		return nil, nil
	}
	return &s, err
}

// couponCheck 对应 UserController::CouponCheck：购买前计算优惠后的价格。
func (a *App) couponCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	code := trimmed(r, "coupon")
	s, err := a.shopParam(r)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	if s == nil {
		a.json(w, fail("非法请求"))
		return
	}
	if code == "" {
		a.json(w, Ret{"ret": 1, "name": s.Name, "credit": "0 %", "total": s.Price.String() + "元"})
		return
	}
	c, msg, err := a.checkCoupon(ctx, u.ID, code, s.ID, false)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	if msg != "" {
		a.json(w, fail(msg))
		return
	}
	price := s.Price.Float() * (float64(100-c.Credit) / 100)
	a.json(w, Ret{"ret": 1, "name": s.Name, "credit": strconv.Itoa(int(c.Credit)) + " %", "total": util.PHPFloat(price) + "元"})
}

// checkCoupon 校验优惠码：存在、适用于该商品、使用次数未超限；checkExpire 为 true 时还检查是否过期（原版只在购买时检查）。
// 返回非空 msg 表示不可用。
func (a *App) checkCoupon(ctx context.Context, uid uint32, code string, shopID uint64, checkExpire bool) (store.Coupon, string, error) {
	c, err := a.Q.GetCouponByCode(ctx, code)
	if isNoRows(err) {
		return c, "优惠码无效", nil
	}
	if err != nil {
		return c, "", err
	}
	if !couponMatches(c, shopID) {
		return c, "此优惠码不可用于此商品", nil
	}
	if checkExpire && c.Expire < time.Now().Unix() {
		return c, "此优惠码已过期", nil
	}
	if c.Onetime > 0 {
		n, err := a.Q.CountBoughtWithCoupon(ctx, store.CountBoughtWithCouponParams{Userid: uid, Coupon: c.Code})
		if err != nil {
			return c, "", err
		}
		if n >= int64(c.Onetime) {
			return c, "优惠码次数已用完", nil
		}
	}
	return c, "", nil
}

// buy 对应 UserController::buy：用余额购买套餐。
//
// 与原版的差异（K32）：优惠码不存在时原版会因空对象报错，这里提示“优惠码无效”；原版购买时不检查优惠码使用次数
// （只在“检查优惠码”时检查，可绕过），这里一并检查；余额检查与扣款在事务中完成，防止并发透支。
func (a *App) buy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	code := trimmed(r, "coupon")
	s, err := a.shopParam(r)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	if s == nil {
		a.json(w, fail("非法请求"))
		return
	}
	var credit int32
	onetime := false
	if code != "" {
		c, msg, err := a.checkCoupon(ctx, u.ID, code, s.ID, true)
		if err != nil {
			a.jsonError(w, r, err)
			return
		}
		if msg != "" {
			a.json(w, fail(msg))
			return
		}
		credit, onetime = c.Credit, c.Onetime == 1
	}
	// 原版：$price = 售价 * (100 - 折扣) / 100，余额用 bcsub 保留两位小数（截断），
	// 等同于按“分”向上取整扣款。
	priceFloat := s.Price.Float() * (float64(100-credit) / 100)
	charge := dec.D((int64(s.Price)*int64(100-credit) + 99) / 100)
	now := time.Now()
	sm := model.NewShop(*s)

	err = a.tx(ctx, func(q *store.Queries) error {
		cur, err := q.GetUserForUpdate(ctx, u.ID)
		if err != nil {
			return err
		}
		if cur.Money < charge {
			return errMsg("喵喵喵~ 当前余额不足，总价为" + util.PHPFloat(priceFloat) + "元。</br><a href=\"/user/code\">点击进入充值界面</a>")
		}
		cur.Money -= charge
		// 每个帐号只有最新购买的套餐自动续费：旧套餐继续续费会重复扣款，并把等级改回旧套餐。
		if err := q.DisableUserRenew(ctx, u.ID); err != nil {
			return err
		}
		renew := int64(0)
		if a.Cfg.Users.EnableAutoRenew && param(r, "autorenew") == "1" && s.AutoRenew != 0 {
			renew = now.Unix() + int64(s.AutoRenew)*86400
		}
		// 原版：一次性优惠码的订单记录原价（自动续费按记录的价格扣款）。
		recorded := dec.FromFloat(priceFloat)
		if onetime {
			recorded = s.Price
		}
		if err := q.InsertBought(ctx, store.InsertBoughtParams{
			Userid: u.ID, Shopid: s.ID, Datetime: now.Unix(), Renew: renew, Coupon: code, Price: recorded,
		}); err != nil {
			return err
		}
		sm.Apply(&cur, false, a.Cfg.Users.EnableBoughtReset, a.Cfg.Users.EnableBoughtExtend, now)
		if err := q.UpdateUserAfterBuy(ctx, afterBuyParams(cur)); err != nil {
			return err
		}
		_, err = q.DeductUserMoney(ctx, store.DeductUserMoneyParams{Amount: charge, ID: u.ID})
		return err
	})
	if m, isMsg := err.(errMsg); isMsg { //nolint:errorlint // errMsg 只在本包内直接返回
		a.json(w, fail(string(m)))
		return
	}
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("购买成功"))
}

// afterBuyParams 把套餐生效后的用户字段写回数据库。
func afterBuyParams(u store.User) store.UpdateUserAfterBuyParams {
	return store.UpdateUserAfterBuyParams{
		TransferEnable: u.TransferEnable, U: u.U, D: u.D, LastDayT: u.LastDayT, ExpireIn: u.ExpireIn,
		Class: u.Class, ClassExpire: u.ClassExpire, NodeSpeedlimit: u.NodeSpeedlimit, NodeConnector: u.NodeConnector, ID: u.ID,
	}
}

// bought 对应 UserController::bought：购买记录。
func (a *App) bought(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	total, err := a.Q.CountBoughtByUser(ctx, u.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p := newPager(r, "/user/bought", 15, total)
	list, err := a.Q.ListBoughtByUser(ctx, store.ListBoughtByUserParams{Userid: u.ID, Limit: p.Limit(), Offset: p.Offset()})
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	ids := make([]uint64, 0, len(list))
	for _, b := range list {
		ids = append(ids, b.Shopid)
	}
	shops := map[uint64]*model.Shop{}
	if len(ids) > 0 {
		sl, err := a.Q.ListShopsByIDs(ctx, ids)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		for _, s := range sl {
			shops[s.ID] = model.NewShop(s)
		}
	}
	rows := make([]map[string]any, 0, len(list))
	for _, b := range list {
		s := shops[b.Shopid]
		if s == nil { // 商品已删除：原版 shop() 返回 null，名称与说明为空
			s = model.NewShop(store.Shop{})
		}
		rows = append(rows, map[string]any{
			"ID": b.ID, "Datetime": util.ToDateTime(b.Datetime, a.Loc), "Shop": s, "Price": b.Price,
			"Renew": b.Renew, "RenewDate": util.ToDateTime(b.Renew, a.Loc),
		})
	}
	p.Items = rows
	a.userPage(w, r, "user/bought", map[string]any{"shops": p, "auto_renew": a.Cfg.Users.EnableAutoRenew})
}

// deleteBought 对应 UserController::deleteBoughtGet：关闭自动续费。
func (a *App) deleteBought(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(param(r, "id"), 10, 64)
	if err != nil {
		a.json(w, fail("关闭自动续费失败，订单不存在。"))
		return
	}
	n, err := a.Q.DisableBoughtRenew(r.Context(), store.DisableBoughtRenewParams{ID: id, Userid: auth.User(r).ID})
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	if n == 0 {
		// 已经关闭的订单 UPDATE 影响 0 行，需区分是否存在。
		if _, err := a.Q.GetBoughtForUser(r.Context(), store.GetBoughtForUserParams{ID: id, Userid: auth.User(r).ID}); isNoRows(err) {
			a.json(w, fail("关闭自动续费失败，订单不存在。"))
			return
		}
	}
	a.json(w, ok("关闭自动续费成功"))
}

// code 对应 UserController::code：充值页面与充值记录。
func (a *App) code(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	total, err := a.Q.CountUserCodes(ctx, u.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p := newPager(r, "/user/code", 15, total)
	list, err := a.Q.ListUserCodes(ctx, store.ListUserCodesParams{Userid: u.ID, Limit: p.Limit(), Offset: p.Offset()})
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	rows := make([]map[string]any, 0, len(list))
	for _, c := range list {
		rows = append(rows, map[string]any{"ID": c.ID, "Code": c.Code, "Type": c.Type, "Number": c.Number, "Usedatetime": u.FormatTime(c.Usedatetime)})
	}
	p.Items = rows
	// 原版 Payment::purchaseHTML 返回渠道的 HTML 片段；这里传渠道名，由模板引入对应片段。
	pmw := ""
	if a.purchaseEnabled() {
		pmw = a.Cfg.Payment.System
	}
	a.userPage(w, r, "user/code", map[string]any{"codes": p, "pmw": pmw, "bitpay": "", "purchase_notice": a.purchaseNotice()})
}

// codePost 对应 UserController::codepost：使用充值码。
//
// 原版先查询再标记已使用，同一个充值码并发提交可以被使用多次；这里在事务中锁定该充值码（K33）。
func (a *App) codePost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	codeStr := trimmed(r, "code")
	if codeStr == "" {
		a.json(w, fail("非法输入"))
		return
	}
	now := time.Now()
	var res Ret
	err := a.tx(ctx, func(q *store.Queries) error {
		c, err := q.GetUnusedCodeForUpdate(ctx, codeStr)
		if isNoRows(err) {
			res = fail("此充值码错误")
			return nil
		}
		if err != nil {
			return err
		}
		if err := q.MarkCodeUsed(ctx, store.MarkCodeUsedParams{Usedatetime: now, Userid: u.ID, ID: c.ID}); err != nil {
			return err
		}
		cur, err := q.GetUserForUpdate(ctx, u.ID)
		if err != nil {
			return err
		}
		switch {
		case c.Type == -1:
			if err := q.AddUserMoney(ctx, store.AddUserMoneyParams{Amount: c.Number, ID: u.ID}); err != nil {
				return err
			}
			// code_payback 为 0 时不返利，也不写返利记录。
			if cur.RefBy != 0 && a.Cfg.Users.CodePayback > 0 {
				get := c.Number.MulPercent(a.Cfg.Users.CodePayback)
				if err := q.AddUserMoney(ctx, store.AddUserMoneyParams{Amount: get, ID: cur.RefBy}); err != nil {
					return err
				}
				if err := q.InsertPayback(ctx, store.InsertPaybackParams{Total: c.Number, Userid: u.ID, RefBy: cur.RefBy, RefGet: get, Datetime: now.Unix()}); err != nil {
					return err
				}
			}
			if a.Cfg.Display.EnableDonate {
				if cur.IsHide == 1 {
					a.tg("姐姐姐姐，一位不愿透露姓名的大老爷给我们捐了 "+c.Number.String()+" 元呢~", false)
				} else {
					a.tg("姐姐姐姐，"+cur.UserName+" 大老爷给我们捐了 "+c.Number.String()+" 元呢~", false)
				}
			}
			res = ok("充值成功，充值的金额为" + c.Number.String() + "元。")
		case c.Type == 10001:
			if err := q.AddUserTransferEnable(ctx, store.AddUserTransferEnableParams{TransferEnable: int64(c.Number.Float() * util.GB), ID: u.ID}); err != nil {
				return err
			}
			res = ok("兑换成功")
		case c.Type == 10002:
			add := time.Duration(c.Number.Float() * 86400 * float64(time.Second))
			exp := cur.ExpireIn.Add(add)
			if now.After(cur.ExpireIn) {
				exp = now.Add(add)
			}
			if err := q.UpdateUserExpireIn(ctx, store.UpdateUserExpireInParams{ExpireIn: exp, ID: u.ID}); err != nil {
				return err
			}
			res = ok("兑换成功")
		case c.Type >= 1 && c.Type <= 10000:
			base := cur.ClassExpire
			if cur.Class == 0 || cur.Class != c.Type {
				base = now
			}
			exp := base.Add(time.Duration(c.Number.Float() * 86400 * float64(time.Second)))
			if err := q.UpdateUserClass(ctx, store.UpdateUserClassParams{Class: c.Type, ClassExpire: exp, ID: u.ID}); err != nil {
				return err
			}
			res = ok("兑换成功")
		default:
			res = ok("兑换成功")
		}
		return nil
	})
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	// 原版除充值余额外，其他类型的充值码成功后不返回任何内容（页面显示“出现了一些错误”），这里统一返回结果。
	a.json(w, res)
}

// codeCheck 对应 UserController::code_check：支付页面轮询是否已到账。
func (a *App) codeCheck(w http.ResponseWriter, r *http.Request) {
	ts, err := strconv.ParseInt(r.URL.Query().Get("time"), 10, 64)
	if err != nil {
		a.json(w, Ret{"ret": 0})
		return
	}
	c, err := a.Q.FindRechargeCodeAfter(r.Context(), store.FindRechargeCodeAfterParams{Userid: auth.User(r).ID, Usedatetime: time.Unix(ts, 0).UTC()})
	if err == nil && strings.Contains(c.Code, "充值") {
		a.json(w, Ret{"ret": 1})
		return
	}
	if err != nil && !isNoRows(err) {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, Ret{"ret": 0})
}

// userInviteCode 对应原版 invite 中“没有邀请码就生成一个”（User::addInviteCode）。
//
// 原版循环检查的是“该用户是否已有邀请码”而非“新码是否重复”，4 位随机码重复时写入失败（K28）。
func (a *App) userInviteCode(ctx context.Context, uid uint32) (store.SsInviteCode, error) {
	c, err := a.Q.GetUserInviteCode(ctx, uid)
	if !isNoRows(err) {
		return c, err
	}
	for i := 0; i < 20; i++ {
		code := util.RandomChar(4)
		if n, err := a.Q.CountInviteCode(ctx, code); err != nil {
			return c, err
		} else if n > 0 {
			continue
		}
		if err := a.Q.InsertInviteCode(ctx, store.InsertInviteCodeParams{Code: code, UserID: uid}); err != nil {
			return c, err
		}
		return a.Q.GetUserInviteCode(ctx, uid)
	}
	return c, errMsg("无法生成邀请码")
}

// invite 对应 UserController::invite：邀请链接与返利记录。
func (a *App) invite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	code, err := a.userInviteCode(ctx, u.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	// 不做返利：邀请页不显示返利说明与返利记录（后台仍可查看原有记录）。
	a.userPage(w, r, "user/invite", map[string]any{"code": code})
}

// buyInvite 对应 UserController::buyInvite：购买邀请次数。
func (a *App) buyInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	price := a.Cfg.Register.InvitePrice
	numStr := trimmed(r, "num")
	num, err := strconv.ParseInt(numStr, 10, 32)
	if !util.IsInt(numStr) || err != nil || price < 0 || num <= 0 {
		a.json(w, fail("非法请求"))
		return
	}
	n32 := int32(num) //nolint:gosec // ParseInt 已限制在 32 位范围内
	amount := price * float64(num)
	charge := dec.FromFloat(amount)
	var inviteNum int32
	err = a.tx(ctx, func(q *store.Queries) error {
		cur, err := q.GetUserForUpdate(ctx, u.ID)
		if err != nil {
			return err
		}
		if cur.Money < charge {
			return errMsg("余额不足，总价为" + util.PHPFloat(amount) + "元。")
		}
		if err := q.AddUserInviteNum(ctx, store.AddUserInviteNumParams{InviteNum: n32, ID: u.ID}); err != nil {
			return err
		}
		inviteNum = cur.InviteNum + n32
		_, err = q.DeductUserMoney(ctx, store.DeductUserMoneyParams{Amount: charge, ID: u.ID})
		return err
	})
	if m, isMsg := err.(errMsg); isMsg { //nolint:errorlint // errMsg 只在本包内直接返回
		a.json(w, fail(string(m)))
		return
	}
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, Ret{"ret": 1, "msg": "邀请次数添加成功", "invite_num": inviteNum})
}

// customInvite 对应 UserController::customInvite：定制邀请链接后缀。
func (a *App) customInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	price := a.Cfg.Register.CustomInvitePrice
	custom := trimmed(r, "customcode")
	if !util.IsValidate(custom) || price < 0 || custom == "" || len(custom) > 32 {
		a.json(w, fail("非法请求,邀请链接后缀不能包含特殊符号且长度不能大于32字符"))
		return
	}
	charge := dec.FromFloat(price)
	err := a.tx(ctx, func(q *store.Queries) error {
		if n, err := q.CountInviteCode(ctx, custom); err != nil {
			return err
		} else if n > 0 {
			return errMsg("此后缀名被抢注了")
		}
		cur, err := q.GetUserForUpdate(ctx, u.ID)
		if err != nil {
			return err
		}
		if cur.Money < charge {
			return errMsg("余额不足，总价为" + util.PHPFloat(price) + "元。")
		}
		code, err := q.GetUserInviteCode(ctx, u.ID)
		if isNoRows(err) {
			return errMsg("请先打开邀请页面生成邀请链接")
		} else if err != nil {
			return err
		}
		if err := q.UpdateInviteCode(ctx, store.UpdateInviteCodeParams{Code: custom, ID: code.ID}); err != nil {
			return err
		}
		_, err = q.DeductUserMoney(ctx, store.DeductUserMoneyParams{Amount: charge, ID: u.ID})
		return err
	})
	if m, isMsg := err.(errMsg); isMsg { //nolint:errorlint // errMsg 只在本包内直接返回
		a.json(w, fail(string(m)))
		return
	}
	if err != nil {
		// 两个用户同时抢注同一后缀时，唯一索引会拒绝后一个。
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("定制成功"))
}

// resetInviteURL 对应 UserController::resetInviteURL：删除邀请码，再次打开邀请页时生成新的。
func (a *App) resetInviteURL(w http.ResponseWriter, r *http.Request) {
	if err := a.Q.DeleteUserInviteCodes(r.Context(), auth.User(r).ID); err != nil {
		a.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/user/invite", http.StatusFound)
}
