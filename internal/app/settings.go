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
	"github.com/SadNoo/SS-No-PHP/internal/server"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// themes 是可选的主题（原版读取 resources/views 下的目录，只有 material）。
var themes = []string{"material"}

func (a *App) settingsRoutes(r chi.Router) {
	r.Get("/profile", a.profile)
	r.Get("/edit", a.edit)
	r.Post("/password", a.updatePassword)
	r.Post("/wechat", a.updateWechat)
	r.Post("/theme", a.updateTheme)
	r.Post("/mail", a.updateMail)
	r.Post("/sspwd", a.updateSsPwd)
	r.Post("/hide", a.updateHide)
	r.Post("/gacheck", a.gaCheck)
	r.Post("/gaset", a.gaSet)
	r.Post("/resetport", a.resetPort)
	r.Post("/specifyport", a.specifyPort)
	r.Post("/pacset", a.pacSet)
	r.Post("/unblock", a.unblock)
	r.Get("/kill", a.simplePage("user/kill"))
	r.Post("/kill", a.handleKill)
	// K23：原版为 GET。
	r.Post("/gareset", a.gaReset)
	r.Post("/telegram_reset", a.telegramReset)
	r.Post("/discord_reset", a.discordReset)
}

// ipLocation 是页面上显示的 IP 与归属地。
type ipLocation struct {
	IP       string
	Location string
}

// profile 对应 UserController::profile：最近在线 IP、最近登录 IP、返利记录。
func (a *App) profile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	total, err := a.Q.CountPaybacksByRef(ctx, u.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p := newPager(r, "/user/profile", 15, total)
	paybacks, err := a.Q.ListPaybacksByRef(ctx, store.ListPaybacksByRefParams{RefBy: u.ID, Limit: p.Limit(), Offset: p.Offset()})
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if p.Items, err = a.paybackRows(r, paybacks); err != nil {
		a.serverError(w, r, err)
		return
	}

	logins, err := a.Q.ListRecentLoginIPs(ctx, u.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	var loginIPs []ipLocation
	seen := map[string]bool{}
	for _, l := range logins {
		if !seen[l.Ip] {
			seen[l.Ip] = true
			loginIPs = append(loginIPs, ipLocation{l.Ip, a.Geo.Lookup(l.Ip)})
		}
	}

	alive, err := a.Q.ListAliveIPsSince(ctx, store.ListAliveIPsSinceParams{Userid: u.ID, Datetime: time.Now().Unix() - 300})
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	nodeIPs, err := a.nodeIPSet(r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	var aliveIPs []ipLocation
	seen = map[string]bool{}
	for _, x := range alive {
		ip := util.RealIP(x.Ip)
		if nodeIPs[ip] || seen[ip] {
			continue
		}
		seen[ip] = true
		aliveIPs = append(aliveIPs, ipLocation{ip, a.Geo.Lookup(ip)})
	}
	a.userPage(w, r, "user/profile", map[string]any{"userip": aliveIPs, "userloginip": loginIPs, "paybacks": p})
}

// paybackRows 返利记录及其对应的被邀请用户（原版 Payback::user()）。
func (a *App) paybackRows(r *http.Request, list []store.Payback) ([]map[string]any, error) {
	ids := make([]uint32, 0, len(list))
	for _, x := range list {
		ids = append(ids, x.Userid)
	}
	users, err := a.usersByID(r, ids)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(list))
	for _, x := range list {
		row := map[string]any{"ID": x.ID, "Total": x.Total, "RefGet": x.RefGet, "Datetime": x.Datetime, "User": nil}
		if pu, ok := users[x.Userid]; ok {
			row["User"] = pu
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// edit 对应 UserController::edit：资料编辑页。
func (a *App) edit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	ip := server.ClientIP(r)
	block, isBlock := "IP: "+ip+" 没有被封", 0
	if _, err := a.Q.GetBlockIP(ctx, ip); err == nil {
		block, isBlock = "IP: "+ip+" 已被封", 1
	} else if !isNoRows(err) {
		a.serverError(w, r, err)
		return
	}
	bindToken, err := a.telegramBindToken(ctx, u.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.userPage(w, r, "user/edit", map[string]any{
		"themes":       themes,
		"isBlock":      isBlock,
		"Block":        block,
		"bind_token":   bindToken,
		"telegram_bot": a.Cfg.Telegram.Bot,
	})
}

// telegramBindToken 对应 TelegramSessionManager::add_bind_session：生成（或刷新）绑定 Telegram 用的令牌。
func (a *App) telegramBindToken(ctx context.Context, uid uint32) (string, error) {
	token := ""
	for i := 0; i < 10; i++ {
		t := util.RandomChar(16)
		n, err := a.Q.CountTelegramSessionToken(ctx, t)
		if err != nil {
			return "", err
		}
		if n == 0 {
			token = t
			break
		}
	}
	if token == "" {
		return "couldn't alloc token", nil
	}
	now := time.Now().Unix()
	s, err := a.Q.GetTelegramBindSession(ctx, uid)
	if err == nil {
		return token, a.Q.UpdateTelegramSession(ctx, store.UpdateTelegramSessionParams{SessionContent: token, Datetime: now, ID: s.ID})
	}
	if !isNoRows(err) {
		return "", err
	}
	return token, a.Q.InsertTelegramSession(ctx, store.InsertTelegramSessionParams{UserID: uid, Type: 0, SessionContent: token, Datetime: now})
}

// updatePassword 对应 UserController::updatePassword。
func (a *App) updatePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	oldpwd, pwd, repwd := param(r, "oldpwd"), param(r, "pwd"), param(r, "repwd")
	legacy := auth.Legacy{Method: a.Cfg.Security.LegacyPwdMethod, Salt: a.Cfg.Security.LegacySalt}
	if ok, _ := auth.VerifyPassword(u.Pass, oldpwd, legacy); !ok {
		a.json(w, fail("旧密码错误"))
		return
	}
	if pwd != repwd {
		a.json(w, fail("两次输入不符合"))
		return
	}
	if len(pwd) < 8 {
		a.json(w, fail("密码太短啦"))
		return
	}
	hash, err := auth.HashPassword(pwd)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	err = a.tx(ctx, func(q *store.Queries) error {
		if err := q.UpdateUserPass(ctx, store.UpdateUserPassParams{Pass: hash, ID: u.ID}); err != nil {
			return err
		}
		return q.DeleteLinksByUser(ctx, u.ID) // 原版 clean_link：订阅链接随之重置
	})
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	if err := a.Auth.RefreshFingerprint(ctx, hash); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("修改成功"))
}

// updateWechat 对应 UserController::updateWechat：修改联络方式。
func (a *App) updateWechat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	wechat := trimmed(r, "wechat")
	typ := param(r, "imtype")
	if u.TelegramID != 0 {
		a.json(w, fail("您绑定了 Telegram ，所以此项并不能被修改。"))
		return
	}
	if u.Discord != 0 {
		a.json(w, fail("您绑定了 Discord ，所以此项并不能被修改。"))
		return
	}
	imType, err := strconv.ParseInt(typ, 10, 32)
	if wechat == "" || typ == "" || err != nil {
		a.json(w, fail("非法输入"))
		return
	}
	n, err := a.Q.CountUsersByIM(ctx, store.CountUsersByIMParams{ImValue: wechat, ImType: int32(imType)})
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	if n > 0 {
		a.json(w, fail("此联络方式已经被注册"))
		return
	}
	// 原版用 AntiXSS 过滤后保存；新版原样保存，输出时由模板统一转义。
	if err := a.Q.UpdateUserIM(ctx, store.UpdateUserIMParams{ImType: int32(imType), ImValue: wechat, ID: u.ID}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("修改成功"))
}

// updateTheme 对应 UserController::updateTheme。原版不校验主题名，填错会导致页面无法打开；只接受已有主题。
func (a *App) updateTheme(w http.ResponseWriter, r *http.Request) {
	theme := param(r, "theme")
	valid := false
	for _, t := range themes {
		valid = valid || t == theme
	}
	if !valid {
		a.json(w, fail("非法输入"))
		return
	}
	if err := a.Q.UpdateUserTheme(r.Context(), store.UpdateUserThemeParams{Theme: theme, ID: auth.User(r).ID}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("设置成功"))
}

// zeroOne 解析只允许 0 / 1 的设置项。
func zeroOne(s string) (int32, bool) {
	switch strings.TrimSpace(s) {
	case "0":
		return 0, true
	case "1":
		return 1, true
	}
	return 0, false
}

// updateMail 对应 UserController::updateMail：是否接收每日流量报告邮件。
func (a *App) updateMail(w http.ResponseWriter, r *http.Request) {
	v, valid := zeroOne(param(r, "mail"))
	if !valid {
		a.json(w, fail("非法输入"))
		return
	}
	if err := a.Q.UpdateUserDailyMail(r.Context(), store.UpdateUserDailyMailParams{Senddailymail: v, ID: auth.User(r).ID}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("修改成功"))
}

// updateHide 对应 UserController::updateHide：捐赠时是否匿名。原版不校验取值，这里只接受 0 / 1。
func (a *App) updateHide(w http.ResponseWriter, r *http.Request) {
	v, valid := zeroOne(param(r, "hide"))
	if !valid {
		a.json(w, fail("非法输入"))
		return
	}
	if err := a.Q.UpdateUserHide(r.Context(), store.UpdateUserHideParams{IsHide: v, ID: auth.User(r).ID}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("修改成功"))
}

// pacSet 对应 UserController::PacSet。
func (a *App) pacSet(w http.ResponseWriter, r *http.Request) {
	pac := param(r, "pac")
	if pac == "" {
		a.json(w, fail("输入不能为空"))
		return
	}
	if err := a.Q.UpdateUserPac(r.Context(), store.UpdateUserPacParams{Pac: pac, ID: auth.User(r).ID}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("修改成功"))
}

// updateSsPwd 对应 UserController::updateSsPwd：修改节点连接密码。
func (a *App) updateSsPwd(w http.ResponseWriter, r *http.Request) {
	pwd := trimmed(r, "sspwd")
	if pwd == "" {
		a.json(w, fail("密码不能为空"))
		return
	}
	if !util.IsValidate(pwd) {
		a.json(w, fail("密码无效"))
		return
	}
	u := auth.User(r)
	if err := a.Q.UpdateUserPasswd(r.Context(), store.UpdateUserPasswdParams{Passwd: pwd, ID: u.ID}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.radiusAdd(r.Context(), u.User, pwd)
	a.json(w, Ret{"ret": 1})
}

// gaCheck 对应 UserController::GaCheck：测试两步验证码。
func (a *App) gaCheck(w http.ResponseWriter, r *http.Request) {
	code := param(r, "code")
	if code == "" {
		a.json(w, fail("二维码不能为空"))
		return
	}
	if !auth.VerifyTOTP(auth.User(r).GaToken, code, time.Now()) {
		a.json(w, fail("测试错误"))
		return
	}
	a.json(w, ok("测试成功"))
}

// gaSet 对应 UserController::GaSet：开启或关闭登录两步验证。原版不校验取值，这里只接受 0 / 1。
func (a *App) gaSet(w http.ResponseWriter, r *http.Request) {
	v, valid := zeroOne(param(r, "enable"))
	if !valid {
		a.json(w, fail("选项无效"))
		return
	}
	if err := a.Q.UpdateUserGaEnable(r.Context(), store.UpdateUserGaEnableParams{GaEnable: v, ID: auth.User(r).ID}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok("设置成功"))
}

// gaReset 对应 UserController::GaReset：重新生成两步验证密钥。
func (a *App) gaReset(w http.ResponseWriter, r *http.Request) {
	if err := a.Q.UpdateUserGaToken(r.Context(), store.UpdateUserGaTokenParams{GaToken: auth.NewTOTPSecret(), ID: auth.User(r).ID}); err != nil {
		a.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/user/edit", http.StatusFound)
}

// telegramReset 对应 UserController::telegram_reset：解绑 Telegram。
func (a *App) telegramReset(w http.ResponseWriter, r *http.Request) {
	if err := a.Q.ResetUserTelegram(r.Context(), auth.User(r).ID); err != nil {
		a.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/user/edit", http.StatusFound)
}

// discordReset 对应 UserController::discord_reset：解绑 Discord。
func (a *App) discordReset(w http.ResponseWriter, r *http.Request) {
	if err := a.Q.ResetUserDiscord(r.Context(), auth.User(r).ID); err != nil {
		a.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/user/edit", http.StatusFound)
}

// errMsg 是直接返回给用户的业务错误（在事务中返回以回滚）。
type errMsg string

func (e errMsg) Error() string { return string(e) }

// changePort 更换端口：扣款、同步中转规则中的端口（原版 ResetPort / SpecifyPort 的公共部分）。
// price 为负数表示该功能未开放（K30：原版此时仍可调用，且负价格会给用户加钱）。
func (a *App) changePort(ctx context.Context, u store.User, price float64, pick func(q *store.Queries) (int32, error)) (int32, error) {
	if price < 0 {
		return 0, errMsg("此功能未开放")
	}
	var port int32
	err := a.tx(ctx, func(q *store.Queries) error {
		cur, err := q.GetUserForUpdate(ctx, u.ID)
		if err != nil {
			return err
		}
		amount := dec.FromFloat(price)
		if cur.Money < amount {
			return errMsg("余额不足")
		}
		if port, err = pick(q); err != nil {
			return err
		}
		if err := q.UpdateRelayPortForUser(ctx, store.UpdateRelayPortForUserParams{NewPort: port, UserID: u.ID, OldPort: cur.Port}); err != nil {
			return err
		}
		if err := q.UpdateUserPort(ctx, store.UpdateUserPortParams{Port: port, ID: u.ID}); err != nil {
			return err
		}
		_, err = q.DeductUserMoney(ctx, store.DeductUserMoneyParams{Amount: amount, ID: u.ID})
		return err
	})
	return port, err
}

// portResult 输出更换端口的结果。
func (a *App) portResult(w http.ResponseWriter, r *http.Request, msg string, err error) {
	if m, isMsg := err.(errMsg); isMsg { //nolint:errorlint // errMsg 只在本文件内直接返回
		a.json(w, fail(string(m)))
		return
	}
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok(msg))
}

// resetPort 对应 UserController::ResetPort：随机更换端口。
func (a *App) resetPort(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	port, err := a.changePort(ctx, auth.User(r).User, a.Cfg.Users.PortPrice, func(q *store.Queries) (int32, error) {
		return a.availablePortQ(ctx, q)
	})
	if err == nil {
		a.json(w, Ret{"ret": 1, "msg": port}) // 原版 msg 字段为新端口号
		return
	}
	a.portResult(w, r, "", err)
}

// specifyPort 对应 UserController::SpecifyPort：指定端口。
func (a *App) specifyPort(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raw := param(r, "port")
	n, convErr := strconv.Atoi(raw)
	_, err := a.changePort(ctx, auth.User(r).User, a.Cfg.Users.PortPriceSpecify, func(q *store.Queries) (int32, error) {
		if convErr != nil || !util.IsInt(raw) || n < a.Cfg.Register.MinPort || n > a.Cfg.Register.MaxPort {
			return 0, errMsg("端口不在要求范围内")
		}
		used, err := q.CountUsersByPort(ctx, int32(n)) //nolint:gosec // 已校验端口范围
		if err != nil {
			return 0, err
		}
		if used > 0 {
			return 0, errMsg("端口已被占用")
		}
		return int32(n), nil //nolint:gosec // 已校验端口范围
	})
	a.portResult(w, r, "钦定成功", err)
}

// unblock 对应 UserController::Unblock：解封当前 IP。
func (a *App) unblock(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := server.ClientIP(r)
	err := a.tx(ctx, func(q *store.Queries) error {
		if err := deleteBlocksForIP(ctx, q, ip); err != nil {
			return err
		}
		return q.InsertUnblockIP(ctx, store.InsertUnblockIPParams{Userid: auth.User(r).ID, Ip: ip, Datetime: time.Now().Unix()})
	})
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, ok(ip))
}

// handleKill 对应 UserController::handleKill：用户自行删除账户。
func (a *App) handleKill(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	legacy := auth.Legacy{Method: a.Cfg.Security.LegacyPwdMethod, Salt: a.Cfg.Security.LegacySalt}
	if ok, _ := auth.VerifyPassword(u.Pass, param(r, "passwd"), legacy); !ok {
		a.json(w, fail(" 密码错误"))
		return
	}
	if !a.Cfg.Users.EnableKill {
		a.json(w, fail("管理员不允许删除，如需删除请联系管理员。"))
		return
	}
	if err := a.killUser(ctx, u.User); err != nil {
		a.jsonError(w, r, err)
		return
	}
	_ = a.Auth.Logout(ctx)
	a.json(w, ok("您的帐号已经从我们的系统中删除。欢迎下次光临!"))
}

// killUser 对应 User::kill_user：删除用户及其全部关联数据（新版在一个事务中完成）。
func (a *App) killUser(ctx context.Context, u store.User) error {
	err := a.disconnectTx(ctx, func(q *store.Queries) error {
		// 先锁住用户行，再删除关联数据：与写入 disconnect_ip 的事务（每分钟检查、到期解除、修复命令）加锁顺序相同。
		if _, err := q.LockUserRow(ctx, u.ID); err != nil && !isNoRows(err) {
			return err
		}
		for _, del := range []func(context.Context, uint32) error{
			q.KillRadiusBan, q.KillDisconnectIP, q.KillBought, q.KillAliveIP, q.KillCode, q.KillDetectLog,
			q.DeleteLinksByUser, q.KillLoginIP, q.KillInviteCode, q.KillTelegramSession, q.KillUnblockIP,
			q.KillTrafficLog, q.KillUserToken, q.KillIPOnline,
		} {
			if err := del(ctx, u.ID); err != nil {
				return err
			}
		}
		if err := q.KillPasswordReset(ctx, u.Email); err != nil {
			return err
		}
		return q.DeleteUser(ctx, u.ID)
	})
	if err == nil {
		a.radiusDelete(ctx, u.Email)
	}
	return err
}
