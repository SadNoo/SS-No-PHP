package model

import (
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

func TestUUIDv3(t *testing.T) {
	// Python: uuid.uuid3(uuid.NAMESPACE_DNS, "1|abc")
	if got := UUIDv3DNS("1|abc"); got != "42547b9c-ef12-3a10-9dea-2df0ce7ea5f3" {
		t.Fatal(got)
	}
}

func TestMuMd5(t *testing.T) {
	u := NewUser(store.User{ID: 1, Passwd: "abc", Method: "rc4-md5",
		Obfs: "plain", Protocol: "origin"}, time.UTC)
	// PHP: substr(md5("1abcrc4-md5plainorigin"), 0, 5) = f66ff
	if got := u.MuMd5("%5m%id.%suffix", "microsoft.com"); got != "f66ff1.microsoft.com" {
		t.Fatal(got)
	}
}

func TestTrafficPercentAndCheckin(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	u := NewUser(store.User{U: 300, D: 270, TransferEnable: 1000}, loc)
	if got := u.TrafficUsagePercent(); got != 57 {
		t.Fatalf("百分比 %v", got)
	}
	// 北京时间 2026-01-02 00:30 签到，同一天 23:00 不能再签，第二天可以。
	u.User.LastCheckInTime = time.Date(2026, 1, 2, 0, 30, 0, 0, loc).Unix()
	if u.IsAbleToCheckin(time.Date(2026, 1, 2, 23, 0, 0, 0, loc)) {
		t.Fatal("同一天不应能签到")
	}
	if !u.IsAbleToCheckin(time.Date(2026, 1, 3, 0, 1, 0, 0, loc)) {
		t.Fatal("第二天应能签到")
	}
}

func TestSentinelTimes(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	u := NewUser(store.User{ClassExpire: NoClassExpire, ExpireIn: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, loc)
	if u.ClassExpireStr() != "1989-06-04 00:05:00" || u.HasClassExpire() {
		t.Fatal("哨兵值应原样显示")
	}
	if u.ExpireInStr() != "2026-01-01 08:00:00" {
		t.Fatal(u.ExpireInStr())
	}
}
