package auth

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("哈希格式错误: %s", h)
	}
	if ok, rehash := VerifyPassword(h, "correct horse", Legacy{}); !ok || rehash {
		t.Fatal("新哈希应当校验通过且无需升级")
	}
	if ok, _ := VerifyPassword(h, "wrong", Legacy{}); ok {
		t.Fatal("错误密码不应通过")
	}
}

func TestLegacyMD5AndSHA256(t *testing.T) {
	sum := md5.Sum([]byte("pw" + "salty"))
	md5Hash := hex.EncodeToString(sum[:])
	if ok, rehash := VerifyPassword(md5Hash, "pw", Legacy{Method: "md5", Salt: "salty"}); !ok || !rehash {
		t.Fatal("旧版 md5 哈希应当通过并要求升级")
	}
	if ok, _ := VerifyPassword(md5Hash, "pw", Legacy{Method: "md5", Salt: "other"}); ok {
		t.Fatal("盐不对时不应通过")
	}
	s := sha256.Sum256([]byte("pw" + "salty"))
	shaHash := hex.EncodeToString(s[:])
	if ok, rehash := VerifyPassword(shaHash, "pw", Legacy{Method: "sha256", Salt: "salty"}); !ok || !rehash {
		t.Fatal("旧版 sha256 哈希应当通过并要求升级")
	}
}

func TestLegacyBcryptPHP(t *testing.T) {
	h, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	php := "$2y$" + string(h)[4:]
	if ok, rehash := VerifyPassword(php, "pw", Legacy{Method: "bcrypt"}); !ok || !rehash {
		t.Fatal("PHP bcrypt 哈希应当通过并要求升级")
	}
}

// 以下哈希由 PHP 8.4 password_hash("password", ...) 实际生成，验证与原版哈希格式兼容。
func TestLegacyPHPHashes(t *testing.T) {
	hashes := []string{
		"$argon2id$v=19$m=65536,t=4,p=1$alY3TnI3REpzRDVnQ0VuUw$Gh/fqB8dOIBQKBSakQ197PHQnPH7I8gT0jdhI0IgWBk",
		"$argon2i$v=19$m=65536,t=4,p=1$a2xRSEh4WDQ2ODdBRFZ0OQ$C/ocbkNjNrB46ZMDaJl10eswGWiFutwPZoY+ssgrk5U",
		"$2y$12$RrPWuabUtflgDhyLRJuL5OHI9kvYR9iJ3DgCE.OlGwL4w/gLNEOti",
	}
	for _, h := range hashes {
		ok, rehash := VerifyPassword(h, "password", Legacy{})
		if !ok || !rehash {
			t.Errorf("%s: ok=%v rehash=%v，期望通过并要求升级", h[:10], ok, rehash)
		}
		if ok, _ := VerifyPassword(h, "wrong", Legacy{}); ok {
			t.Errorf("%s: 错误密码不应通过", h[:10])
		}
	}
}

// RFC 6238 附录 B 的 SHA1 测试向量（密钥 "12345678901234567890"，取后 6 位）。
func TestTOTPVectors(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"}
	for ts, want := range cases {
		got, err := TOTPCode(secret, ts/30)
		if err != nil || got != want {
			t.Errorf("t=%d: 得到 %s，期望 %s（%v）", ts, got, want, err)
		}
	}
	if !VerifyTOTP(secret, "287082", time.Unix(59+30, 0)) {
		t.Error("应允许前一个时间片")
	}
	if VerifyTOTP(secret, "287082", time.Unix(59+90, 0)) {
		t.Error("超过误差范围不应通过")
	}
	if VerifyTOTP(secret, "", time.Unix(59, 0)) {
		t.Error("空验证码不应通过")
	}
}

func TestNewTOTPSecret(t *testing.T) {
	s := NewTOTPSecret()
	if len(s) != 16 {
		t.Fatal(s)
	}
	if _, err := TOTPCode(s, 1); err != nil {
		t.Fatal(err)
	}
}
