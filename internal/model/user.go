// Package model 是在数据库记录之上的业务对象，对应原版 Models 中的计算方法（显示、权限判断等）。
package model

import (
	"crypto/md5"  //nolint:gosec // UUID v3 与原版混淆参数算法规定使用 md5
	"crypto/sha1" //nolint:gosec // 仅作密码哈希指纹，不涉及安全强度
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// 原版用固定时间表示“没有等级期限”和“账户永不过期”。
var (
	NoClassExpire  = time.Date(1989, 6, 4, 0, 5, 0, 0, time.UTC)
	NeverExpire    = time.Date(2099, 6, 4, 0, 5, 0, 0, time.UTC)
	sentinelFormat = "2006-01-02 15:04:05"
)

// User 包装数据库中的用户记录，并提供原版 User 模型中的计算方法。
type User struct {
	store.User
	Loc   *time.Location
	guest bool
}

// Guest 返回代表“未登录访客”的用户对象，模板中 $.User.IsLogin 为 false。
func Guest(loc *time.Location) *User { return &User{Loc: loc, guest: true} }

// IsLogin 对应原版 $user->isLogin。
func (u *User) IsLogin() bool { return u != nil && !u.guest }

// NewUser 创建用户对象。loc 为业务时区。
func NewUser(u store.User, loc *time.Location) *User { return &User{User: u, Loc: loc} }

// IsAdmin 对应 User::isAdmin。
func (u *User) IsAdmin() bool { return u.User.IsAdmin != 0 }

// IsEnabled 账户是否启用。
func (u *User) IsEnabled() bool { return u.Enable == 1 }

// FormatTime 把数据库中的 UTC 时间按业务时区格式化；哨兵值原样显示，与原版一致。
func (u *User) FormatTime(t time.Time) string {
	if t.Equal(NoClassExpire) || t.Equal(NeverExpire) {
		return t.UTC().Format(sentinelFormat)
	}
	return t.In(u.Loc).Format(sentinelFormat)
}

// ClassExpireStr 等级到期时间。
func (u *User) ClassExpireStr() string { return u.FormatTime(u.ClassExpire) }

// ExpireInStr 账户到期时间。
func (u *User) ExpireInStr() string { return u.FormatTime(u.ExpireIn) }

// RegDateStr 注册时间（原版 regDate）。
func (u *User) RegDateStr() string { return u.FormatTime(u.RegDate) }

// HasClassExpire 等级是否设置了期限（模板中原版判断 class_expire != "1989-06-04 00:05:00"）。
func (u *User) HasClassExpire() bool { return !u.ClassExpire.Equal(NoClassExpire) }

// LastSsTime 对应 User::lastSsTime。
func (u *User) LastSsTime() string {
	if u.T == 0 {
		return "从未使用喵"
	}
	return util.ToDateTime(u.T, u.Loc)
}

// LastCheckInTime 对应 User::lastCheckInTime。
func (u *User) LastCheckInTime() string {
	if u.User.LastCheckInTime == 0 {
		return "从未签到"
	}
	return util.ToDateTime(u.User.LastCheckInTime, u.Loc)
}

// IsAbleToCheckin 对应 User::isAbleToCheckin：今天（业务时区）是否还没签到。
func (u *User) IsAbleToCheckin(now time.Time) bool {
	return now.In(u.Loc).Format("20060102") != time.Unix(u.User.LastCheckInTime, 0).In(u.Loc).Format("20060102")
}

// AbleToCheckin 供模板使用：当前时刻是否可以签到。
func (u *User) AbleToCheckin() bool { return u.IsAbleToCheckin(time.Now()) }

// TrafficUsagePercent 对应 User::trafficUsagePercent：已用流量百分比（四舍五入到整数百分比）。
func (u *User) TrafficUsagePercent() float64 {
	if u.TransferEnable == 0 {
		return 0
	}
	// 原版：round(used / total, 2) * 100，结果总是整数。
	return math.Round(util.PHPRound(float64(u.U+u.D)/float64(u.TransferEnable), 2) * 100)
}

// EnableTraffic 对应 User::enableTraffic。
func (u *User) EnableTraffic() string { return util.FlowAutoShow(float64(u.TransferEnable)) }

// EnableTrafficInGB 对应 User::enableTrafficInGB。
func (u *User) EnableTrafficInGB() float64 { return util.BytesToGB(float64(u.TransferEnable)) }

// UsedTraffic 对应 User::usedTraffic。
func (u *User) UsedTraffic() string { return util.FlowAutoShow(float64(u.U + u.D)) }

// UnusedTraffic 对应 User::unusedTraffic。
func (u *User) UnusedTraffic() string {
	return util.FlowAutoShow(float64(u.TransferEnable - u.U - u.D))
}

// UnusedTrafficBytes 剩余流量（字节）。
func (u *User) UnusedTrafficBytes() int64 { return u.TransferEnable - u.U - u.D }

// TodayUsedTraffic 对应 User::TodayusedTraffic。
func (u *User) TodayUsedTraffic() string {
	return util.FlowAutoShow(float64(u.U + u.D - u.LastDayT))
}

// LastUsedTraffic 对应 User::LastusedTraffic。
func (u *User) LastUsedTraffic() string { return util.FlowAutoShow(float64(u.LastDayT)) }

// Gravatar 对应 User::getGravatarAttribute（原版固定返回本站头像）。
func (u *User) Gravatar() string { return "/images/Avatar.jpg" }

// UUID 对应 User::getUuid：UUID v3（DNS 命名空间，名称为 "id|passwd"）。
func (u *User) UUID() string { return UUIDv3DNS(fmt.Sprintf("%d|%s", u.ID, u.Passwd)) }

// ForbiddenIPLines 对应 get_forbidden_ip：逗号换成换行。
func (u *User) ForbiddenIPLines() string { return strings.ReplaceAll(u.ForbiddenIp, ",", "\n") }

// ForbiddenPortLines 对应 get_forbidden_port。
func (u *User) ForbiddenPortLines() string {
	return strings.ReplaceAll(u.ForbiddenPort, ",", "\n")
}

// GAURL 对应 User::getGAurl。
func (u *User) GAURL(appName string) string {
	return "otpauth://totp/" + urlEncode(appName+"-"+u.UserName+"-两步验证码") + "?secret=" + u.GaToken
}

var muKeyRe = regexp.MustCompile(`%-?[1-9]\d*m`)

// MuMd5 对应 User::getMuMd5：按 mu_regex 生成单端口多用户的混淆参数。
func (u *User) MuMd5(muRegex, muSuffix string) string {
	str := strings.NewReplacer("%id", strconv.FormatUint(uint64(u.ID), 10), "%suffix", muSuffix).Replace(muRegex)
	sum := md5.Sum([]byte(fmt.Sprintf("%d%s%s%s%s", u.ID, u.Passwd, u.Method, u.Obfs, u.Protocol))) //nolint:gosec // 与原版算法一致，非安全用途
	full := hex.EncodeToString(sum[:])
	return muKeyRe.ReplaceAllStringFunc(str, func(key string) string {
		n, err := strconv.Atoi(strings.Trim(key, "%m"))
		if err != nil {
			return key
		}
		// PHP: substr(md5, n < 0 ? n : 0, abs(n))
		l := n
		if l < 0 {
			l = -l
		}
		if l > len(full) {
			l = len(full)
		}
		if n < 0 {
			return full[len(full)-l:]
		}
		return full[:l]
	})
}

// UUIDv3DNS 生成 RFC 4122 第 3 版 UUID（MD5，DNS 命名空间），与 Ramsey\Uuid::uuid3(NAMESPACE_DNS, name) 一致。
func UUIDv3DNS(name string) string {
	ns := []byte{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	h := md5.New() //nolint:gosec // UUID v3 规定使用 MD5
	h.Write(ns)
	h.Write([]byte(name))
	b := h.Sum(nil)
	b[6] = (b[6] & 0x0f) | 0x30
	b[8] = (b[8] & 0x3f) | 0x80
	x := hex.EncodeToString(b)
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}

// PassFingerprint 返回登录密码哈希的指纹，存入会话：用户修改密码后，旧会话自动失效（与原版 Cookie 行为一致）。
func PassFingerprint(passHash string) string {
	sum := sha1.Sum([]byte(passHash)) //nolint:gosec // 仅用作指纹，不涉及安全强度
	return hex.EncodeToString(sum[:8])
}

func urlEncode(s string) string {
	var sb strings.Builder
	for _, b := range []byte(s) {
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '-', b == '_', b == '.':
			sb.WriteByte(b)
		case b == ' ':
			sb.WriteByte('+')
		default:
			fmt.Fprintf(&sb, "%%%02X", b)
		}
	}
	return sb.String()
}
