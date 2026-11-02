package server

import (
	"testing"
	"time"
)

func TestRealIPResolve(t *testing.T) {
	ri, err := NewRealIP([]string{"127.0.0.1", "10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, remote, xff, want string
	}{
		{"直连不可信，忽略 XFF", "203.0.113.9:1234", "1.1.1.1", "203.0.113.9"},
		{"可信代理，取 XFF 最右侧不可信地址", "127.0.0.1:80", "198.51.100.7", "198.51.100.7"},
		{"伪造的左侧地址被忽略", "127.0.0.1:80", "6.6.6.6, 198.51.100.7", "198.51.100.7"},
		{"跳过多层可信代理", "127.0.0.1:80", "198.51.100.7, 10.1.2.3", "198.51.100.7"},
		{"XFF 格式错误时退回直连地址", "127.0.0.1:80", "not-an-ip", "127.0.0.1"},
		{"全部可信时退回直连地址", "127.0.0.1:80", "10.0.0.1", "127.0.0.1"},
		{"无 XFF", "127.0.0.1:80", "", "127.0.0.1"},
	}
	for _, c := range cases {
		if got := ri.Resolve(c.remote, c.xff); got != c.want {
			t.Errorf("%s: Resolve(%q, %q) = %q，期望 %q", c.name, c.remote, c.xff, got, c.want)
		}
	}
}

func TestNewRealIPRejectsInvalid(t *testing.T) {
	if _, err := NewRealIP([]string{"abc"}); err == nil {
		t.Fatal("无效地址应当报错")
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(time.Hour, 2)
	for i := 0; i < 2; i++ {
		if !rl.Allow("a") {
			t.Fatal("突发额度内应当放行")
		}
	}
	if rl.Allow("a") {
		t.Fatal("超出突发额度应当拒绝")
	}
	if !rl.Allow("b") {
		t.Fatal("不同的键互不影响")
	}
}

func TestRedactPath(t *testing.T) {
	if got := redactPath("/link/abcdef"); got != "/link/***" {
		t.Fatalf("订阅令牌未隐藏: %s", got)
	}
	if got := redactPath("/user/node"); got != "/user/node" {
		t.Fatalf("普通路径不应改变: %s", got)
	}
}
