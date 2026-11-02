package sub

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Filter only narrows the already authorized node set. Nil display switches
// preserve existing output, including names and subscription metadata defaults.
type Filter struct {
	Type               string
	Classes, NoClasses []int32
	Name               *regexp.Regexp
	Emoji, Extend      *bool
}

func parseFilter(q url.Values) (Filter, error) {
	f := Filter{Type: q.Get("type")}
	if !contains([]string{"", "all", "ss", "ss2022", "vmess", "vless", "anytls"}, f.Type) {
		return f, errors.New("type 无效；SSR 已停用")
	}
	if f.Type == "ss" {
		f.Type = "ss2022"
	}
	for key, dst := range map[string]*[]int32{"class": &f.Classes, "noclass": &f.NoClasses} {
		if value, ok := q[key]; ok {
			parts := strings.FieldsFunc(value[0], func(r rune) bool { return r == '+' || r == ',' || unicode.IsSpace(r) })
			if len(parts) == 0 || len(parts) > 128 {
				return f, errors.New("节点等级列表无效")
			}
			for _, part := range parts {
				n, err := strconv.ParseInt(part, 10, 32)
				if err != nil || n < 0 {
					return f, errors.New("节点等级必须为非负整数")
				}
				*dst = append(*dst, int32(n))
			}
		}
	}
	for key, dst := range map[string]**bool{"emoji": &f.Emoji, "extend": &f.Extend} {
		if v, ok := q[key]; ok {
			if v[0] != "0" && v[0] != "1" {
				return f, errors.New("emoji/extend 应为 0 或 1")
			}
			b := v[0] == "1"
			*dst = &b
		}
	}
	if value := q.Get("regex"); value != "" {
		if len(value) > 512 {
			return f, errors.New("regex 最长 512 字节")
		}
		// Accept the common legacy /pattern/i and ~pattern~i forms as well as
		// bare RE2. Unsupported PCRE features fail instead of silently unfiltering.
		if value[0] == '/' || value[0] == '~' {
			end := strings.LastIndexByte(value[1:], value[0]) + 1
			if end == 0 {
				return f, errors.New("regex 分隔符无效")
			}
			flags := value[end+1:]
			if strings.Trim(flags, "imsu") != "" {
				return f, errors.New("regex 标志无效")
			}
			pattern := strings.ReplaceAll(value[1:end], "\\"+value[:1], value[:1])
			flags = strings.ReplaceAll(flags, "u", "")
			if flags != "" {
				pattern = "(?" + flags + ")" + pattern
			}
			value = pattern
		}
		var err error
		f.Name, err = regexp.Compile(value)
		if err != nil {
			return f, errors.New("regex 无效或使用了不支持的 PCRE 特性")
		}
	}
	return f, nil
}

func (f Filter) active() bool {
	return f.Type != "" && f.Type != "all" || f.Classes != nil || f.NoClasses != nil || f.Name != nil || f.Emoji != nil || f.Extend != nil
}

func (f Filter) match(p Proxy) bool {
	if f.Type != "" && f.Type != "all" && f.Type != p.Kind && (f.Type != "ss2022" || p.Method != SS2022Method) {
		return false
	}
	if f.Classes != nil {
		if !classContains(f.Classes, p.Class) {
			return false
		}
	} else if classContains(f.NoClasses, p.Class) {
		return false
	}
	return f.Name == nil || f.Name.MatchString(p.Name)
}

func classContains(classes []int32, class int32) bool {
	for _, n := range classes {
		if n == class {
			return true
		}
	}
	return false
}

func (f Filter) proxies(nodes []Proxy) []Proxy {
	var out []Proxy
	for _, p := range nodes {
		if !f.match(p) {
			continue
		}
		p.Name = f.name(p.Name)
		out = append(out, p)
	}
	return out
}

func (f Filter) name(name string) string {
	if f.Emoji == nil || !*f.Emoji {
		return name
	}
	runes := []rune(strings.TrimSpace(name))
	if len(runes) >= 2 && runes[0] >= 0x1f1e6 && runes[0] <= 0x1f1ff && runes[1] >= 0x1f1e6 && runes[1] <= 0x1f1ff {
		return name
	}
	for _, region := range nodeRegions {
		if region.match.MatchString(name) {
			return region.flag + " " + name
		}
	}
	return name
}

var nodeRegions = func() []struct {
	flag  string
	match *regexp.Regexp
} {
	var out []struct {
		flag  string
		match *regexp.Regexp
	}
	for _, item := range [][2]string{
		{"🇭🇰", `香港|(?i)hong\s*kong|\bHK\b`}, {"🇹🇼", `台湾|台灣|(?i)\btaiwan\b|\bTW\b`},
		{"🇲🇴", `澳门|澳門|(?i)\bmacau\b|\bMO\b`}, {"🇯🇵", `日本|东京|東京|大阪|(?i)\bjapan\b|\btokyo\b|\bJP\b`},
		{"🇸🇬", `新加坡|狮城|獅城|(?i)\bsingapore\b|\bSG\b`}, {"🇺🇸", `美国|美國|洛杉矶|(?i)\bunited states\b|\bUSA?\b`},
		{"🇬🇧", `英国|英國|伦敦|(?i)\bunited kingdom\b|\blondon\b|\bUK\b|\bGB\b`},
		{"🇩🇪", `德国|德國|法兰克福|(?i)\bgermany\b|\bDE\b`}, {"🇫🇷", `法国|法國|巴黎|(?i)\bfrance\b|\bFR\b`},
		{"🇨🇦", `加拿大|(?i)\bcanada\b|\bCA\b`}, {"🇦🇺", `澳大利亚|澳洲|(?i)\baustralia\b|\bAU\b`},
		{"🇰🇷", `韩国|韓國|首尔|(?i)\bkorea\b|\bKR\b`}, {"🇮🇳", `印度|(?i)\bindia\b|\bIN\b`},
		{"🇷🇺", `俄罗斯|俄羅斯|(?i)\brussia\b|\bRU\b`}, {"🇳🇱", `荷兰|荷蘭|(?i)\bnetherlands\b|\bNL\b`},
		{"🇨🇳", `中国|中國|回国|回國|(?i)\bchina\b|\bCN\b`},
	} {
		out = append(out, struct {
			flag  string
			match *regexp.Regexp
		}{item[0], regexp.MustCompile(item[1])})
	}
	return out
}()
