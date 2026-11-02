package sub

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed templates/*.conf.tmpl
var textProfileFiles embed.FS

var textProfiles, textProfilesErr = template.New("client-profiles").Option("missingkey=error").Funcs(template.FuncMap{"join": strings.Join, "ipRule": cidrRule}).ParseFS(textProfileFiles, "templates/*.conf.tmpl")

type profileRuleSet struct{ Name, URL, Policy string }
type textProfileData struct {
	Proxies, Names, SiriRules []string
	ProxyDomains              []string
	RuleSets                  []profileRuleSet
	Bypass, LocalNames        []string
}

// profileRuleSets uses each client's native format. Surge's All variants avoid
// depending on newer nested-rule-set syntax in a Surge 4 subscription.
func profileRuleSets(client string) []profileRuleSet {
	platform := "Surge"
	if client == "loon" {
		platform = "Loon"
	}
	sets := []profileRuleSet{}
	for _, category := range []struct{ name, policy string }{{"Apple", "🍎 Only"}, {"Netflix", "🏖 Netflix"}, {"Global", "🍃 Proxy"}, {"ChinaMax", "🍂 Domestic"}} {
		base := "https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/" + platform + "/" + category.name + "/" + category.name
		if client == "loon" && category.name != "Netflix" {
			sets = append(sets, profileRuleSet{Name: category.name + "-Domain", URL: base + "_Domain.list", Policy: category.policy})
		}
		filename := base
		if client == "surge" && category.name != "Netflix" {
			filename += "_All_No_Resolve"
		}
		sets = append(sets, profileRuleSet{Name: category.name, URL: filename + ".list", Policy: category.policy})
	}
	return sets
}

// clientBypass 是各客户端完整配置中不经过代理的本机与局域网网段（Surge / Loon 的 skip-proxy 与直连规则、Clash 的直连规则）。
var clientBypass = []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10"}

// clientLocalNames 是本地域名：直连，并且使用真实 IP（Surge always-real-ip、Loon real-ip、Clash fake-ip-filter）。
var clientLocalNames = []string{"localhost", "*.localhost", "local", "*.local", "lan", "*.lan", "home.arpa", "*.home.arpa"}

// clientDNS 是各客户端完整配置中直连解析使用的 DNS：一个固定的普通 DNS（用于解析加密 DNS 的域名）与阿里 DoH。
const (
	clientDNSPlain = "223.5.5.5"
	clientDNSDoH   = "https://dns.alidns.com/dns-query"
)

func cidrRule(address string) string {
	if strings.Contains(address, ":") {
		return "IP-CIDR6"
	}
	return "IP-CIDR"
}

func renderTextProfile(client, version string, lines, names, proxyDomains []string) (string, error) {
	if textProfilesErr != nil {
		return "", fmt.Errorf("parse client templates: %w", textProfilesErr)
	}
	filename := "loon.conf.tmpl"
	if client == "surge" {
		filename = "surge" + version + ".conf.tmpl"
	} else if client != "loon" {
		return "", fmt.Errorf("unsupported full profile %q", client)
	}
	// Proxies from Service are already normalized. Reject unsafe direct callers
	// instead of allowing a name to become another section, rule, or group member.
	used := map[string]bool{"DIRECT": true, "REJECT": true, "🍃 Proxy": true, "🫥SiriAI": true, "🍎 Only": true, "🏖 Netflix": true, "🍂 Domestic": true, "☁️ Others": true}
	for _, name := range names {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n\t,=;#[]") || used[name] {
			return "", fmt.Errorf("invalid or duplicate proxy name")
		}
		used[name] = true
	}
	data := textProfileData{Proxies: lines, Names: names, SiriRules: siriClientRules, ProxyDomains: proxyDomains, RuleSets: profileRuleSets(client),
		Bypass: clientBypass, LocalNames: clientLocalNames,
	}
	var b bytes.Buffer
	if err := textProfiles.ExecuteTemplate(&b, filename, data); err != nil {
		return "", fmt.Errorf("render client profile: %w", err)
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}
