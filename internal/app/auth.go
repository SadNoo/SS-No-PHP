package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/auth"
	"github.com/SadNoo/SS-No-PHP/internal/captcha"
	"github.com/SadNoo/SS-No-PHP/internal/dec"
	"github.com/SadNoo/SS-No-PHP/internal/server"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

func (a *App) authRoutes(r chi.Router) {
	r.Get("/login", a.login)
	r.With(a.loginLimit.Middleware).Post("/qrcode_check", a.qrcodeCheck)
	r.With(a.loginLimit.Middleware).Post("/login", a.loginHandle)
	r.With(a.loginLimit.Middleware).Post("/qrcode_login", a.qrcodeLoginHandle)
	r.Get("/register", a.register)
	r.With(a.regLimit.Middleware).Post("/register", a.registerHandle)
	r.With(a.mailLimit.Middleware).Post("/send", a.sendVerify)
	r.Get("/logout", a.logout)
	r.Get("/telegram_oauth", a.telegramOAuth)
	r.Get("/login_getCaptcha", a.getCaptcha)
}

// captchaData 把验证码数据转为模板变量（原版 geetest_html / recaptcha_sitekey）。
func captchaData(v captcha.View, data map[string]any) {
	data["recaptcha_sitekey"] = v.RecaptchaSitekey
	if v.Geetest != nil {
		data["geetest_html"] = v.Geetest
	} else {
		data["geetest_html"] = nil
	}
}

// login 对应 AuthController::login。
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"telegram_bot": a.Cfg.Telegram.Bot,
		"base_url":     a.Cfg.App.BaseURL,
		"login_token":  "",
		"login_number": "",
	}
	captchaData(a.Captcha.Prepare(r.Context(), captcha.Login), data)
	if a.Cfg.Telegram.Enable {
		token, number, err := a.addTelegramLoginSession(r.Context())
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		data["login_token"], data["login_number"] = token, number
	}
	a.render(w, r, "auth/login", data)
}

// getCaptcha 对应 AuthController::getCaptcha。
func (a *App) getCaptcha(w http.ResponseWriter, r *http.Request) {
	v := a.Captcha.PrepareAlways(r.Context())
	res := Ret{"respon": 1}
	if v.RecaptchaSitekey != "" {
		res["recaptchaKey"] = v.RecaptchaSitekey
	}
	if v.Geetest != nil {
		res["GtSdk"] = v.Geetest
	}
	a.json(w, res)
}

func (a *App) logLogin(ctx context.Context, uid uint32, ip string, failed bool) {
	typ := int32(0)
	if failed {
		typ = 1
	}
	if err := a.Q.InsertLoginIP(ctx, store.InsertLoginIPParams{Userid: uid, Ip: ip, Datetime: time.Now().Unix(), Type: typ}); err != nil {
		a.Log.Warn("记录登录 IP 失败", "uid", uid, "err", err)
	}
}

// loginHandle 对应 AuthController::loginHandle。
func (a *App) loginHandle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	email := strings.ToLower(trimmed(r, "email"))
	passwd := param(r, "passwd")
	code := param(r, "code")
	remember := param(r, "remember_me") != ""
	ip := server.ClientIP(r)

	if err := r.ParseForm(); err != nil {
		a.json(w, fail("请求格式错误"))
		return
	}
	if !a.Captcha.Verify(ctx, captcha.Login, r.Form, ip) {
		a.json(w, fail("系统无法接受您的验证结果，请刷新页面后重试。"))
		return
	}

	u, err := a.Q.GetUserByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		// K19：原版提示“邮箱不存在”，可被用来探测哪些邮箱已注册；统一提示，并做一次哈希计算使响应时间一致。
		auth.VerifyPassword("$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", passwd, auth.Legacy{})
		a.json(w, fail("邮箱或者密码错误"))
		return
	}
	if err != nil {
		a.jsonError(w, r, err)
		return
	}

	legacy := auth.Legacy{Method: a.Cfg.Security.LegacyPwdMethod, Salt: a.Cfg.Security.LegacySalt}
	okPass, rehash := auth.VerifyPassword(u.Pass, passwd, legacy)
	if !okPass {
		a.logLogin(ctx, u.ID, ip, true)
		a.json(w, fail("邮箱或者密码错误"))
		return
	}

	if u.GaEnable == 1 && !auth.VerifyTOTP(u.GaToken, code, time.Now()) {
		a.json(w, fail("两步验证码错误，如果您是丢失了生成器或者错误地设置了这个选项，您可以尝试重置密码，即可取消这个选项。"))
		return
	}

	// K08：旧格式密码登录成功后自动升级为 argon2id。
	if rehash {
		if h, err := auth.HashPassword(passwd); err == nil {
			if err := a.Q.UpdateUserPass(ctx, store.UpdateUserPassParams{Pass: h, ID: u.ID}); err != nil {
				a.Log.Warn("升级密码哈希失败", "uid", u.ID, "err", err)
			} else {
				u.Pass = h
			}
		}
	}

	if err := a.Auth.Login(ctx, u, ip, remember); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.logLogin(ctx, u.ID, ip, false)
	a.json(w, ok("登录成功"))
}

// qrcodeLoginHandle 对应 AuthController::qrcode_loginHandle（Telegram 扫码/数字登录的第二步）。
func (a *App) qrcodeLoginHandle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, err := a.verifyTelegramLoginStep2(ctx, param(r, "token"), param(r, "number"))
	if err != nil || uid == 0 {
		a.json(w, fail("此令牌无法被使用。"))
		return
	}
	u, err := a.Q.GetUser(ctx, uid)
	if err != nil {
		a.json(w, fail("此令牌无法被使用。"))
		return
	}
	if err := a.Auth.Login(ctx, u, server.ClientIP(r), false); err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.logLogin(ctx, u.ID, server.ClientIP(r), false)
	a.json(w, ok("登录成功"))
}

// qrcodeCheck 对应 AuthController::qrcode_check：轮询 Telegram 登录状态。
func (a *App) qrcodeCheck(w http.ResponseWriter, r *http.Request) {
	if !a.Cfg.Telegram.Enable {
		a.json(w, Ret{"ret": 0})
		return
	}
	ret, err := a.checkTelegramLoginSession(r.Context(), param(r, "token"), param(r, "number"))
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, Ret{"ret": ret})
}

// register 对应 AuthController::register。
func (a *App) register(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"enable_email_verify": strconv.FormatBool(a.Cfg.Register.EnableEmailVerify),
		// 原版用 AntiXSS 清理后输出；新版模板输出时自动转义，这里只限制长度。
		"code": limitLen(r.URL.Query().Get("code"), 128),
	}
	captchaData(a.Captcha.Prepare(r.Context(), captcha.Register), data)
	a.render(w, r, "auth/register", data)
}

func limitLen(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// isEmailLegal 对应 Check::isEmailLegal：格式合法且长度不超过 32（与原版一致）。
func isEmailLegal(email string) bool {
	if len(email) > 32 || !emailRe.MatchString(email) {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}

// sendVerify 对应 AuthController::sendVerify：发送注册邮箱验证码。
func (a *App) sendVerify(w http.ResponseWriter, r *http.Request) {
	if !a.Cfg.Register.EnableEmailVerify {
		a.json(w, Ret{"ret": 0})
		return
	}
	ctx := r.Context()
	email := trimmed(r, "email")
	if email == "" {
		a.json(w, fail("未填写邮箱"))
		return
	}
	if !isEmailLegal(email) {
		a.json(w, fail("邮箱无效"))
		return
	}
	if n, err := a.Q.CountUsersByEmail(ctx, email); err != nil {
		a.jsonError(w, r, err)
		return
	} else if n > 0 {
		a.json(w, fail("此邮箱已经注册"))
		return
	}
	now := time.Now().Unix()
	ip := server.ClientIP(r)
	ipCount, err := a.Q.CountEmailVerifyByIP(ctx, store.CountEmailVerifyByIPParams{Ip: ip, ExpireIn: now})
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	if ipCount >= int64(a.Cfg.Register.EmailVerifyIPLimit) {
		a.json(w, fail("此IP请求次数过多"))
		return
	}
	mailCount, err := a.Q.CountEmailVerifyByEmail(ctx, store.CountEmailVerifyByEmailParams{Email: email, ExpireIn: now})
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	if mailCount >= 3 {
		a.json(w, fail("此邮箱请求次数过多"))
		return
	}
	code := util.RandomNum(6)
	expire := now + int64(a.Cfg.Register.EmailVerifyTTL)
	if err := a.Q.InsertEmailVerify(ctx, store.InsertEmailVerifyParams{Email: email, Ip: ip, Code: code, ExpireIn: expire}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	err = a.Mail.Send(ctx, email, a.Cfg.App.Name+"- 验证邮件", "auth_verify", map[string]any{
		"code": code, "expire": util.ToDateTime(expire, a.Loc),
	})
	if err != nil {
		a.Log.Warn("发送注册验证码邮件失败", "email", email, "err", err)
		a.json(w, Ret{"ret": 1, "msg": "邮件发送失败，请联系网站管理员。"})
		return
	}
	a.json(w, ok("验证码发送成功，请查收邮件。"))
}

// registerHandle 对应 AuthController::registerHandle。
func (a *App) registerHandle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reg := a.Cfg.Register
	if reg.Mode == "close" {
		a.json(w, fail("未开放注册。"))
		return
	}
	name := param(r, "name")
	email := strings.ToLower(trimmed(r, "email"))
	passwd := param(r, "passwd")
	repasswd := param(r, "repasswd")
	code := trimmed(r, "code")
	imtype := param(r, "imtype")
	emailcode := trimmed(r, "emailcode")
	wechat := trimmed(r, "wechat")
	ip := server.ClientIP(r)

	if err := r.ParseForm(); err != nil {
		a.json(w, fail("请求格式错误"))
		return
	}
	if !a.Captcha.Verify(ctx, captcha.Register, r.Form, ip) {
		a.json(w, fail("系统无法接受您的验证结果，请刷新页面后重试。"))
		return
	}

	// 邀请码：邀请人等级为 0 时不可用；invite_num 为可邀请次数，负数表示不限。
	var inviter *store.User
	ic, err := a.Q.GetInviteCode(ctx, code)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if reg.Mode == "invite" {
			a.json(w, fail("邀请码无效"))
			return
		}
	case err != nil:
		a.jsonError(w, r, err)
		return
	case ic.UserID != 0:
		gu, err := a.Q.GetUser(ctx, ic.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			a.json(w, fail("邀请人不存在"))
			return
		} else if err != nil {
			a.jsonError(w, r, err)
			return
		}
		if gu.Class == 0 {
			a.json(w, fail("邀请人不是VIP"))
			return
		}
		if gu.InviteNum == 0 {
			a.json(w, fail("邀请人可用邀请次数为0"))
			return
		}
		inviter = &gu
	}

	if !isEmailLegal(email) {
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
	if reg.EnableEmailVerify {
		if _, err := a.Q.FindEmailVerify(ctx, store.FindEmailVerifyParams{Email: email, Code: emailcode, ExpireIn: time.Now().Unix()}); err != nil {
			a.json(w, fail("您的邮箱验证码不正确"))
			return
		}
	}
	if len(passwd) < 8 {
		a.json(w, fail("密码请大于8位"))
		return
	}
	if passwd != repasswd {
		a.json(w, fail("两次密码输入不符"))
		return
	}
	if imtype == "" || wechat == "" {
		a.json(w, fail("请填上你的联络方式"))
		return
	}
	imType, err := strconv.Atoi(imtype)
	if err != nil || imType < 1 || imType > 4 {
		a.json(w, fail("请填上你的联络方式"))
		return
	}
	if n, err := a.Q.CountUsersByIM(ctx, store.CountUsersByIMParams{ImValue: wechat, ImType: int32(imType)}); err != nil { //nolint:gosec // 已校验范围
		a.jsonError(w, r, err)
		return
	} else if n > 0 {
		a.json(w, fail("此联络方式已注册"))
		return
	}

	hash, err := auth.HashPassword(passwd)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	port, err := a.availablePort(ctx)
	if err != nil {
		a.json(w, fail("端口已分配完，请联系网站管理员"))
		return
	}
	now := time.Now()
	params := a.newUserParams(limitLen(name, 128), email, hash, ip, port, int32(imType), limitLen(wechat, 255), now) //nolint:gosec // 已校验范围
	err = a.tx(ctx, func(q *store.Queries) error {
		if inviter != nil {
			params.RefBy = inviter.ID
			params.Money = dec.FromFloat(reg.InviteGetMoney)
			n, err := q.GrantInviteReward(ctx, store.GrantInviteRewardParams{TransferEnable: util.GBToBytes(a.Cfg.Users.InviteGift), ID: inviter.ID})
			if err != nil {
				return err
			}
			if n == 0 {
				return errInviteExhausted
			}
		}
		if reg.EnableEmailVerify {
			if err := q.DeleteEmailVerifyByEmail(ctx, email); err != nil {
				return err
			}
		}
		_, err := q.CreateUser(ctx, params)
		return err
	})
	if errors.Is(err, errInviteExhausted) {
		a.json(w, fail("邀请人可用邀请次数为0"))
		return
	}
	if err != nil {
		// 并发注册同一邮箱时由唯一索引拒绝。
		if strings.Contains(err.Error(), "Duplicate entry") {
			a.json(w, fail("邮箱已经被注册了"))
			return
		}
		a.jsonError(w, r, err)
		return
	}
	if nu, err := a.Q.GetUserByEmail(ctx, email); err == nil {
		a.radiusAdd(ctx, nu, nu.Passwd)
	}
	a.json(w, ok("注册成功！正在进入登录界面"))
}

var errInviteExhausted = errors.New("邀请次数已用完")

// availablePort 对应 Tools::getAvPort：在端口池中随机选一个未被使用的端口。
func (a *App) availablePort(ctx context.Context) (int32, error) {
	return a.availablePortQ(ctx, a.Q)
}

// availablePortQ 同 availablePort，可在事务中使用。
func (a *App) availablePortQ(ctx context.Context, q *store.Queries) (int32, error) {
	used, err := q.ListUsedPorts(ctx)
	if err != nil {
		return 0, err
	}
	taken := make(map[int32]bool, len(used))
	for _, p := range used {
		taken[p] = true
	}
	minP, maxP := a.Cfg.Register.MinPort, a.Cfg.Register.MaxPort
	var free []int32
	for p := minP; p <= maxP; p++ {
		if !taken[int32(p)] { //nolint:gosec // 端口范围已校验
			free = append(free, int32(p)) //nolint:gosec // 端口范围已校验
		}
	}
	if len(free) == 0 {
		return 0, errors.New("端口池已用完")
	}
	return free[randIndex(len(free))], nil
}

func randIndex(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(fmt.Sprintf("crypto/rand 不可用: %v", err))
	}
	return int(v.Int64())
}

// randomGroup 对应注册时的 ramdom_group：从逗号分隔的分组中随机选一个。
func randomGroup(groups string) int32 {
	parts := strings.Split(groups, ",")
	g, _ := strconv.Atoi(strings.TrimSpace(parts[randIndex(len(parts))]))
	return int32(g) //nolint:gosec // 分组号来自配置
}

// logout 对应 AuthController::logout。
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	_ = a.Auth.Logout(r.Context())
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

// telegramOAuth 对应 AuthController::telegram_oauth（Telegram Login Widget 回调）。
func (a *App) telegramOAuth(w http.ResponseWriter, r *http.Request) {
	if !a.Cfg.Telegram.Enable {
		http.Redirect(w, r, "/404", http.StatusFound)
		return
	}
	q := r.URL.Query()
	if !telegramOAuthCheck(q, a.Cfg.Telegram.Token, time.Now()) {
		a.render(w, r, "home/telegram_error", map[string]any{"title": "登陆超时或非法构造信息", "message": "很抱歉带来的不便，请重新试试", "redirect": "/auth/login"})
		return
	}
	tgID, _ := strconv.ParseInt(q.Get("id"), 10, 64)
	u, err := a.Q.GetUserByTelegramID(r.Context(), tgID)
	if err != nil {
		a.render(w, r, "home/telegram_error", map[string]any{"title": "您需要先进行邮箱注册后绑定Telegram才能使用授权登录", "message": "很抱歉带来的不便，请重新试试", "redirect": "/auth/login"})
		return
	}
	if err := a.Auth.Login(r.Context(), u, server.ClientIP(r), false); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.logLogin(r.Context(), u.ID, server.ClientIP(r), false)
	a.render(w, r, "home/telegram_success", map[string]any{"title": "登录成功", "message": "正在前往仪表盘", "redirect": "/user"})
}

// telegramOAuthCheck 校验 Telegram Login Widget 的签名，5 分钟内有效。
func telegramOAuthCheck(q map[string][]string, botToken string, now time.Time) bool {
	hash := ""
	if v := q["hash"]; len(v) > 0 {
		hash = v[0]
	}
	if hash == "" || botToken == "" {
		return false
	}
	var pairs []string
	for k, v := range q {
		if k == "hash" || len(v) == 0 {
			continue
		}
		pairs = append(pairs, k+"="+v[0])
	}
	sort.Strings(pairs)
	secret := sha256.Sum256([]byte(botToken))
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(strings.Join(pairs, "\n")))
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(hash)) {
		return false
	}
	authDate, err := strconv.ParseInt(firstOf(q["auth_date"]), 10, 64)
	return err == nil && now.Unix()-authDate <= 300
}

func firstOf(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// ---- Telegram 登录会话（对应 TelegramSessionManager 的登录部分）----

// addTelegramLoginSession 对应 add_login_session：生成 16 位令牌和 6 位数字。
func (a *App) addTelegramLoginSession(ctx context.Context) (string, string, error) {
	for i := 0; i < 10; i++ {
		token := util.RandomChar(16)
		number := strconv.Itoa(100000 + randIndex(900000))
		n, err := a.Q.CountTelegramLoginSessionConflict(ctx, store.CountTelegramLoginSessionConflictParams{Token: token, Number: number})
		if err != nil {
			return "", "", err
		}
		if n == 0 {
			err := a.Q.InsertTelegramSession(ctx, store.InsertTelegramSessionParams{UserID: 0, Type: 1, SessionContent: token + "|" + number, Datetime: time.Now().Unix()})
			return token, number, err
		}
	}
	return "", "", errors.New("无法分配 Telegram 登录令牌")
}

// checkTelegramLoginSession 对应 check_login_session：返回 -1 表示过期，0 表示尚未确认，>0 为用户 ID。
func (a *App) checkTelegramLoginSession(ctx context.Context, token, number string) (int64, error) {
	s, err := a.Q.GetTelegramLoginSession(ctx, token+"|"+number)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if s.Datetime < time.Now().Unix()-90 {
		return -1, nil
	}
	return int64(s.UserID), nil
}

// verifyTelegramLoginStep2 对应 step2_verify_login_session：取出已确认的用户 ID 并删除会话。
func (a *App) verifyTelegramLoginStep2(ctx context.Context, token, number string) (uint32, error) {
	s, err := a.Q.GetTelegramLoginSession(ctx, token+"|"+number)
	if err != nil {
		return 0, err
	}
	if s.Datetime <= time.Now().Unix()-90 {
		return 0, nil
	}
	if err := a.Q.DeleteTelegramSession(ctx, s.ID); err != nil {
		return 0, err
	}
	return s.UserID, nil
}

// newUserParams 按注册设置生成新用户（注册与后台快速创建共用，对应原版两处相同的初始化代码）。
func (a *App) newUserParams(name, email, hash, ip string, port, imType int32, imValue string, now time.Time) store.CreateUserParams {
	reg := a.Cfg.Register
	if now.IsZero() {
		now = time.Now()
	}
	return store.CreateUserParams{
		UserName: name, Email: email, Pass: hash, Passwd: util.RandomChar(6), Port: port,
		Method: reg.Method, Protocol: reg.Protocol, ProtocolParam: reg.ProtocolParam, Obfs: reg.Obfs, ObfsParam: reg.ObfsParam,
		ForbiddenIp: reg.ForbiddenIP, ForbiddenPort: reg.ForbiddenPort,
		ImType: imType, ImValue: imValue,
		TransferEnable: util.GBToBytes(reg.DefaultTraffic), InviteNum: int32(reg.InviteNum), //nolint:gosec // 配置值
		AutoResetDay: int32(reg.AutoResetDay), AutoResetBandwidth: dec.FromFloat(reg.AutoResetBandwidth), //nolint:gosec // 配置值
		ClassExpire: now.Add(time.Duration(reg.UserClassExpireHours) * time.Hour).UTC(),
		Class:       int32(reg.UserClassDefault), NodeConnector: int32(reg.UserConn), //nolint:gosec // 配置值
		NodeSpeedlimit: dec.FromFloat(reg.UserSpeedlimit),
		ExpireIn:       now.Add(time.Duration(reg.UserExpireInDefault) * 24 * time.Hour).UTC(),
		RegDate:        now.UTC(), RegIp: ip, Plan: "A", Theme: "material", Enable: 1,
		NodeGroup: randomGroup(reg.RandomGroup), GaToken: auth.NewTOTPSecret(),
	}
}
