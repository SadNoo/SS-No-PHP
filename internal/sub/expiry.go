package sub

import (
	"fmt"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// ExpiryKind 区分订阅中显示的到期信息类型。帐号有效期（ExpireIn）不在订阅中显示：
// 新帐号按 100 年计算，帐号到期意味着删除，不是用户需要关注的“到期”。
type ExpiryKind int

const (
	// ExpiryPlan 付费用户（等级大于 0）：显示套餐到期时间，即等级到期时间。
	ExpiryPlan ExpiryKind = iota
	// ExpiryNone 普通用户（等级为 0）：没有套餐期限，正文显示注册时间，响应头不写 expire。
	ExpiryNone
	// ExpiryInvalid 等级大于 0 却没有等级到期时间（1989 哨兵）。这是不允许存在的异常数据：
	// 响应头不写 expire，正文提示联系管理员，调用方应记录警告。
	ExpiryInvalid
)

// UserExpiry 返回订阅显示用的到期类型与时间（ExpiryPlan 为等级到期时间，ExpiryNone 为注册时间）。
// 响应头与正文统一使用本函数，避免两处口径不一致。
func UserExpiry(u store.User) (ExpiryKind, time.Time) {
	if u.Class <= 0 {
		return ExpiryNone, u.RegDate
	}
	if u.ClassExpire.Year() <= 1989 {
		return ExpiryInvalid, time.Time{}
	}
	return ExpiryPlan, u.ClassExpire
}

// UserinfoHeader 生成 Subscription-Userinfo 响应头。只有付费用户写 expire：
// 客户端把 expire 解读为到期时间，写入注册时间（过去的日期）会被显示为已过期。
func UserinfoHeader(u store.User) string {
	h := fmt.Sprintf("upload=%d; download=%d; total=%d", u.U, u.D, u.TransferEnable)
	if kind, t := UserExpiry(u); kind == ExpiryPlan {
		h += fmt.Sprintf("; expire=%d", t.Unix())
	}
	return h
}

// expiryText 是订阅正文附带信息中的到期部分。
func expiryText(u store.User, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	switch kind, t := UserExpiry(u); kind {
	case ExpiryPlan:
		return "套餐到期：" + t.In(loc).Format("2006-01-02")
	case ExpiryNone:
		return "注册时间：" + t.In(loc).Format("2006-01-02")
	default:
		return "套餐到期：未设置，请联系管理员"
	}
}
