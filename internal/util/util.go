// Package util 对应原版 App\Utils\Tools 中与业务无关的通用函数，输出格式与原版保持一致。
package util

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	KB = 1024
	MB = 1048576
	GB = 1073741824
	TB = GB * 1024
	PB = TB * 1024
)

// PHPRound 模拟 PHP round($v, $precision)：四舍五入，0.5 远离零。
func PHPRound(v float64, precision int) float64 {
	p := math.Pow(10, float64(precision))
	r := v * p
	// 消除 1.005*100 = 100.49999 这类二进制误差，与 PHP 的 pre-rounding 行为一致。
	r = math.Round(r*1e6) / 1e6
	if r >= 0 {
		return math.Floor(r+0.5) / p
	}
	return math.Ceil(r-0.5) / p
}

// PHPFloat 按 PHP 把浮点数转成字符串的方式格式化：整数不带小数点，其余去掉末尾的 0。
func PHPFloat(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// FlowAutoShow 对应 Tools::flowAutoShow：根据大小自动选择单位，保留两位小数。
func FlowAutoShow(value float64) string {
	a := math.Abs(value)
	switch {
	case a > PB:
		return PHPFloat(PHPRound(value/PB, 2)) + "PB"
	case a > TB:
		return PHPFloat(PHPRound(value/TB, 2)) + "TB"
	case a > GB:
		return PHPFloat(PHPRound(value/GB, 2)) + "GB"
	case a > MB:
		return PHPFloat(PHPRound(value/MB, 2)) + "MB"
	case a > KB:
		return PHPFloat(PHPRound(value/KB, 2)) + "KB"
	default:
		return PHPFloat(PHPRound(value, 2)) + "B"
	}
}

// MBToBytes 对应 Tools::toMB（名称虽是 toMB，实际是 MB 转字节）。
func MBToBytes(mb float64) int64 { return int64(mb * MB) }

// GBToBytes 对应 Tools::toGB（GB 转字节）。
func GBToBytes(gb float64) int64 { return int64(gb * GB) }

// BytesToGB 对应 Tools::flowToGB。
func BytesToGB(b float64) float64 { return b / GB }

// BytesToMB 对应 Tools::flowToMB。
func BytesToMB(b float64) float64 { return b / MB }

const (
	digits   = "0123456789"
	alphaNum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

func randomFrom(charset string, n int) string {
	var sb strings.Builder
	sb.Grow(n)
	max := big.NewInt(int64(len(charset)))
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand 失败意味着系统熵源不可用，继续运行会生成可预测的令牌。
			panic(fmt.Sprintf("crypto/rand 不可用: %v", err))
		}
		sb.WriteByte(charset[idx.Int64()])
	}
	return sb.String()
}

// RandomNum 对应 Tools::genRandomNum，使用 crypto/rand。
func RandomNum(n int) string { return randomFrom(digits, n) }

// RandomChar 对应 Tools::genRandomChar，使用 crypto/rand。
func RandomChar(n int) string { return randomFrom(alphaNum, n) }

// Token 对应 Tools::genToken：64 位随机字符串。
func Token() string { return RandomChar(64) }

var ipv4Re = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)

// IsIPv4 对应 Tools::is_ip。
func IsIPv4(s string) bool { return ipv4Re.MatchString(s) }

// ToDateTime 对应 Tools::toDateTime：Unix 秒转 "2006-01-02 15:04:05"（业务时区）。
func ToDateTime(unix int64, loc *time.Location) string {
	return time.Unix(unix, 0).In(loc).Format("2006-01-02 15:04:05")
}

// SecondsToTime 对应 Tools::secondsToTime，例如 "1 天, 2 小时, 3 分 + 4 秒"。
func SecondsToTime(sec int64) string {
	if sec < 0 {
		sec = -sec
	}
	d := sec / 86400
	h := sec % 86400 / 3600
	m := sec % 3600 / 60
	s := sec % 60
	return fmt.Sprintf("%d 天, %d 小时, %d 分 + %d 秒", d, h, m, s)
}

// Base64URLEncode 对应 Tools::base64_url_encode（URL 安全、去掉填充）。
func Base64URLEncode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// Base64URLDecode 对应 Tools::base64_url_decode，兼容带或不带填充的输入。
func Base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

var validateRe = regexp.MustCompile(`[^A-Za-z0-9\-_.]`)

// IsValidate 对应 Tools::is_validate：只允许字母、数字、- _ .
func IsValidate(s string) bool { return !validateRe.MatchString(s) }

// RealIP 对应 Tools::getRealIp：去掉 IPv4 映射前缀 ::ffff:。
func RealIP(raw string) string { return strings.ReplaceAll(raw, "::ffff:", "") }

// IsInt 对应 Tools::isInt：可带负号的纯数字。
func IsInt(s string) bool {
	s = strings.TrimPrefix(s, "-")
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// MiddleText 对应 Tools::get_middle_text：取 begin 与 end 之间的文本，找不到返回 false。
//
// 注意原版用 `strpos(...) == false` 判断，位置为 0 时也视为找不到，这里保持一致。
func MiddleText(origin, begin, end string) (string, bool) {
	b := strings.Index(origin, begin)
	if b <= 0 {
		return "", false
	}
	start := b + len(begin)
	e := strings.Index(origin[start:], end)
	if e < 0 {
		return "", false
	}
	return origin[start : start+e], true
}

// NumberFormat 对应 PHP number_format($v, $decimals)：四舍五入并使用逗号作千位分隔符。
func NumberFormat(v float64, decimals int) string {
	s := strconv.FormatFloat(PHPRound(v, decimals), 'f', decimals, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, hasFrac := strings.Cut(s, ".")
	var sb strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			sb.WriteByte(',')
		}
		sb.WriteRune(c)
	}
	out := sb.String()
	if hasFrac {
		out += "." + frac
	}
	if neg && strings.Trim(out, "0.,") != "" {
		out = "-" + out
	}
	return out
}

// NumberFormatPlain 对应 number_format($v, $decimals, '.', ”)：不带千位分隔符。
func NumberFormatPlain(v float64, decimals int) string {
	return strconv.FormatFloat(PHPRound(v, decimals), 'f', decimals, 64)
}
