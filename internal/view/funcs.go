package view

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// PHPFuncs 返回按 PHP 语义实现的模板函数。原版 Smarty 模板中的比较、真假判断、算术都依赖 PHP 的宽松类型，
// 移植后的模板统一通过这些函数计算，保证判断结果与原版一致。
func PHPFuncs(loc *time.Location) template.FuncMap {
	return template.FuncMap{
		"truthy": Truthy,
		"peq":    func(a, b any) bool { return LooseEqual(a, b) },
		"pne":    func(a, b any) bool { return !LooseEqual(a, b) },
		"plt":    func(a, b any) bool { return compare(a, b) < 0 },
		"pgt":    func(a, b any) bool { return compare(a, b) > 0 },
		"ple":    func(a, b any) bool { return compare(a, b) <= 0 },
		"pge":    func(a, b any) bool { return compare(a, b) >= 0 },
		"add":    func(a, b any) float64 { return ToFloat(a) + ToFloat(b) },
		"sub":    func(a, b any) float64 { return ToFloat(a) - ToFloat(b) },
		"mul":    func(a, b any) float64 { return ToFloat(a) * ToFloat(b) },
		"div":    div,
		"mod": func(a, b any) int64 {
			bb := int64(ToFloat(b))
			if bb == 0 {
				return 0
			}
			return int64(ToFloat(a)) % bb
		},
		"neg":    func(a any) float64 { return -ToFloat(a) },
		"concat": func(a, b any) string { return ToString(a) + ToString(b) },
		"ternary": func(c bool, a, b any) any {
			if c {
				return a
			}
			return b
		},
		"phpDate":      func(layout string, ts ...any) string { return phpDate(layout, ts, loc) },
		"numberFormat": func(v any, dec int) string { return util.NumberFormat(ToFloat(v), dec) },
		"round": func(v any, dec ...int) float64 {
			d := 0
			if len(dec) > 0 {
				d = dec[0]
			}
			return util.PHPRound(ToFloat(v), d)
		},
		"count":     count,
		"inArray":   inArray,
		"isset":     func(v any) bool { return !isNil(v) },
		"empty":     func(v any) bool { return !Truthy(v) },
		"explode":   func(sep string, s any) []string { return strings.Split(ToString(s), sep) },
		"implode":   func(sep string, list []string) string { return strings.Join(list, sep) },
		"strlen":    func(s any) int { return len(ToString(s)) },
		"intval":    func(v any) int64 { return int64(ToFloat(v)) },
		"now":       func() int64 { return time.Now().Unix() },
		"urlencode": func(s any) string { return url.QueryEscape(ToString(s)) },
		"base64":    func(s any) string { return base64.StdEncoding.EncodeToString([]byte(ToString(s))) },
		"nl2br": func(s any) template.HTML {
			return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(ToString(s)), "\n", "<br />\n")) //nolint:gosec // 已先转义
		}, //nolint:gosec // 已先转义
		"flow":     func(v any) string { return util.FlowAutoShow(ToFloat(v)) },
		"phpFloat": func(v any) string { return util.PHPFloat(ToFloat(v)) },
		"str":      ToString,
		// safeHTML 原样输出管理员编写的 HTML（公告等，原版同样不转义）。不得用于用户提交的内容。
		"safeHTML": func(v any) template.HTML { return template.HTML(ToString(v)) }, //nolint:gosec // 仅用于管理员内容

		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i
			}
			return s
		},
		"dict": dict,
		"list": func(v ...any) []any { return v },
	}
}

func dict(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[ToString(kv[i])] = kv[i+1]
	}
	return m
}

// floater 由 dec.D 等自定义数值类型实现，避免把以“分”为单位的内部值当成真实数值。
type floater interface{ Float() float64 }

func indirect(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	return rv.Interface()
}

func isNil(v any) bool { return indirect(v) == nil }

// Truthy 按 PHP 规则判断真假：null、false、0、0.0、""、"0"、空数组为假。
func Truthy(v any) bool {
	v = indirect(v)
	if v == nil {
		return false
	}
	if f, ok := v.(floater); ok {
		return f.Float() != 0
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != "" && x != "0"
	case template.HTML:
		return x != "" && x != "0"
	case time.Time:
		return !x.IsZero()
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() != 0
	case reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() > 0
	}
	return true
}

// numeric 尝试把值转为数字；第二个返回值表示是否是数字或数字字符串。
func numeric(v any) (float64, bool) {
	v = indirect(v)
	if v == nil {
		return 0, false
	}
	if f, ok := v.(floater); ok {
		return f.Float(), true
	}
	switch x := v.(type) {
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	}
	return 0, false
}

// ToFloat 转为浮点数，非数字为 0。
func ToFloat(v any) float64 {
	f, _ := numeric(v)
	return f
}

// ToString 按 PHP 方式转为字符串。
func ToString(v any) string {
	v = indirect(v)
	if v == nil {
		return ""
	}
	if f, ok := v.(floater); ok {
		return util.PHPFloat(f.Float())
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "1"
		}
		return ""
	case float64:
		return util.PHPFloat(x)
	case float32:
		return util.PHPFloat(float64(x))
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}

// LooseEqual 近似 PHP 的 == 比较。
func LooseEqual(a, b any) bool {
	a, b = indirect(a), indirect(b)
	if a == nil || b == nil {
		if a == nil && b == nil {
			return true
		}
		other := a
		if a == nil {
			other = b
		}
		// PHP：null 与字符串比较时转为 ""；与其他类型比较时按布尔值比较。
		if s, ok := other.(string); ok {
			return s == ""
		}
		return !Truthy(other)
	}
	if ab, ok := a.(bool); ok {
		return ab == Truthy(b)
	}
	if bb, ok := b.(bool); ok {
		return bb == Truthy(a)
	}
	fa, na := numeric(a)
	fb, nb := numeric(b)
	if na && nb {
		return fa == fb
	}
	return ToString(a) == ToString(b)
}

func compare(a, b any) int {
	fa, na := numeric(a)
	fb, nb := numeric(b)
	if na && nb || (isNil(a) || isNil(b)) {
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}
		return 0
	}
	return strings.Compare(ToString(a), ToString(b))
}

func div(a, b any) float64 {
	fb := ToFloat(b)
	if fb == 0 {
		return math.NaN()
	}
	return ToFloat(a) / fb
}

func count(v any) int {
	v = indirect(v)
	if v == nil {
		return 0
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array, reflect.String:
		return rv.Len()
	}
	return 1
}

func inArray(needle, haystack any) bool {
	rv := reflect.ValueOf(indirect(haystack))
	if !rv.IsValid() {
		return false
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			if LooseEqual(needle, rv.Index(i).Interface()) {
				return true
			}
		}
	case reflect.Map:
		for _, k := range rv.MapKeys() {
			if LooseEqual(needle, rv.MapIndex(k).Interface()) {
				return true
			}
		}
	}
	return false
}

// phpDate 实现 PHP date() 中常用的格式字符。
func phpDate(layout string, ts []any, loc *time.Location) string {
	t := time.Now().In(loc)
	if len(ts) > 0 {
		t = time.Unix(int64(ToFloat(ts[0])), 0).In(loc)
	}
	var sb strings.Builder
	for _, c := range layout {
		switch c {
		case 'Y':
			sb.WriteString(t.Format("2006"))
		case 'y':
			sb.WriteString(t.Format("06"))
		case 'm':
			sb.WriteString(t.Format("01"))
		case 'n':
			sb.WriteString(strconv.Itoa(int(t.Month())))
		case 'd':
			sb.WriteString(t.Format("02"))
		case 'j':
			sb.WriteString(strconv.Itoa(t.Day()))
		case 'H':
			sb.WriteString(t.Format("15"))
		case 'i':
			sb.WriteString(t.Format("04"))
		case 's':
			sb.WriteString(t.Format("05"))
		default:
			sb.WriteRune(c)
		}
	}
	return sb.String()
}
