package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(path, []byte(`
[app]
name = "测试站"
base_url = "https://a.example/"
key = "0123456789abcdef"
[database]
port = 3307
conn_max_lifetime = "5m"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NIUNIU_DATABASE_PASSWORD", "secret")
	t.Setenv("NIUNIU_HTTP_TRUSTED_PROXIES", "10.0.0.1, 10.0.0.2")
	t.Setenv("NIUNIU_SECURITY_SESSION_LIFETIME", "48h")

	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.App.Name != "测试站" || c.Database.Port != 3307 || c.Database.Password != "secret" {
		t.Fatalf("配置未正确加载: %+v", c)
	}
	if c.App.BaseURL != "https://a.example" || c.App.SubURL != "https://a.example/link/" {
		t.Fatalf("站点地址处理错误: %q %q", c.App.BaseURL, c.App.SubURL)
	}
	if c.Database.ConnMaxLifetime.D() != 5*time.Minute || c.Security.SessionLifetime.D() != 48*time.Hour {
		t.Fatal("时长解析错误")
	}
	if len(c.HTTP.TrustedProxies) != 2 || c.HTTP.TrustedProxies[1] != "10.0.0.2" {
		t.Fatalf("列表环境变量解析错误: %v", c.HTTP.TrustedProxies)
	}
}

func TestLoadRejectsUnknownKeyAndWeakKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	_ = os.WriteFile(path, []byte("[app]\nkey = \"0123456789abcdef\"\nunknown = 1\n"), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("未知配置项应当报错")
	}
	_ = os.WriteFile(path, []byte("[app]\nkey = \"short\"\n"), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("过短的 key 应当报错")
	}
	_ = os.WriteFile(path, []byte("[app]\nkey = \"0123456789abcdef\"\n[database]\ntls_mode = \"skip-verify\"\n"), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("不允许跳过证书校验")
	}
}

func TestGroupLinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	head := "[app]\nkey = \"0123456789abcdef\"\n[contact]\nadmin_contact1 = \"邮箱\"\n"
	_ = os.WriteFile(path, []byte(head+`
[[contact.group_links]]
title = "Telegram(VIP可见)"
text = "点击加入TG群"
url = "https://t.me/example_group"
min_class = 1
[[contact.group_links]]
title = "管理员TG"
text = "点击发起TG对话"
url = "tg://resolve?domain=example"
`), 0o600)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Contact.GroupLinks) != 2 || c.Contact.GroupLinks[0].MinClass != 1 || c.Contact.GroupLinks[1].MinClass != 0 || c.Contact.AdminContact1 != "邮箱" {
		t.Fatalf("群组入口解析错误: %+v", c.Contact)
	}
	for _, bad := range []string{
		"[[contact.group_links]]\ntitle = \"a\"\ntext = \"b\"\nurl = \"javascript:alert(1)\"\n",
		"[[contact.group_links]]\ntitle = \"a\"\ntext = \"\"\nurl = \"https://t.me/x\"\n",
		"[[contact.group_links]]\ntitle = \"a\"\ntext = \"b\"\nurl = \"https://t.me/x\"\nenable_mylivechat = true\n",
	} {
		_ = os.WriteFile(path, []byte(head+bad), 0o600)
		if _, err := Load(path); err == nil {
			t.Fatalf("应拒绝: %s", bad)
		}
	}
}

func TestValidGroupLinkURL(t *testing.T) {
	for _, ok := range []string{"https://t.me/example", "HTTP://example.com/a?b=1", "https://example.test:8443/x", "https://例子.测试/群组", "tg://resolve?domain=example", "tg://join?invite=AbC", "mailto:admin@example.com"} {
		if !ValidGroupLinkURL(ok) {
			t.Errorf("应接受 %s", ok)
		}
	}
	for _, bad := range []string{
		"", "javascript:alert(1)", "JavaScript:alert(1)", "data:text/html,x", "vbscript:x", "ftp://example.com", "whatsapp://send?phone=1",
		"/user", "//evil.example", "https://", "tg:", "mailto:",
		// 只有端口、没有主机名
		"http://:80", "https://:443", "tg://:1",
		// 空白：半角、全角（U+3000）、不换行（U+00A0）、换行
		"https://t.me/a b", "https://example.test/a\u3000b", "https://example.test/a\u00a0b", "tg://resolve?domain=a\n",
		// 控制字符（C0、DEL、C1）与不可见格式字符（零宽空格、文字方向控制、BOM）
		"https://example.test/a\x01b", "https://example.test/a\x7fb", "https://example.test/a\u0085b",
		"https://example.test/a\u200bb", "https://example.test/\u202egpj.exe", "\ufeffhttps://example.test/",
	} {
		if ValidGroupLinkURL(bad) {
			t.Errorf("应拒绝 %q", bad)
		}
	}
	// 非法链接在读取配置时就明确报错（Load 不连接数据库）。
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	for _, bad := range []string{"ftp://example.com", "https://:443", "http://:80", "https://example.test/a\u3000b", "https://example.test/a\u00a0b"} {
		conf := "[app]\nkey = \"0123456789abcdef\"\n[[contact.group_links]]\ntitle = \"测试群组\"\ntext = \"点击加入\"\nurl = \"" + bad + "\"\nmin_class = 0\n"
		_ = os.WriteFile(path, []byte(conf), 0o600)
		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), "contact.group_links 第 1 项无效") {
			t.Errorf("%q 应在配置校验时报错: %v", bad, err)
		}
	}
}

func TestResolvePath(t *testing.T) {
	dir := t.TempDir()
	exists := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(exists, []byte("# 空\n"), 0o600)
	missing := filepath.Join(dir, "none.toml")
	env := func(v string) func(string) (string, bool) {
		return func(k string) (string, bool) {
			if k == "NIUNIU_CONFIG_OPTIONAL" && v != "" {
				return v, true
			}
			return "", false
		}
	}
	for _, c := range []struct {
		path, optional, want string
		skipped, err         bool
	}{
		{missing, "", missing, false, false}, // 未设置：原样返回，由 Load 报错
		{missing, "false", missing, false, false},
		{missing, "true", "", true, false},
		{exists, "true", exists, false, false},
		{"", "true", "", false, false},
		{missing, "yes", "", false, true},
	} {
		got, skipped, err := ResolvePath(c.path, env(c.optional))
		if got != c.want || skipped != c.skipped || (err != nil) != c.err {
			t.Errorf("ResolvePath(%q, %q) = %q %v %v", c.path, c.optional, got, skipped, err)
		}
	}
	if _, err := Load(missing); err == nil {
		t.Error("未设置可选时，配置文件不存在应报错")
	}
}

// TestDockerConfigTemplate 确认 Docker 部署的配置模板原样可以加载，去掉示例的注释后也可以加载。
func TestDockerConfigTemplate(t *testing.T) {
	t.Setenv("NIUNIU_APP_KEY", "0123456789abcdef0123")
	const tmpl = "../../deploy/prod/config.toml.example"
	c, err := Load(tmpl)
	if err != nil || len(c.Contact.GroupLinks) != 0 {
		t.Fatalf("模板应可加载且没有群组: %v %v", err, c.Contact.GroupLinks)
	}
	src, err := os.ReadFile(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(src), "\n") {
		for _, p := range []string{"# [[contact.group_links]]", "# title =", "# text =", "# url =", "# min_class ="} {
			if strings.HasPrefix(line, p) {
				line = strings.TrimPrefix(line, "# ")
			}
		}
		out = append(out, line)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	_ = os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o600)
	c, err = Load(path)
	if err != nil || len(c.Contact.GroupLinks) != 2 || c.Contact.GroupLinks[1].Text != "点击发起TG对话" {
		t.Fatalf("去掉注释后应有两组群组: %v %+v", err, c.Contact.GroupLinks)
	}
}

func TestSubURLBackup(t *testing.T) {
	t.Setenv("NIUNIU_APP_KEY", "0123456789abcdef0123")
	t.Setenv("NIUNIU_APP_BASE_URL", "https://panel.example.com")
	// 环境变量用逗号分隔；空项、与主订阅地址相同的项、重复项被去掉。
	t.Setenv("NIUNIU_APP_SUB_URL_BACKUP", "https://sub2.example.net/link/, ,https://panel.example.com/link/,https://sub2.example.net/link/,http://sub3.example.org:8443/link/")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.App.SubURLBackup, " "); got != "https://sub2.example.net/link/ http://sub3.example.org:8443/link/" {
		t.Fatalf("备用订阅地址不正确: %q", got)
	}
	for _, bad := range []string{"sub2.example.net/link/", "ftp://sub2.example.net/link/", "https:///link/", "https://:443/link/",
		"https://sub2.example.net/", "https://sub2.example.net/link", "https://sub2.example.net/link/?a=1", "https://sub2.example.net/link/#x",
		"https://sub2.example.net/li\u3000nk/", "https://sub2.example.net/link/\u200b", "javascript:alert(1)//link/"} {
		t.Setenv("NIUNIU_APP_SUB_URL_BACKUP", bad)
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "app.sub_url_backup") {
			t.Errorf("%q 应被拒绝: %v", bad, err)
		}
	}
}

func TestSubProxyDomains(t *testing.T) {
	t.Setenv("NIUNIU_APP_KEY", "0123456789abcdef0123")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.App.SubProxyDomains) != 0 {
		t.Fatalf("默认应为空: %q", c.App.SubProxyDomains)
	}
	// 环境变量用逗号分隔；转小写、去掉开头的点、空项与重复项。
	t.Setenv("NIUNIU_APP_SUB_PROXY_DOMAINS", " Site.Example.COM, ,.site.example.com,xn--fiqs8s.example,a-b.example.net")
	if c, err = Load(""); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.App.SubProxyDomains, " "); got != "site.example.com xn--fiqs8s.example a-b.example.net" {
		t.Fatalf("域名处理不正确: %q", got)
	}
	for _, bad := range []string{"example", "example.com,DIRECT", "a b.example", "-a.example", "a-.example", "a..example", "example.com.",
		"*.example.com", "例子.example", "example.com/x", "example.com:443", "example.com\u200b", "1.2.3.4/32"} {
		c := Default()
		c.App.Key = "0123456789abcdef0123"
		c.App.SubProxyDomains = []string{bad}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "app.sub_proxy_domains") {
			t.Errorf("%q 应被拒绝: %v", bad, err)
		}
	}
	// 配置文件写 [] 时不输出规则（不回落到默认值）。
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte("[app]\nsub_proxy_domains = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NIUNIU_APP_SUB_PROXY_DOMAINS", "")
	if err := os.Unsetenv("NIUNIU_APP_SUB_PROXY_DOMAINS"); err != nil {
		t.Fatal(err)
	}
	if c, err = Load(path); err != nil || len(c.App.SubProxyDomains) != 0 {
		t.Fatalf("写 [] 后应为空: %v %q", err, c.App.SubProxyDomains)
	}
}
