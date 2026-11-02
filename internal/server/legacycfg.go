package server

import (
	"strconv"

	"github.com/SadNoo/SS-No-PHP/internal/config"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// LegacyConfig 按原版 Config::getPublicConfig 的键名返回配置值（字符串形式，布尔值为 "true"/"false"），
// 供移植后的模板中 (cfg "键名") 使用，使模板中的判断与原版一致。
// 只暴露原版公开给模板的配置，不包含任何密钥。
func LegacyConfig(c *config.Config, key string) string {
	b := strconv.FormatBool
	f := func(v float64) string { return util.PHPFloat(v) }
	i := strconv.Itoa
	switch key {
	case "appName":
		return c.App.Name
	case "baseUrl":
		return c.App.BaseURL
	case "subUrl":
		return c.App.SubURL
	case "min_port":
		return i(c.Register.MinPort)
	case "max_port":
		return i(c.Register.MaxPort)
	case "checkinMin":
		return f(c.Users.CheckinMin)
	case "checkinMax":
		return f(c.Users.CheckinMax)
	case "invite_price":
		return f(c.Register.InvitePrice)
	case "custom_invite_price":
		return f(c.Register.CustomInvitePrice)
	case "invite_get_money":
		return f(c.Register.InviteGetMoney)
	case "code_payback":
		return f(c.Users.CodePayback)
	case "invite_gift":
		return f(c.Users.InviteGift)
	case "port_price":
		return f(c.Users.PortPrice)
	case "port_price_specify":
		return f(c.Users.PortPriceSpecify)
	case "enable_auto_renew":
		return b(c.Users.EnableAutoRenew)
	case "jump_delay":
		return i(c.Display.JumpDelay)
	case "enable_analytics_code", "sspanelAnalysis":
		// K11：原版默认向原作者的统计服务发送访问数据，已移除。
		return "false"
	case "enable_donate":
		return b(c.Display.EnableDonate)
	case "enable_telegram":
		return b(c.Telegram.Enable)
	case "telegram_bot":
		return c.Telegram.Bot
	case "enable_discord":
		return b(c.Contact.EnableDiscord)
	case "payment_system":
		return c.Payment.System
	case "enable_mylivechat":
		return b(c.Contact.EnableMylivechat)
	case "mylivechat_id":
		return c.Contact.MylivechatID
	case "enable_ticket":
		return b(c.Contact.EnableTicket)
	case "enable_admin_contact":
		return b(c.Contact.EnableAdminContact)
	case "admin_contact1":
		return c.Contact.AdminContact1
	case "admin_contact2":
		return c.Contact.AdminContact2
	case "admin_contact3":
		return c.Contact.AdminContact3
	case "register_mode":
		return c.Register.Mode
	case "enable_flag":
		return b(c.Display.EnableFlag)
	case "enable_kill":
		return b(c.Users.EnableKill)
	case "captcha_provider":
		return c.Captcha.Provider
	case "enable_email_verify":
		return b(c.Register.EnableEmailVerify)
	case "mergeSub":
		return b(c.Users.MergeSub)
	}
	return ""
}
