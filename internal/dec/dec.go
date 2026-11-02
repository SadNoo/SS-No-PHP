// Package dec 是两位小数的定点数，对应数据库中的 DECIMAL(x,2) 字段（金额、限速、流量 GB 数等）。
// 内部以 1/100 为单位的整数存储和计算，避免浮点误差。
package dec

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// D 是两位小数的定点数，内部值为实际值 × 100。
type D int64

// FromInt 由整数构造，例如 FromInt(12) = 12.00。
func FromInt(v int64) D { return D(v * 100) }

// FromFloat 由浮点数构造，四舍五入到两位小数。
func FromFloat(f float64) D {
	if f >= 0 {
		return D(int64(f*100 + 0.5))
	}
	return D(int64(f*100 - 0.5))
}

// Parse 解析 "12.34"、"-0.5"、"3" 这类字符串，最多两位小数。
func Parse(s string) (D, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("数值为空")
	}
	neg := false
	if s[0] == '-' || s[0] == '+' {
		neg = s[0] == '-'
		s = s[1:]
	}
	intPart, frac, hasFrac := strings.Cut(s, ".")
	if intPart == "" {
		intPart = "0"
	}
	if hasFrac {
		if len(frac) == 0 || len(frac) > 2 {
			return 0, fmt.Errorf("数值格式错误: %q", s)
		}
		for len(frac) < 2 {
			frac += "0"
		}
	} else {
		frac = "00"
	}
	i, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("数值格式错误: %q", s)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("数值格式错误: %q", s)
	}
	v := i*100 + f
	if neg {
		v = -v
	}
	return D(v), nil
}

// String 返回保留两位小数的字符串，例如 "12.30"。
func (a D) String() string {
	v := int64(a)
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}

// Float 返回浮点值，仅用于显示或与外部接口交互。
func (a D) Float() float64 { return float64(a) / 100 }

// MulPercent 返回 a × p%，四舍五入到两位小数（例如返利计算）。
func (a D) MulPercent(p float64) D {
	v := float64(a) * p / 100
	if v >= 0 {
		return D(int64(v + 0.5))
	}
	return D(int64(v - 0.5))
}

// Scan 实现 sql.Scanner。
func (a *D) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*a = 0
		return nil
	case []byte:
		p, err := Parse(string(v))
		*a = p
		return err
	case string:
		p, err := Parse(v)
		*a = p
		return err
	case int64:
		*a = D(v * 100)
		return nil
	case float64:
		p, err := Parse(strconv.FormatFloat(v, 'f', 2, 64))
		*a = p
		return err
	default:
		return fmt.Errorf("无法把 %T 转换为两位小数", src)
	}
}

// Value 实现 driver.Valuer。
func (a D) Value() (driver.Value, error) { return a.String(), nil }

// NullD 是可为 NULL 的 D。
type NullD struct {
	Dec   D
	Valid bool
}

// Scan 实现 sql.Scanner。
func (n *NullD) Scan(src any) error {
	if src == nil {
		n.Dec, n.Valid = 0, false
		return nil
	}
	n.Valid = true
	return n.Dec.Scan(src)
}

// Value 实现 driver.Valuer。
func (n NullD) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Dec.Value()
}
