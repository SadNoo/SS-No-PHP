package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/telegram"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// tgRSS 对应 TelegramProcess::$all_rss（按钮顺序与原版一致）。
var tgRSS = []struct{ Key, Name string }{
	{"clean_link", "重置订阅"},
	{"?mu=0", "SSR普通订阅"},
	{"?mu=1", "SSR单端口订阅"},
	{"?mu=3", "SS/SSD订阅"},
	{"?mu=2", "V2ray订阅"},
	{"?mu=4", "Clash订阅"},
}

const (
	tgNotBound = "您未绑定本站账号，您可以进入网站的“资料编辑”，在右下方绑定您的账号"
	tgHelp     = "命令列表：\n/ping  获取群组ID\n/traffic 查询流量\n/checkin 签到\n/help 获取帮助信息\n/rss 获取节点订阅"
)

var (
	reTGBind   = regexp.MustCompile(`^mod://bind/([A-Za-z0-9]{16})$`)
	reTGLogin  = regexp.MustCompile(`^mod://login/([A-Za-z0-9]{16})$`)
	reTGNumber = regexp.MustCompile(`^[0-9]{6}$`)
)

// telegramBot 返回机器人客户端；未启用时为 nil。
func (a *App) telegramBot() *telegram.Bot {
	if !a.Cfg.Telegram.Enable || a.Cfg.Telegram.Token == "" {
		return nil
	}
	if a.TGBot != nil {
		return a.TGBot
	}
	return telegram.New(a.Cfg.Telegram.Token)
}

// telegramCallback 对应 HomeController::telegram：机器人 webhook。
//
// 原版只比较 URL 中的 token 与 telegram_request_token，两者都为空时任何人都能伪造更新，
// 冒充任意 Telegram 用户签到、查询订阅、确认网页登录（K48）。这里要求令牌非空并做常数时间比较，
// 同时接受 setWebhook 时设置的 X-Telegram-Bot-Api-Secret-Token 头。
func (a *App) telegramCallback(w http.ResponseWriter, r *http.Request) {
	bot := a.telegramBot()
	want := a.Cfg.Telegram.RequestToken
	authed := tokenEq(r.URL.Query().Get("token"), want) || tokenEq(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"), want)
	if bot == nil || !authed {
		http.Error(w, "不正确请求！", http.StatusForbidden)
		return
	}
	var u telegram.Update
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&u); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// 回复消息需要调用 Telegram 接口，不依赖本次请求的生命周期。
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 45*time.Second)
	defer cancel()
	var err error
	switch {
	case u.CallbackQuery != nil:
		err = a.tgCallback(ctx, bot, u.CallbackQuery)
	case u.Message != nil && u.Message.From != nil && !u.Message.From.IsBot:
		err = a.tgMessage(ctx, bot, u.Message)
	}
	if err != nil {
		a.Log.Warn("处理 Telegram 消息失败", "err", err)
	}
	w.WriteHeader(http.StatusOK)
}

func tokenEq(got, want string) bool {
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// tgMessage 对应 TelegramProcess::telegram_process。
func (a *App) tgMessage(ctx context.Context, bot *telegram.Bot, m *telegram.Message) error {
	user, err := a.Q.GetUserByTelegramID(ctx, m.From.ID)
	var u *store.User
	switch {
	case err == nil:
		u = &user
	case !isNoRows(err):
		return err
	}
	cmd := m.Command(a.Cfg.Telegram.Bot)
	private := m.Chat.ID > 0
	if !private && a.Cfg.Telegram.GroupQuiet {
		return nil
	}
	reply, keyboard := "", [][]telegram.InlineButton(nil)
	switch cmd {
	case "ping":
		if private {
			reply = fmt.Sprintf("Pong!您的 ID 是 %d!", m.Chat.ID)
		} else {
			reply = fmt.Sprintf("Pong!这个群组的 ID 是 %d!", m.Chat.ID)
		}
	case "traffic", "checkin", "prpr":
		if reply, err = a.tgNeedBind(ctx, cmd, u); err != nil {
			return err
		}
	case "rss":
		switch {
		case !private:
			reply = "请私聊机器人使用该命令"
		case u == nil:
			reply = tgNotBound
		default:
			reply = "点击以下按钮获取对应订阅: "
			for _, s := range tgRSS {
				keyboard = append(keyboard, []telegram.InlineButton{{Text: s.Name, CallbackData: s.Key}})
			}
		}
	case "help":
		reply = tgHelp
		// 原版私聊时把提示追加到了一个未定义的变量上，未绑定的提示从未显示。
		if u == nil {
			reply += "\n" + tgNotBound
		}
	default:
		if private {
			if reply, err = a.tgPrivateText(ctx, bot, m, u); err != nil {
				return err
			}
		} else if len(m.NewChatMembers) > 0 && a.Cfg.Telegram.EnableWelcomeMessage {
			n := m.NewChatMembers[0]
			reply = "欢迎 " + n.FirstName + " " + n.LastName
		}
	}
	// 原版收到普通文字时转给图灵机器人聊天；图灵的旧接口已停用，这里不回复。
	if reply == "" {
		return nil
	}
	_ = bot.SendChatAction(ctx, m.Chat.ID, "typing")
	return bot.SendMessage(ctx, m.Chat.ID, reply, m.MessageID, keyboard)
}

// tgNeedBind 对应 TelegramProcess::needbind_method。
func (a *App) tgNeedBind(ctx context.Context, cmd string, su *store.User) (string, error) {
	if su == nil {
		return tgNotBound, nil
	}
	u := model.NewUser(*su, a.Loc)
	switch cmd {
	case "traffic":
		pct := func(v int64) string {
			if u.TransferEnable == 0 {
				return "0.00%"
			}
			return util.NumberFormat(float64(v)/float64(u.TransferEnable)*100, 2) + "%"
		}
		return "您当前的流量状况：\n" +
			"今日已使用 " + u.TodayUsedTraffic() + " " + pct(u.U+u.D-u.LastDayT) + "\n" +
			"今日之前已使用 " + u.LastUsedTraffic() + " " + pct(u.LastDayT) + "\n" +
			"未使用 " + u.UnusedTraffic() + " " + pct(u.TransferEnable-u.U-u.D), nil
	case "checkin":
		now := time.Now()
		if !u.IsAbleToCheckin(now) {
			return "您今天已经签过到了！", nil
		}
		traffic := randomInt(int64(a.Cfg.Users.CheckinMin), int64(a.Cfg.Users.CheckinMax))
		y, mo, d := now.In(a.Loc).Date()
		n, err := a.Q.CheckinUser(ctx, store.CheckinUserParams{
			TransferEnable: traffic * util.MB, LastCheckInTime: now.Unix(), ID: u.ID,
			LastCheckInTime_2: time.Date(y, mo, d, 0, 0, 0, 0, a.Loc).Unix(),
		})
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "您今天已经签过到了！", nil
		}
		return fmt.Sprintf("签到成功！获得了 %d MB 流量！", traffic), nil
	default: // prpr
		prpr := []string{"⁄(⁄ ⁄•⁄ω⁄•⁄ ⁄)⁄", "(≧ ﹏ ≦)", "(*/ω＼*)", "ヽ(*。>Д<)o゜", "(つ ﹏ ⊂)", "( >  < )"}
		return prpr[randomInt(0, int64(len(prpr)-1))], nil
	}
}

// tgPrivateText 处理私聊中的非命令消息：6 位登录数字、绑定 / 登录二维码图片。
// 除原版的二维码图片外，也接受直接发送二维码中的文字（mod://bind/...），识别失败时可以改用文字。
func (a *App) tgPrivateText(ctx context.Context, bot *telegram.Bot, m *telegram.Message, u *store.User) (string, error) {
	text := strings.TrimSpace(m.Text)
	if len(m.Photo) > 0 {
		decoded, err := a.tgDecodePhoto(ctx, bot, m.Photo)
		if err != nil {
			a.Log.Info("Telegram 图片识别失败", "err", err)
			return "未能识别二维码，请发送清晰的二维码截图，或直接发送二维码下方的文字", nil
		}
		text = decoded
	}
	if reTGNumber.MatchString(text) {
		if u == nil {
			return "登录验证失败，您未绑定本站账号", nil
		}
		n, err := a.Q.ConfirmTelegramLoginByNumber(ctx, store.ConfirmTelegramLoginByNumberParams{UserID: u.ID, Number: text, Since: time.Now().Unix() - 90})
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "登录验证失败，数字无效", nil
		}
		return "登录验证成功，邮箱：" + u.Email, nil
	}
	if s := reTGBind.FindStringSubmatch(text); s != nil {
		return a.tgBind(ctx, m.From, s[1])
	}
	if s := reTGLogin.FindStringSubmatch(text); s != nil {
		if u == nil {
			return "登录验证失败，您未绑定本站账号", nil
		}
		n, err := a.Q.ConfirmTelegramLoginByToken(ctx, store.ConfirmTelegramLoginByTokenParams{UserID: u.ID, Token: s[1], Since: time.Now().Unix() - 90})
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "登录验证失败，二维码无效", nil
		}
		return "登录验证成功，邮箱：" + u.Email, nil
	}
	return "", nil
}

// tgDecodePhoto 下载图片的最大尺寸并识别二维码；失败时依次尝试较小的尺寸。
func (a *App) tgDecodePhoto(ctx context.Context, bot *telegram.Bot, photos []telegram.PhotoSize) (string, error) {
	var lastErr error
	for i := len(photos) - 1; i >= 0; i-- { // Telegram 按尺寸从小到大排列
		data, err := bot.DownloadFile(ctx, photos[i].FileID)
		if err != nil {
			return "", err
		}
		text, err := telegram.DecodeQR(data)
		if err == nil {
			return text, nil
		}
		lastErr = err
	}
	return "", lastErr
}

// tgBind 对应 verify_bind_session 及其后的绑定。
//
// 原版不检查该 Telegram 帐号是否已绑定其他用户，同一个 Telegram 帐号可绑定多个用户，
// 之后机器人和 Telegram 登录只会随机作用于其中一个（K48 附带修复）。这里绑定前先解除旧绑定。
func (a *App) tgBind(ctx context.Context, from *telegram.User, token string) (string, error) {
	var email string
	err := a.tx(ctx, func(q *store.Queries) error {
		s, err := q.TakeTelegramBindSession(ctx, store.TakeTelegramBindSessionParams{SessionContent: token, Datetime: time.Now().Unix() - 600})
		if err != nil {
			return err
		}
		if err := q.DeleteTelegramSession(ctx, s.ID); err != nil {
			return err
		}
		if err := q.UnbindTelegramID(ctx, from.ID); err != nil {
			return err
		}
		if err := q.BindTelegram(ctx, store.BindTelegramParams{TelegramID: from.ID, ImValue: from.Username, ID: s.UserID}); err != nil {
			return err
		}
		u, err := q.GetUser(ctx, s.UserID)
		email = u.Email
		return err
	})
	if isNoRows(err) {
		return "绑定失败，二维码无效：" + token + "二维码的有效期为10分钟，请尝试刷新网站的“资料编辑”页面以更新二维码", nil
	}
	if err != nil {
		return "", err
	}
	return "绑定成功，邮箱：" + email, nil
}

// tgCallback 对应 TelegramProcess::callback_bind_method：订阅按钮。
func (a *App) tgCallback(ctx context.Context, bot *telegram.Bot, c *telegram.CallbackQuery) error {
	_ = bot.AnswerCallbackQuery(ctx, c.ID)
	if c.Message == nil {
		return nil
	}
	u, err := a.Q.GetUserByTelegramID(ctx, c.From.ID)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	reply := "？？？"
	for _, s := range tgRSS {
		if s.Key != c.Data {
			continue
		}
		if s.Key == "clean_link" {
			if err := a.Q.DeleteLinksByUser(ctx, u.ID); err != nil {
				return err
			}
			reply = "链接重置成功"
		} else {
			token, err := a.Sub.SubToken(ctx, u.ID, 0)
			if err != nil {
				return err
			}
			reply = s.Name + ": " + a.Cfg.App.SubURL + token + s.Key
		}
	}
	return bot.SendMessage(ctx, c.Message.Chat.ID, reply, c.Message.MessageID, nil)
}

// setTelegramWebhook 对应 XCat::setTelegram。
func (a *App) setTelegramWebhook(ctx context.Context) error {
	bot := a.telegramBot()
	if bot == nil {
		return fmt.Errorf("未启用 Telegram（telegram.enable / telegram.token）")
	}
	tok := a.Cfg.Telegram.RequestToken
	if tok == "" {
		return fmt.Errorf("请先设置 telegram.request_token（随机字符串）")
	}
	secret := ""
	if regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`).MatchString(tok) {
		secret = tok
	}
	return bot.SetWebhook(ctx, a.Cfg.App.BaseURL+"/telegram_callback?token="+tok, secret)
}

// sendDailyUsageByTG 对应 XCat::sendDailyUsageByTG：向所有绑定 Telegram 的用户私聊发送流量情况。
func (a *App) sendDailyUsageByTG(ctx context.Context) error {
	bot := a.telegramBot()
	if bot == nil {
		return fmt.Errorf("未启用 Telegram")
	}
	users, err := a.Q.ListUsersWithTelegram(ctx)
	if err != nil {
		return err
	}
	for _, su := range users {
		u := model.NewUser(su, a.Loc)
		pct := func(v int64) string {
			if u.TransferEnable == 0 {
				return "0.00"
			}
			return util.NumberFormat(float64(v)/float64(u.TransferEnable)*100, 2)
		}
		msg := "您当前的流量状况：\n" +
			"今天已使用 " + u.TodayUsedTraffic() + " " + pct(u.U+u.D-u.LastDayT) + "%\n" +
			"今天前已使用 " + u.LastUsedTraffic() + " " + pct(u.LastDayT) + "%\n" +
			"剩余 " + u.UnusedTraffic() + " " + pct(u.TransferEnable-u.U-u.D) + "%"
		if err := bot.SendMessage(ctx, u.TelegramID, msg, 0, nil); err != nil {
			a.Log.Warn("发送 Telegram 流量报告失败", "uid", u.ID, "err", err)
		}
	}
	return nil
}
