package config

// 本文件定义与原版 .config.php 对应的业务配置。键名尽量沿用原版（改为小写下划线），
// 以便从旧配置迁移；每个字段注释中标出原版键名。

// Register 对应原版“用户注册设置”。
type Register struct {
	Mode                 string  `toml:"mode"`                      // register_mode：close / open / invite
	DefaultTraffic       float64 `toml:"default_traffic"`           // defaultTraffic：初始流量（GB）
	UserExpireInDefault  int     `toml:"user_expire_in_default"`    // user_expire_in_default：账户有效期（天），默认 100 年；帐号到期会被删除
	UserClassDefault     int     `toml:"user_class_default"`        // user_class_default
	UserClassExpireHours int     `toml:"user_class_expire_default"` // user_class_expire_default：等级有效期（小时）
	UserConn             int     `toml:"user_conn"`                 // user_conn：新注册用户的同时在线 IP 数，默认 5，0 不限
	UserSpeedlimit       float64 `toml:"user_speedlimit"`           // user_speedlimit：限速（Mbps），0 不限
	AutoResetDay         int     `toml:"reg_auto_reset_day"`        // reg_auto_reset_day
	AutoResetBandwidth   float64 `toml:"reg_auto_reset_bandwidth"`  // reg_auto_reset_bandwidth（GB）
	RandomGroup          string  `toml:"random_group"`              // ramdom_group：逗号分隔的分组
	Method               string  `toml:"reg_method"`
	Protocol             string  `toml:"reg_protocol"`
	ProtocolParam        string  `toml:"reg_protocol_param"`
	Obfs                 string  `toml:"reg_obfs"`
	ObfsParam            string  `toml:"reg_obfs_param"`
	ForbiddenIP          string  `toml:"reg_forbidden_ip"`
	ForbiddenPort        string  `toml:"reg_forbidden_port"`
	MinPort              int     `toml:"min_port"`
	MaxPort              int     `toml:"max_port"`
	MuSuffix             string  `toml:"mu_suffix"`
	MuRegex              string  `toml:"mu_regex"`
	InviteNum            int     `toml:"invite_num"`          // inviteNum
	InviteGetMoney       float64 `toml:"invite_get_money"`    // invite_get_money（元）
	InvitePrice          float64 `toml:"invite_price"`        // invite_price，小于 0 不开放
	CustomInvitePrice    float64 `toml:"custom_invite_price"` // custom_invite_price，小于 0 不开放
	EnableEmailVerify    bool    `toml:"enable_email_verify"`
	EmailVerifyTTL       int     `toml:"email_verify_ttl"`     // 秒
	EmailVerifyIPLimit   int     `toml:"email_verify_iplimit"` // 有效期内单 IP 请求次数
}

// Users 对应原版“已注册用户设置”。
type Users struct {
	CheckinMin              float64 `toml:"checkin_min"` // checkinMin（MB）
	CheckinMax              float64 `toml:"checkin_max"` // checkinMax（MB）
	AutoCleanUncheckDays    int     `toml:"auto_clean_uncheck_days"`
	AutoCleanUnusedDays     int     `toml:"auto_clean_unused_days"`
	AutoCleanMinMoney       float64 `toml:"auto_clean_min_money"`
	CodePayback             float64 `toml:"code_payback"` // 充值返利百分比
	InviteGift              float64 `toml:"invite_gift"`  // 邀请奖励流量（GB）
	EnableBoughtReset       bool    `toml:"enable_bought_reset"`
	EnableBoughtExtend      bool    `toml:"enable_bought_extend"`
	EnableAutoRenew         bool    `toml:"enable_auto_renew"` // 自动续费（默认关闭）：关闭时购买不记录续费时间、定时任务不续费扣款
	PortPrice               float64 `toml:"port_price"`
	PortPriceSpecify        float64 `toml:"port_price_specify"`
	ClassExpireResetTraffic float64 `toml:"class_expire_reset_traffic"` // GB，小于 0 不重置
	AccountExpireDeleteDays int     `toml:"account_expire_delete_days"` // 小于 0 不删除
	EnableKill              bool    `toml:"enable_kill"`
	NotifyLimitMode         string  `toml:"notify_limit_mode"` // false / per / mb
	NotifyLimitValue        float64 `toml:"notify_limit_value"`
	MergeSub                bool    `toml:"merge_sub"` // mergeSub
}

// Mail 对应原版“邮件设置”。
type Mail struct {
	Driver         string `toml:"driver"` // mailDriver：none / smtp / mailgun / sendgrid
	SendPageLimit  int    `toml:"send_page_limit"`
	MailgunKey     string `toml:"mailgun_key"`
	MailgunDomain  string `toml:"mailgun_domain"`
	MailgunSender  string `toml:"mailgun_sender"`
	SMTPHost       string `toml:"smtp_host"`
	SMTPUsername   string `toml:"smtp_username"`
	SMTPPort       int    `toml:"smtp_port"`
	SMTPName       string `toml:"smtp_name"`
	SMTPSender     string `toml:"smtp_sender"`
	SMTPPassword   string `toml:"smtp_password"` // 原版键名拼写为 smtp_passsword
	SMTPSSL        bool   `toml:"smtp_ssl"`      // true：SMTPS（465）；false：STARTTLS
	SendgridKey    string `toml:"sendgrid_key"`
	SendgridSender string `toml:"sendgrid_sender"`
}

// Captcha 对应原版“验证码设置”。
type Captcha struct {
	Provider         string `toml:"provider"` // recaptcha / geetest
	RecaptchaSitekey string `toml:"recaptcha_sitekey"`
	RecaptchaSecret  string `toml:"recaptcha_secret"`
	GeetestID        string `toml:"geetest_id"`
	GeetestKey       string `toml:"geetest_key"`
	EnableReg        bool   `toml:"enable_reg_captcha"`
	EnableLogin      bool   `toml:"enable_login_captcha"`
	EnableCheckin    bool   `toml:"enable_checkin_captcha"`
}

// Telegram 对应原版 Bot 设置中的 Telegram 部分。
type Telegram struct {
	Enable               bool   `toml:"enable"`
	Token                string `toml:"token"`
	ChatID               int64  `toml:"chat_id"`
	Bot                  string `toml:"bot"` // 机器人用户名，不带 @
	GroupQuiet           bool   `toml:"group_quiet"`
	RequestToken         string `toml:"request_token"`
	EnableWelcomeMessage bool   `toml:"enable_welcome_message"`
	FinancePublic        bool   `toml:"finance_public"`
}

// Contact 对应原版“沟通设置”。
type Contact struct {
	EnableMylivechat   bool   `toml:"enable_mylivechat"`
	MylivechatID       string `toml:"mylivechat_id"`
	EnableTicket       bool   `toml:"enable_ticket"`
	MailTicket         bool   `toml:"mail_ticket"`
	UseScFtqq          bool   `toml:"use_scftqq"`
	ScFtqqSCKEY        string `toml:"scftqq_sckey"`
	EnableAdminContact bool   `toml:"enable_admin_contact"`
	AdminContact1      string `toml:"admin_contact1"`
	AdminContact2      string `toml:"admin_contact2"`
	AdminContact3      string `toml:"admin_contact3"`
	EnableDiscord      bool   `toml:"enable_discord"`
	// GroupLinks 是用户中心首页「群组」中的入口，按配置顺序显示，为空时不显示该区块。
	GroupLinks []GroupLink `toml:"group_links"`
}

// GroupLink 是一个群组入口：用户等级低于 MinClass 时只显示标题，不显示链接。
type GroupLink struct {
	Title    string `toml:"title"`     // 标题，例如 "Telegram(VIP可见)"
	Text     string `toml:"text"`      // 链接文字，例如 "点击加入TG群"
	URL      string `toml:"url"`       // 只接受 https、http、tg、mailto
	MinClass int    `toml:"min_class"` // 0 为所有用户可见
}

// Display 对应原版“其他面板显示设置”及部分高级设置。
type Display struct {
	SalesPeriod       string  `toml:"sales_period"` // expire 或天数
	EnableFlag        bool    `toml:"enable_flag"`
	FlagRegex         string  `toml:"flag_regex"` // 从节点名称中取国家/地区的 Go 正则，匹配结果去掉首尾空白后使用（原版 /.*?(?=\s)/ 对应 ^\S+\s）
	EnableDonate      bool    `toml:"enable_donate"`
	DisplayIOSClass   int     `toml:"display_ios_class"`
	DisplayIOSTopup   float64 `toml:"display_ios_topup"`
	IOSAccount        string  `toml:"ios_account"`
	IOSPassword       string  `toml:"ios_password"`
	JumpDelay         int     `toml:"jump_delay"` // 毫秒
	EnableLoginBindIP bool    `toml:"enable_login_bind_ip"`
	LoginWarn         bool    `toml:"login_warn"`
	SpeedtestDuration int     `toml:"speedtest_duration"`
	QQWryPath         string  `toml:"qqwry_path"` // 纯真 IP 库路径（原版 storage/qqwry.dat），不存在时不显示归属地
}

// Jobs 是定时任务设置。原版需要在 crontab 中逐条添加 xcat 命令，新版由程序内置调度。
type Jobs struct {
	Enable        bool   `toml:"enable"`          // 是否在本实例运行定时任务（多实例部署时也只会有一个实例在执行，见 GET_LOCK）
	DailyMailTime string `toml:"daily_mail_time"` // 每日流量报告邮件的发送时间（原版 crontab 30 22 * * *），空字符串表示不发送
	FinanceMail   bool   `toml:"finance_mail"`    // 财务日报 / 周报 / 月报（原版 sendFinanceMail_day / week / month）
	DetectGFW     bool   `toml:"detect_gfw"`      // 节点被墙检测（原版 detectGFW），使用 detect.gfw_* 配置
	SyncNode      bool   `toml:"sync_node"`       // 每分钟解析节点域名并更新节点 IP（原版 syncnode）
}

// Detect 对应原版“节点检测”。
type Detect struct {
	GFWInterval            int    `toml:"gfw_interval"`
	GFWPort                int    `toml:"gfw_port"`
	GFWURL                 string `toml:"gfw_url"`
	GFWCount               int    `toml:"gfw_count"`
	EnableDetectOffline    bool   `toml:"enable_detect_offline"`
	DetectOfflineUseScFtqq bool   `toml:"detect_offline_use_scftqq"`
}

// Radius 对应原版 Radius 设置。
//
// 原版的 radius_db_host 等对应 [radius.db] 段，连接参数（含 TLS）与 [database] 相同。
type Radius struct {
	Enable bool     `toml:"enable"` // enable_radius
	Secret string   `toml:"secret"` // radius_secret：写入 NAS 表的共享密钥
	DB     Database `toml:"db"`
}

// DNS 对应原版 Cloudflare 设置（CloudXNS 已停止服务，不再支持）。
type DNS struct {
	CloudflareEnable bool   `toml:"cloudflare_enable"`
	CloudflareEmail  string `toml:"cloudflare_email"`
	CloudflareKey    string `toml:"cloudflare_key"`
	CloudflareName   string `toml:"cloudflare_name"`
}

func defaultBusiness(c *Config) {
	c.Register = Register{
		Mode: "open", DefaultTraffic: 1, UserExpireInDefault: 36500, UserClassExpireHours: 24, UserConn: 5,
		RandomGroup: "0", Method: "rc4-md5", Protocol: "origin", Obfs: "plain",
		ForbiddenIP: "127.0.0.0/8,0.0.0.0/8,10.0.0.0/8,100.64.0.0/10,169.254.0.0/16,172.16.0.0/12,192.168.0.0/16,::1/128,fc00::/7,fe80::/10", MinPort: 10000, MaxPort: 65535,
		MuSuffix: "microsoft.com", MuRegex: "%5m%id.%suffix",
		InviteNum: 10, InviteGetMoney: 0, InvitePrice: -1, CustomInvitePrice: -1,
		EmailVerifyTTL: 3600, EmailVerifyIPLimit: 10,
	}
	c.Users = Users{
		CheckinMin: 1, CheckinMax: 50, AutoCleanUncheckDays: -1, AutoCleanUnusedDays: -1,
		AutoCleanMinMoney: 1, CodePayback: 0, InviteGift: 0, EnableBoughtReset: true, EnableBoughtExtend: true,
		PortPrice: -1, PortPriceSpecify: -1, AccountExpireDeleteDays: -1, EnableKill: true,
		NotifyLimitMode: "false", NotifyLimitValue: 20,
	}
	c.Mail = Mail{Driver: "none", SendPageLimit: 50, SMTPPort: 465, SMTPSSL: true}
	c.Captcha = Captcha{Provider: "recaptcha"}
	c.Telegram = Telegram{Bot: "_bot", EnableWelcomeMessage: true}
	c.Contact = Contact{EnableTicket: true, MailTicket: true, MylivechatID: "null"}
	c.Display = Display{
		SalesPeriod: "30", FlagRegex: `^\S+\s`, DisplayIOSClass: -1, JumpDelay: 1200,
		SpeedtestDuration: 6, QQWryPath: "data/qqwry.dat",
	}
	c.Detect = Detect{GFWInterval: 3600, GFWPort: 22, GFWURL: "https://cn-qz-tcping.torch.njs.app/{ip}/{port}", GFWCount: 3, EnableDetectOffline: true, DetectOfflineUseScFtqq: true}
	c.Jobs = Jobs{Enable: true, DailyMailTime: "22:30", SyncNode: true}
	c.Radius = Radius{DB: c.Database}
	c.Radius.DB.Name, c.Radius.DB.User, c.Radius.DB.Password = "radius", "radius", ""
	c.Radius.DB.MaxOpenConns, c.Radius.DB.MaxIdleConns = 5, 2
	c.Payment = Payment{System: "none", Amount: []float64{2, 23, 233, 2333, 23333}, F2FGateway: "https://openapi.alipay.com/gateway.do"}
}

// Payment 对应原版“支付系统设置”。各支付渠道的参数随支付模块加入。
type Payment struct {
	// System 对应 payment_system：none / codepay / trimepay / f2fpay / chenAlipay / paymentwall / spay / payjs / bitpay / doiampay
	System string `toml:"system"`
	// Amount 对应 amount：充值金额选项。
	Amount []float64 `toml:"amount"`
	// Notice 是显示在“充值”和“套餐购买”页面顶部的购买说明（纯文本，可多行），
	// 例如不开放在线支付时写“添加微信 xxx 好友购买套餐”。为空时不显示。
	Notice string `toml:"notice"`

	// 支付宝当面付（payment_system = f2fpay）。
	F2FAppID              string `toml:"f2fpay_app_id"`        // f2fpay_app_id
	F2FMerchantPrivateKey string `toml:"merchant_private_key"` // 应用私钥（PEM 或去掉头尾的 Base64），建议用环境变量
	F2FAlipayPublicKey    string `toml:"alipay_public_key"`    // 支付宝公钥（PEM 或去掉头尾的 Base64）
	F2FNotifyURL          string `toml:"f2f_notify_url"`       // f2fNotifyUrl，为空时使用 base_url + /payment/notify
	F2FGateway            string `toml:"f2f_gateway"`          // 支付宝网关，默认正式环境
}
