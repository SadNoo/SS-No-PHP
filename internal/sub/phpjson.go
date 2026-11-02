package sub

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// 本文件实现与 PHP json_encode 输出一致的 JSON 编码，用于订阅内容的逐字节兼容。

// JSONFlags 对应 PHP json_encode 的选项。
type JSONFlags int

const (
	UnescapedSlashes JSONFlags = 64
	UnescapedUnicode JSONFlags = 256
)

// OMap 是保持插入顺序的对象（对应 PHP 关联数组）。
type OMap struct {
	keys []string
	vals map[string]any
}

// NewOMap 创建有序对象。
func NewOMap() *OMap { return &OMap{vals: map[string]any{}} }

// Set 设置键值。
func (m *OMap) Set(k string, v any) *OMap {
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
	return m
}

// Get 取值。
func (m *OMap) Get(k string) any { return m.vals[k] }

// PHPFloat 表示 PHP 中的 float 值（编码规则与 PHP 一致：1.0 输出为 1，1.5 输出为 1.5）。
type PHPFloat float64

// EncodeJSON 按 PHP json_encode 的规则编码。
func EncodeJSON(v any, flags JSONFlags) string {
	var b strings.Builder
	encode(&b, v, flags)
	return b.String()
}

func encode(b *strings.Builder, v any, f JSONFlags) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		encodeString(b, x, f)
	case int:
		b.WriteString(strconv.Itoa(x))
	case int32:
		b.WriteString(strconv.FormatInt(int64(x), 10))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case uint32:
		b.WriteString(strconv.FormatUint(uint64(x), 10))
	case uint64:
		b.WriteString(strconv.FormatUint(x, 10))
	case int8:
		b.WriteString(strconv.FormatInt(int64(x), 10))
	case PHPFloat:
		b.WriteString(phpFloatString(float64(x)))
	case float64:
		encode(b, PHPFloat(x), f)
	case *OMap:
		b.WriteByte('{')
		for i, k := range x.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeString(b, k, f)
			b.WriteByte(':')
			encode(b, x.vals[k], f)
		}
		b.WriteByte('}')
	case *V2:
		m := NewOMap()
		for _, k := range x.Keys {
			m.Set(k, x.Values[k])
		}
		encode(b, m, f)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			encode(b, e, f)
		}
		b.WriteByte(']')
	case []string:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeString(b, e, f)
		}
		b.WriteByte(']')
	default:
		encodeString(b, fmt.Sprint(x), f)
	}
}

func encodeString(b *strings.Builder, s string, f JSONFlags) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '/':
			if f&UnescapedSlashes != 0 {
				b.WriteByte('/')
			} else {
				b.WriteString(`\/`)
			}
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(b, `\u%04x`, r)
		case r < 0x80:
			b.WriteRune(r)
		case f&UnescapedUnicode != 0:
			b.WriteString(s[i : i+size])
		case r > 0xffff:
			r -= 0x10000
			fmt.Fprintf(b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		default:
			fmt.Fprintf(b, `\u%04x`, r)
		}
		i += size
	}
	b.WriteByte('"')
}

// phpFloatString 按 PHP（serialize_precision = -1）的规则输出浮点数：
// 取最短表示；指数小于 -4 或不小于 17 时使用科学计数法（如 1.0e-8、1.5e+20），
// 不带 JSON_PRESERVE_ZERO_FRACTION 时整数值输出为整数形式。
func phpFloatString(v float64) string {
	if v == 0 {
		return "0"
	}
	if v == math.Trunc(v) && math.Abs(v) < 1e17 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	e := strconv.FormatFloat(v, 'e', -1, 64) // 例如 1.1175870895385742e-08
	mant, expStr, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expStr)
	if exp < -4 || exp >= 17 {
		if !strings.Contains(mant, ".") {
			mant += ".0"
		}
		sign := "+"
		if exp < 0 {
			sign, exp = "-", -exp
		}
		return mant + "e" + sign + strconv.Itoa(exp)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
