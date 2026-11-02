// Package config 负责读取面板配置。
//
// 配置来源（后者覆盖前者）：
//  1. 内置默认值（Default）
//  2. TOML 配置文件
//  3. 环境变量：NIUNIU_<段名>_<键名>，全部大写，例如 NIUNIU_DATABASE_PASSWORD
//
// 密码、密钥类配置建议只通过环境变量提供，不写入配置文件。
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// EnvPrefix 是环境变量覆盖配置时使用的前缀。
const EnvPrefix = "NIUNIU"

// Config 是面板的全部配置。各业务模块的配置段随模块一起加入。
type Config struct {
	App      App      `toml:"app"`
	HTTP     HTTP     `toml:"http"`
	Database Database `toml:"database"`
	Log      Log      `toml:"log"`
	Migrate  Migrate  `toml:"migrate"`
	Security Security `toml:"security"`

	Register Register `toml:"register"`
	Users    Users    `toml:"users"`
	Mail     Mail     `toml:"mail"`
	Captcha  Captcha  `toml:"captcha"`
	Telegram Telegram `toml:"telegram"`
	Contact  Contact  `toml:"contact"`
	Display  Display  `toml:"display"`
	Jobs     Jobs     `toml:"jobs"`
	Detect   Detect   `toml:"detect"`
	Radius   Radius   `toml:"radius"`
	DNS      DNS      `toml:"dns"`
	Payment  Payment  `toml:"payment"`
}

// App 对应旧版的基本设置。
type App struct {
	Name    string `toml:"name"`     // 站点名称（旧 appName）
	BaseURL string `toml:"base_url"` // 站点地址（旧 baseUrl），不带结尾斜杠
	SubURL  string `toml:"sub_url"`  // 订阅地址（旧 subUrl），为空时使用 base_url + "/link/"
	// SubURLBackup 是备用订阅地址（格式同 sub_url，以 /link/ 结尾），用户中心订阅区可以切换；
	// 域名需指向本面板。环境变量 NIUNIU_APP_SUB_URL_BACKUP 用逗号分隔多个。
	SubURLBackup []string `toml:"sub_url_backup"`
	// SubProxyDomains 是订阅完整配置中强制走「🍃 Proxy」的域名后缀（如站点自己的域名），排在 Apple / 国内规则之前。
	// 为空时不输出这条规则。环境变量 NIUNIU_APP_SUB_PROXY_DOMAINS 用逗号分隔多个。
	SubProxyDomains []string `toml:"sub_proxy_domains"`
	Key             string   `toml:"key"`       // 站点密钥（旧 key），用于各类签名，必须是随机字符串
	Debug           bool     `toml:"debug"`     // 调试模式，正式环境必须为 false
	TimeZone        string   `toml:"time_zone"` // 业务时区（旧 timeZone，PRC 对应 Asia/Shanghai）
}

// HTTP 是 Web 服务配置。
type HTTP struct {
	Listen         string   `toml:"listen"`          // 监听地址
	TrustedProxies []string `toml:"trusted_proxies"` // 可信反向代理的 IP 或网段，只有来自这些地址的 X-Forwarded-For 才会被采信
	ReadTimeout    Duration `toml:"read_timeout"`
	WriteTimeout   Duration `toml:"write_timeout"`
	IdleTimeout    Duration `toml:"idle_timeout"`
}

// Database 是 MySQL 连接配置。
type Database struct {
	Host     string `toml:"host"`
	Port     int    `toml:"port"`
	Name     string `toml:"name"`
	User     string `toml:"user"`
	Password string `toml:"password"`

	// TLSMode 取值：
	//   verify   强制 TLS，并校验服务器证书和主机名（推荐，远程数据库必须使用）
	//   disabled 不使用 TLS（仅限本机通过 127.0.0.1 或 unix socket 连接时使用）
	TLSMode       string `toml:"tls_mode"`
	TLSCA         string `toml:"tls_ca"`          // CA 证书文件路径；为空时使用系统 CA
	TLSServerName string `toml:"tls_server_name"` // 证书中的主机名；为空时使用 host
	TLSClientCert string `toml:"tls_client_cert"` // 可选：客户端证书
	TLSClientKey  string `toml:"tls_client_key"`  // 可选：客户端私钥

	MaxOpenConns    int      `toml:"max_open_conns"`
	MaxIdleConns    int      `toml:"max_idle_conns"`
	ConnMaxLifetime Duration `toml:"conn_max_lifetime"`
	ConnectTimeout  Duration `toml:"connect_timeout"`

	// ServerTimeZone 为 true 时会话使用数据库服务器的默认时区（Radius 库与 FreeRADIUS 写入时一致），不在配置文件中设置。
	ServerTimeZone bool `toml:"-"`
}

// Log 是日志配置。
type Log struct {
	Level  string `toml:"level"`  // debug / info / warn / error
	Format string `toml:"format"` // text / json
}

// Migrate 是表结构变更配置。
type Migrate struct {
	// AutoUp 为 true 时，服务启动前自动执行未执行的表结构变更。
	AutoUp bool `toml:"auto_up"`
	// BackupCommand 不为空时，在执行任何待执行的变更之前先运行该命令（例如调用 mysqldump 备份），
	// 命令失败则不执行变更、服务不启动。
	BackupCommand string `toml:"backup_command"`
}

// Security 对应旧版高级设置里与鉴权相关的部分。
type Security struct {
	// LegacyPwdMethod 是旧系统的密码哈希方式（旧 pwdMethod）：md5 / sha256 / bcrypt / argon2i / argon2id。
	// 新系统只用它来校验尚未升级的旧密码，登录成功后会自动升级为 argon2id。
	LegacyPwdMethod string `toml:"legacy_pwd_method"`
	// LegacySalt 是旧系统的密码盐（旧 salt）。
	LegacySalt string `toml:"legacy_salt"`
	// MuKey 和 MuKeyList 是节点后端的通信密钥（旧 muKey / muKeyList）。
	MuKey     string   `toml:"mu_key"`
	MuKeyList []string `toml:"mu_key_list"`
	// SessionLifetime 是“记住我”时的登录有效期（旧 rememberMeDuration，单位天）。
	SessionLifetime Duration `toml:"session_lifetime"`
	// SessionIdleTimeout 是未勾选“记住我”时的会话空闲超时。
	SessionIdleTimeout Duration `toml:"session_idle_timeout"`
	// CookieSecure 为 true 时会话 Cookie 只通过 HTTPS 发送。正式环境必须为 true。
	CookieSecure bool `toml:"cookie_secure"`
	// NodeIPGrace 是动态 IP（DDNS）节点换 IP 后、DNS 尚未更新期间的宽限时间，0 表示不宽限。
	// 只对已停止从登记 IP 发来请求的节点生效，且请求必须带正确的密钥和 node_id。
	NodeIPGrace Duration `toml:"node_ip_grace"`
}

// Default 返回内置默认配置。
func Default() Config {
	c := Config{
		App: App{
			Name:     "SS-No-PHP",
			BaseURL:  "http://127.0.0.1:8080",
			TimeZone: "Asia/Shanghai",
		},
		HTTP: HTTP{
			Listen:         "127.0.0.1:8080",
			TrustedProxies: []string{"127.0.0.1/32", "::1/128"},
			ReadTimeout:    Duration(15 * time.Second),
			WriteTimeout:   Duration(60 * time.Second),
			IdleTimeout:    Duration(120 * time.Second),
		},
		Database: Database{
			Host:            "127.0.0.1",
			Port:            3306,
			Name:            "niuniupanel",
			User:            "panel",
			TLSMode:         "verify",
			MaxOpenConns:    20,
			MaxIdleConns:    5,
			ConnMaxLifetime: Duration(30 * time.Minute),
			ConnectTimeout:  Duration(10 * time.Second),
		},
		Log: Log{Level: "info", Format: "text"},
		Migrate: Migrate{
			AutoUp: true,
		},
		Security: Security{
			LegacyPwdMethod:    "md5",
			SessionLifetime:    Duration(7 * 24 * time.Hour),
			SessionIdleTimeout: Duration(24 * time.Hour),
			NodeIPGrace:        Duration(10 * time.Minute),
			CookieSecure:       true,
		},
	}
	defaultBusiness(&c)
	return c
}

// Load 读取配置文件（path 为空时只使用默认值和环境变量），再应用环境变量覆盖，最后校验。
// ResolvePath 处理可选的配置文件：环境变量 NIUNIU_CONFIG_OPTIONAL 为 true 且文件不存在时返回空路径
// （只使用默认值与环境变量，skipped 为 true）。未设置该变量时原样返回，文件不存在由 Load 报错。
// Docker 部署挂载 deploy/prod/config 目录，其中的 config.toml 可以不存在。
func ResolvePath(path string, lookup func(string) (string, bool)) (resolved string, skipped bool, err error) {
	v, _ := lookup(EnvPrefix + "_CONFIG_OPTIONAL")
	if path == "" || strings.TrimSpace(v) == "" {
		return path, false, nil
	}
	optional, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return "", false, fmt.Errorf("%s_CONFIG_OPTIONAL 只能是 true 或 false: %q", EnvPrefix, v)
	}
	if !optional {
		return path, false, nil
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return "", true, nil
	}
	return path, false, nil
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		md, err := toml.DecodeFile(path, &cfg)
		if err != nil {
			return cfg, fmt.Errorf("读取配置文件 %s: %w", path, err)
		}
		if undecoded := md.Undecoded(); len(undecoded) > 0 {
			keys := make([]string, 0, len(undecoded))
			for _, k := range undecoded {
				keys = append(keys, k.String())
			}
			return cfg, fmt.Errorf("配置文件 %s 中有无法识别的配置项: %s", path, strings.Join(keys, ", "))
		}
	}
	if err := applyEnv(reflect.ValueOf(&cfg).Elem(), EnvPrefix, os.LookupEnv); err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// ValidGroupLinkURL 判断群组入口链接是否可用：只接受 https / http / tg（须有主机名）与 mailto，
// 且不含任何空白（含全角空格、不换行空格）、控制字符与不可见的格式字符（如零宽空格、文字方向控制符）。
// 页面按此白名单直接输出链接（html/template 默认会过滤 tg:// 等协议）。
func ValidGroupLinkURL(raw string) bool {
	if raw == "" || hasUnsafeRune(raw) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http", "tg":
		return u.Hostname() != ""
	case "mailto":
		return u.Opaque != ""
	}
	return false
}

// hasUnsafeRune 判断字符串中是否有空白、控制字符或不可见的格式字符（这些字符会让链接看起来与实际不同）。
func hasUnsafeRune(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError
	}) >= 0
}

var domainSuffixRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// validDomainSuffix 判断订阅规则中的域名后缀：至少两段，每段为字母、数字与连字符（可为 xn-- 形式的国际化域名）。
func validDomainSuffix(d string) bool {
	return len(d) <= 253 && domainSuffixRe.MatchString(d)
}

// validSubURL 判断备用订阅地址：https / http、带主机名、路径以 /link/ 结尾（订阅码直接接在后面），没有查询参数与片段。
func validSubURL(raw string) bool {
	if hasUnsafeRune(raw) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "https" || scheme == "http") && u.Hostname() != "" && strings.HasSuffix(u.Path, "/link/") &&
		u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(raw, "?#")
}

// Validate 检查配置是否完整、安全。
func (c *Config) Validate() error {
	var errs []error
	if strings.TrimSpace(c.App.Key) == "" || len(c.App.Key) < 16 {
		errs = append(errs, errors.New("app.key 必须设置为至少 16 位的随机字符串"))
	}
	c.App.BaseURL = strings.TrimRight(c.App.BaseURL, "/")
	if c.App.SubURL == "" {
		c.App.SubURL = c.App.BaseURL + "/link/"
	}
	// 备用订阅地址：去掉空项、与主订阅地址相同的项和重复项。
	backup := make([]string, 0, len(c.App.SubURLBackup))
	for _, s := range c.App.SubURLBackup {
		s = strings.TrimSpace(s)
		if s == "" || s == c.App.SubURL || slices.Contains(backup, s) {
			continue
		}
		if !validSubURL(s) {
			errs = append(errs, fmt.Errorf("app.sub_url_backup 中的 %q 无效：须为 https:// 或 http:// 开头、带主机名、以 /link/ 结尾的地址", s))
			continue
		}
		backup = append(backup, s)
	}
	c.App.SubURLBackup = backup
	// 走代理的域名后缀：转小写、去掉开头的点与重复项；只接受普通域名，避免把逗号、换行等写进客户端配置。
	domains := make([]string, 0, len(c.App.SubProxyDomains))
	for _, d := range c.App.SubProxyDomains {
		d = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), ".")
		if d == "" || slices.Contains(domains, d) {
			continue
		}
		if !validDomainSuffix(d) {
			errs = append(errs, fmt.Errorf("app.sub_proxy_domains 中的 %q 无效：须为域名，例如 example.com", d))
			continue
		}
		domains = append(domains, d)
	}
	c.App.SubProxyDomains = domains
	if _, err := time.LoadLocation(c.App.TimeZone); err != nil {
		errs = append(errs, fmt.Errorf("app.time_zone 无效: %w", err))
	}
	switch c.Database.TLSMode {
	case "verify", "disabled":
	default:
		errs = append(errs, fmt.Errorf("database.tls_mode 只能是 verify 或 disabled，当前为 %q", c.Database.TLSMode))
	}
	if c.Database.Name == "" || c.Database.User == "" {
		errs = append(errs, errors.New("database.name 和 database.user 不能为空"))
	}
	switch c.Security.LegacyPwdMethod {
	case "md5", "sha256", "bcrypt", "argon2i", "argon2id":
	default:
		errs = append(errs, fmt.Errorf("security.legacy_pwd_method 无效: %q", c.Security.LegacyPwdMethod))
	}
	switch c.Register.Mode {
	case "close", "open", "invite":
	default:
		errs = append(errs, fmt.Errorf("register.mode 只能是 close / open / invite，当前为 %q", c.Register.Mode))
	}
	switch c.Mail.Driver {
	case "none", "smtp", "mailgun", "sendgrid":
	default:
		errs = append(errs, fmt.Errorf("mail.driver 只能是 none / smtp / mailgun / sendgrid，当前为 %q", c.Mail.Driver))
	}
	if c.Register.MinPort <= 0 || c.Register.MaxPort > 65535 || c.Register.MinPort > c.Register.MaxPort {
		errs = append(errs, errors.New("register.min_port / max_port 范围无效"))
	}
	for i, g := range c.Contact.GroupLinks {
		if g.Title == "" || g.Text == "" || !ValidGroupLinkURL(g.URL) {
			errs = append(errs, fmt.Errorf("contact.group_links 第 %d 项无效：title、text 不能为空，url 只能是 https / http / tg / mailto 链接", i+1))
		}
	}
	if _, err := regexp.Compile(c.Display.FlagRegex); err != nil {
		errs = append(errs, fmt.Errorf("display.flag_regex 无效: %w", err))
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("log.format 只能是 text 或 json，当前为 %q", c.Log.Format))
	}
	return errors.Join(errs...)
}

// Location 返回业务时区。Validate 已保证时区有效。
func (c *Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.App.TimeZone)
	if err != nil {
		return time.Local
	}
	return loc
}

// Duration 让 TOML 中可以写 "30s"、"15m"、"168h" 这样的时长。
type Duration time.Duration

// UnmarshalText 实现 encoding.TextUnmarshaler。
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalText 实现 encoding.TextMarshaler。
func (d Duration) MarshalText() ([]byte, error) { return []byte(time.Duration(d).String()), nil }

// D 返回 time.Duration。
func (d Duration) D() time.Duration { return time.Duration(d) }

// applyEnv 按 toml 标签递归地用环境变量覆盖配置。
func applyEnv(v reflect.Value, prefix string, lookup func(string) (string, bool)) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("toml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		name := prefix + "_" + strings.ToUpper(tag)
		fv := v.Field(i)
		if f.Type.Kind() == reflect.Struct {
			if err := applyEnv(fv, name, lookup); err != nil {
				return err
			}
			continue
		}
		raw, ok := lookup(name)
		if !ok {
			continue
		}
		if err := setFromString(fv, raw); err != nil {
			return fmt.Errorf("环境变量 %s: %w", name, err)
		}
	}
	return nil
}

func setFromString(fv reflect.Value, raw string) error {
	if fv.CanAddr() {
		if u, ok := fv.Addr().Interface().(interface{ UnmarshalText([]byte) error }); ok {
			return u.UnmarshalText([]byte(raw))
		}
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int64, reflect.Int32:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		fv.SetInt(n)
	case reflect.Float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return err
		}
		fv.SetFloat(n)
	case reflect.Slice:
		if fv.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("不支持的列表类型 %s", fv.Type())
		}
		var items []string
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" {
				items = append(items, s)
			}
		}
		fv.Set(reflect.ValueOf(items))
	default:
		return fmt.Errorf("不支持的类型 %s", fv.Type())
	}
	return nil
}
