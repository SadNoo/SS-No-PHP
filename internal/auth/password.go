// Package auth 提供密码哈希、两步验证和登录会话。
package auth

import (
	"crypto/md5" //nolint:gosec // 仅用于校验原版遗留的 md5 密码哈希
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// argon2id 参数（RFC 9106 推荐的第二组参数：64 MiB 内存、3 次迭代）。
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword 用 argon2id 生成密码哈希，格式与 PHP password_hash(PASSWORD_ARGON2ID) 相同：
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Legacy 描述原版系统的密码哈希配置，仅用于校验尚未升级的旧密码。
type Legacy struct {
	Method string // md5 / sha256 / bcrypt / argon2i / argon2id
	Salt   string
}

// VerifyPassword 校验密码。needsRehash 为 true 表示校验通过但哈希不是当前的 argon2id 参数，
// 调用方应在登录成功后用 HashPassword 重新生成并保存（K08：旧版默认 md5 + salt）。
func VerifyPassword(stored, password string, legacy Legacy) (ok, needsRehash bool) {
	switch {
	case strings.HasPrefix(stored, "$argon2id$"):
		ok, current := verifyArgon2(stored, password, true)
		return ok, ok && !current
	case strings.HasPrefix(stored, "$argon2i$"):
		ok, _ := verifyArgon2(stored, password, false)
		return ok, ok
	case strings.HasPrefix(stored, "$2y$"), strings.HasPrefix(stored, "$2a$"), strings.HasPrefix(stored, "$2b$"):
		// PHP 的 $2y$ 与 Go bcrypt 的 $2a$ 算法相同。
		h := "$2a$" + stored[4:]
		ok := bcrypt.CompareHashAndPassword([]byte(h), []byte(password)) == nil
		return ok, ok
	}
	// 无前缀：原版 md5 或 sha256 加盐（pwdMethod = md5 / sha256），均为十六进制字符串。
	var computed string
	switch legacy.Method {
	case "sha256":
		sum := sha256.Sum256([]byte(password + legacy.Salt))
		computed = hex.EncodeToString(sum[:])
	default:
		sum := md5.Sum([]byte(password + legacy.Salt)) //nolint:gosec // 仅用于校验旧版遗留哈希
		computed = hex.EncodeToString(sum[:])
	}
	ok = subtle.ConstantTimeCompare([]byte(strings.ToLower(stored)), []byte(computed)) == 1
	return ok, ok
}

// verifyArgon2 解析 PHP/标准格式的 argon2 哈希并校验。current 表示参数是否与当前设置一致。
func verifyArgon2(stored, password string, id bool) (ok, current bool) {
	parts := strings.Split(stored, "$")
	// ["", "argon2id", "v=19", "m=65536,t=3,p=2", salt, hash]
	if len(parts) != 6 {
		return false, false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, false
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 || len(want) > 1024 {
		return false, false
	}
	var got []byte
	if id {
		got = argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want))) //nolint:gosec // 长度已限制
	} else {
		got = argon2.Key([]byte(password), salt, t, m, p, uint32(len(want))) //nolint:gosec // 长度已限制
	}
	ok = subtle.ConstantTimeCompare(got, want) == 1
	current = id && m == argonMemory && t == argonTime && p == argonThreads && len(want) == argonKeyLen
	return ok, current
}
