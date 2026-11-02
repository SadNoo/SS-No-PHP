package sub

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// TestUserExpiry 覆盖订阅到期口径：付费用户显示套餐（等级）到期，普通用户正文显示注册时间且响应头不写 expire，
// 异常用户（等级大于 0 却无等级到期）不写 expire 并提示；响应头与正文使用同一口径。
func TestUserExpiry(t *testing.T) {
	reg := time.Date(2025, 3, 1, 8, 0, 0, 0, time.UTC)
	plan := time.Date(2027, 1, 15, 16, 30, 0, 0, time.UTC) // 北京时间 2027-01-16
	sh, _ := time.LoadLocation("Asia/Shanghai")
	base := store.User{TransferEnable: 1 << 30, U: 10, D: 20, RegDate: reg, ExpireIn: time.Now().AddDate(100, 0, 0)}
	cases := []struct {
		name       string
		class      int32
		classExp   time.Time
		kind       ExpiryKind
		header     string
		bodySuffix string
	}{
		{"付费用户", 2, plan, ExpiryPlan, "upload=10; download=20; total=1073741824; expire=" + strconv.FormatInt(plan.Unix(), 10), "套餐到期：2027-01-16"},
		// 新注册用户默认带 24 小时的等级到期时间，但等级为 0，不能显示为“明天到期”。
		{"普通用户", 0, time.Now().Add(24 * time.Hour), ExpiryNone, "upload=10; download=20; total=1073741824", "注册时间：2025-03-01"},
		{"异常用户", 3, time.Date(1989, 6, 4, 0, 5, 0, 0, time.UTC), ExpiryInvalid, "upload=10; download=20; total=1073741824", "套餐到期：未设置，请联系管理员"},
	}
	for _, c := range cases {
		u := base
		u.Class, u.ClassExpire = c.class, c.classExp
		if kind, _ := UserExpiry(u); kind != c.kind {
			t.Errorf("%s: kind = %v", c.name, kind)
		}
		if got := UserinfoHeader(u); got != c.header {
			t.Errorf("%s: header = %q, want %q", c.name, got, c.header)
		}
		if got := expiryText(u, sh); got != c.bodySuffix {
			t.Errorf("%s: body = %q, want %q", c.name, got, c.bodySuffix)
		}
		// 正文附带信息使用同一口径（以 Surge 注释为例）。
		body := withSubscriptionInfo("[Proxy]\n", requestFor(t, "surge=5&extend=1"), u, time.Now(), sh)
		if !strings.Contains(body, c.bodySuffix) || strings.Contains(body, "过期时间") {
			t.Errorf("%s: 正文口径不一致: %q", c.name, body)
		}
	}
}
