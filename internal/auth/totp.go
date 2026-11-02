package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // TOTP（RFC 6238）规定使用 HMAC-SHA1，与 Google Authenticator 兼容
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

// 与原版 PHPGangsta_GoogleAuthenticator 兼容的两步验证：6 位数字、30 秒一个时间片、允许前后各 1 个时间片误差。
const (
	totpDigits = 6
	totpPeriod = 30
	totpSkew   = 1
)

const base32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// NewTOTPSecret 生成 16 位 Base32 密钥（原版 GA::createSecret）。
func NewTOTPSecret() string {
	var sb strings.Builder
	max := big.NewInt(int64(len(base32Alphabet)))
	for i := 0; i < 16; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(fmt.Sprintf("crypto/rand 不可用: %v", err))
		}
		sb.WriteByte(base32Alphabet[n.Int64()])
	}
	return sb.String()
}

// TOTPCode 计算指定时间片的验证码。
func TOTPCode(secret string, slice int64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		return "", fmt.Errorf("两步验证密钥格式错误: %w", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(slice)) //nolint:gosec // 时间片为正数
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1000000), nil
}

// VerifyTOTP 校验验证码，允许 ±1 个时间片的时钟误差。
func VerifyTOTP(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	slice := now.Unix() / totpPeriod
	for i := int64(-totpSkew); i <= totpSkew; i++ {
		c, err := TOTPCode(secret, slice+i)
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(c), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// TOTPURL 生成 otpauth:// 地址，供验证器扫码（原版 GA::getUrl）。
func TOTPURL(label, secret string) string {
	return "otpauth://totp/" + url.QueryEscape(label) + "?secret=" + secret
}
