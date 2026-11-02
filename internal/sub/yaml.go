package sub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// yamlKeyOrder 是 YAML 输出中键的先后顺序（Clash 配置、节点、策略组、DNS 共用）：列出的键按这里的顺序在前，其余按字母顺序。
var yamlKeyOrder = []string{
	"name", "type", "server", "port",
	"mixed-port", "allow-lan", "mode", "log-level", "enable", "ipv6", "geodata-mode", "dns",
	"enhanced-mode", "fake-ip-range", "fake-ip-filter", "default-nameserver", "nameserver",
	"proxies", "proxy-groups", "rules",
}

var yamlPlainKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// yamlEncode 把 map / 列表 / 标量写成块状 YAML（缩进两格）。字符串一律写成双引号标量（JSON 转义，与 YAML 双引号写法兼容），
// 不会被解析成数字、布尔值或其他类型；只支持 Clash 配置用到的类型，遇到其他类型返回错误。
func yamlEncode(v any) (string, error) {
	var b strings.Builder
	if err := yamlValue(&b, v, 0); err != nil {
		return "", err
	}
	return b.String(), nil
}

func yamlScalar(v any) (string, bool, error) {
	switch x := v.(type) {
	case nil:
		return "null", true, nil
	case string:
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(x); err != nil {
			return "", true, err
		}
		return strings.TrimSuffix(buf.String(), "\n"), true, nil
	case bool:
		return strconv.FormatBool(x), true, nil
	case int:
		return strconv.Itoa(x), true, nil
	case int32:
		return strconv.FormatInt(int64(x), 10), true, nil
	case int64:
		return strconv.FormatInt(x, 10), true, nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true, nil
	}
	return "", false, nil
}

// yamlValue 写出 v：标量直接写在当前行（调用方已写好前缀），map 与列表从下一行开始，缩进 indent 个空格。
func yamlValue(b *strings.Builder, v any, indent int) error {
	switch x := v.(type) {
	case map[string]any:
		return yamlMap(b, x, indent)
	case []any:
		return yamlList(b, x, indent)
	case []string:
		items := make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
		return yamlList(b, items, indent)
	case []map[string]any:
		items := make([]any, len(x))
		for i, m := range x {
			items[i] = m
		}
		return yamlList(b, items, indent)
	}
	s, ok, err := yamlScalar(v)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("YAML 不支持的类型 %T", v)
	}
	b.WriteString(s + "\n")
	return nil
}

func yamlKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	rank := func(k string) int {
		if i := slices.Index(yamlKeyOrder, k); i >= 0 {
			return i
		}
		return len(yamlKeyOrder)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		return strings.Compare(a, b)
	})
	return keys
}

func yamlMap(b *strings.Builder, m map[string]any, indent int) error {
	if len(m) == 0 {
		b.WriteString(strings.Repeat(" ", indent) + "{}\n")
		return nil
	}
	pad := strings.Repeat(" ", indent)
	for _, k := range yamlKeys(m) {
		key := k
		if !yamlPlainKey.MatchString(k) {
			key, _, _ = yamlScalar(k)
		}
		b.WriteString(pad + key + ":")
		if err := yamlChild(b, m[k], indent+2); err != nil {
			return err
		}
	}
	return nil
}

// yamlChild 写出键或列表项后面的值：标量与空集合写在同一行，非空 map / 列表换行缩进。
func yamlChild(b *strings.Builder, v any, indent int) error {
	if isEmptyCollection(v) {
		if _, isMap := v.(map[string]any); isMap {
			b.WriteString(" {}\n")
		} else {
			b.WriteString(" []\n")
		}
		return nil
	}
	if s, ok, err := yamlScalar(v); ok || err != nil {
		if err == nil {
			b.WriteString(" " + s + "\n")
		}
		return err
	}
	b.WriteString("\n")
	return yamlValue(b, v, indent)
}

func isEmptyCollection(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	case []string:
		return len(x) == 0
	case []map[string]any:
		return len(x) == 0
	}
	return false
}

func yamlList(b *strings.Builder, items []any, indent int) error {
	if len(items) == 0 {
		b.WriteString(strings.Repeat(" ", indent) + "[]\n")
		return nil
	}
	pad := strings.Repeat(" ", indent)
	for _, item := range items {
		if s, ok, err := yamlScalar(item); err != nil {
			return err
		} else if ok {
			b.WriteString(pad + "- " + s + "\n")
			continue
		}
		if isEmptyCollection(item) {
			b.WriteString(pad + "-")
			if err := yamlChild(b, item, indent+2); err != nil {
				return err
			}
			continue
		}
		// 非空 map / 列表：内容缩进 indent+2，第一行的缩进换成 "- "。
		var inner strings.Builder
		if err := yamlValue(&inner, item, indent+2); err != nil {
			return err
		}
		b.WriteString(pad + "- " + strings.TrimPrefix(inner.String(), strings.Repeat(" ", indent+2)))
	}
	return nil
}
