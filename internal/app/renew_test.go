package app

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

// renewShops 两个可自动续费的套餐：1 号 10 元 1 级，2 号 20 元 2 级。
func renewShops(e *testEnv) {
	e.exec(`INSERT INTO shop (id,name,price,content,auto_renew,auto_reset_bandwidth,status) VALUES
		(1,'一级',10.00,'{"bandwidth":"100","class":"1","class_expire":"30"}',30,1,1),
		(2,'二级',20.00,'{"bandwidth":"200","class":"2","class_expire":"30"}',30,1,1)`)
}

func (e *testEnv) renewCount(uid uint32) (enabled, total int) {
	_ = e.app.DB.QueryRow("SELECT COUNT(*) FROM bought WHERE userid = ? AND renew <> 0", uid).Scan(&enabled)
	_ = e.app.DB.QueryRow("SELECT COUNT(*) FROM bought WHERE userid = ?", uid).Scan(&total)
	return
}

// TestAutoRenewOffByDefault 默认不开启自动续费：购买不记录续费时间，导入的旧订单到期也不扣款，页面没有续费选项。
func TestAutoRenewOffByDefault(t *testing.T) {
	if config.Default().Users.EnableAutoRenew {
		t.Fatal("自动续费应默认关闭")
	}
	e := newEnv(t)
	ctx := context.Background()
	uid := e.login("off@example.com")
	renewShops(e)
	e.exec("UPDATE user SET money = 100 WHERE id = ?", uid)
	if r := e.post("/user/buy", url.Values{"shop": {"1"}, "autorenew": {"1"}}); r["ret"] != float64(1) {
		t.Fatalf("购买失败: %v", r)
	}
	if enabled, _ := e.renewCount(uid); enabled != 0 {
		t.Fatal("未开启自动续费时购买不应记录续费时间")
	}
	// 旧面板导入的订单：开启续费且已到期。
	e.exec(`INSERT INTO bought (userid,shopid,datetime,renew,coupon,price) VALUES (?,2,UNIX_TIMESTAMP()-100,UNIX_TIMESTAMP()-10,'',20)`, uid)
	e.mustJob("checkjob")
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.Money.String() != "90.00" || u.Class != 1 {
		t.Fatalf("未开启自动续费时不应续费扣款: money=%s class=%d", u.Money, u.Class)
	}
	if !strings.Contains(e.page("/user/bought"), "本站未开启自动续费") {
		t.Error("购买记录中的旧续费订单应显示未开启")
	}
	if shop := e.page("/user/shop"); strings.Contains(shop, `id="autorenew"`) || strings.Contains(shop, "disableothers") {
		t.Error("未开启自动续费时购买确认中不应有续费选项")
	}
}

// TestAutoRenewOnePlan 开启自动续费后，每个帐号只有最新购买的套餐自动续费，旧套餐不会重复扣款、不会把等级改回旧套餐。
func TestAutoRenewOnePlan(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Users.EnableAutoRenew = true })
	ctx := context.Background()
	uid := e.login("one@example.com")
	renewShops(e)
	e.exec("UPDATE user SET money = 100 WHERE id = ?", uid)
	if shop := e.page("/user/shop"); !strings.Contains(shop, `id="autorenew"`) || strings.Contains(shop, "disableothers") || strings.Contains(shop, `id="autorenew" type="checkbox" checked`) {
		t.Error("开启后应有“到期时自动续费”（默认不勾选），没有“关闭旧套餐自动续费”")
	}
	if r := e.post("/user/buy", url.Values{"shop": {"1"}, "autorenew": {"1"}}); r["ret"] != float64(1) {
		t.Fatalf("购买一级失败: %v", r)
	}
	if enabled, _ := e.renewCount(uid); enabled != 1 {
		t.Fatal("勾选自动续费后应记录续费时间")
	}
	// 购买二级：即使请求中带 disableothers=0，旧套餐的自动续费也被关闭。
	if r := e.post("/user/buy", url.Values{"shop": {"2"}, "autorenew": {"1"}, "disableothers": {"0"}}); r["ret"] != float64(1) {
		t.Fatalf("购买二级失败: %v", r)
	}
	var shopID uint64
	_ = e.app.DB.QueryRow("SELECT shopid FROM bought WHERE userid = ? AND renew <> 0", uid).Scan(&shopID)
	if enabled, _ := e.renewCount(uid); enabled != 1 || shopID != 2 {
		t.Fatalf("只有最新的二级套餐应自动续费: %d 个，套餐 %d", enabled, shopID)
	}
	// 不勾选自动续费购买：之前的续费也关闭。
	if r := e.post("/user/buy", url.Values{"shop": {"2"}}); r["ret"] != float64(1) {
		t.Fatalf("购买失败: %v", r)
	}
	if enabled, _ := e.renewCount(uid); enabled != 0 {
		t.Fatal("不勾选自动续费购买后不应有自动续费的订单")
	}
	// 后台为用户添加套餐：同样关闭其他套餐的自动续费。
	e.exec("UPDATE bought SET renew = UNIX_TIMESTAMP() + 100 WHERE userid = ? AND shopid = 1", uid)
	s, _ := e.app.Q.GetShop(ctx, 2)
	if err := e.app.applyShop(ctx, uid, s, false, s.Price, 0, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := e.renewCount(uid); enabled != 0 {
		t.Fatal("后台添加套餐后应关闭其他套餐的自动续费")
	}
}

// TestAutoRenewLegacyMultiple 旧数据中同一用户有两个开启续费且已到期的订单：只续费最新的一个、只扣一次款，等级为最新套餐。
func TestAutoRenewLegacyMultiple(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Users.EnableAutoRenew = true })
	ctx := context.Background()
	uid := e.login("multi@example.com")
	renewShops(e)
	e.exec("UPDATE user SET money = 100, class = 2 WHERE id = ?", uid)
	e.exec(`INSERT INTO bought (userid,shopid,datetime,renew,coupon,price) VALUES
		(?,1,UNIX_TIMESTAMP()-200,UNIX_TIMESTAMP()-20,'',10),
		(?,2,UNIX_TIMESTAMP()-100,UNIX_TIMESTAMP()-10,'',20)`, uid, uid)
	for range 3 {
		e.mustJob("checkjob")
	}
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.Money.String() != "80.00" || u.Class != 2 {
		t.Fatalf("应只续费最新的二级套餐一次: money=%s class=%d", u.Money, u.Class)
	}
	var shopID uint64
	_ = e.app.DB.QueryRow("SELECT shopid FROM bought WHERE userid = ? AND renew <> 0", uid).Scan(&shopID)
	if enabled, total := e.renewCount(uid); enabled != 1 || total != 3 || shopID != 2 {
		t.Fatalf("续费后应只有新的二级订单开启续费: 开启 %d 个、共 %d 个、套餐 %d", enabled, total, shopID)
	}
	if e.mailsTo("multi@example.com", "续费成功") != 1 {
		t.Error("应只通知一次续费成功")
	}
}

// TestAutoRenewStaleList 列出到期订单之后、扣款之前，用户关闭了自动续费或该订单已被续费：不扣款、不发提醒。
func TestAutoRenewStaleList(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Users.EnableAutoRenew = true })
	ctx := context.Background()
	uid := e.login("stale@example.com")
	renewShops(e)
	e.exec("UPDATE user SET money = 100 WHERE id = ?", uid)
	e.exec(`INSERT INTO bought (userid,shopid,datetime,renew,coupon,price) VALUES (?,1,UNIX_TIMESTAMP()-100,UNIX_TIMESTAMP()-10,'',10)`, uid)
	now := time.Now()
	list, err := e.app.Q.ListRenewDue(ctx, now.Unix())
	if err != nil || len(list) != 1 {
		t.Fatalf("应有一个到期订单: %v %d", err, len(list))
	}
	e.exec("UPDATE bought SET renew = 0 WHERE userid = ?", uid) // 用户此时关闭了自动续费
	if err := e.app.renewOne(ctx, list[0], now); err != nil {
		t.Fatal(err)
	}
	// 尚未到期（续费时间被改到之后）同样不扣款。
	e.exec("UPDATE bought SET renew = UNIX_TIMESTAMP() + 3600 WHERE userid = ?", uid)
	if err := e.app.renewOne(ctx, list[0], now); err != nil {
		t.Fatal(err)
	}
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.Money.String() != "100.00" || e.mailsTo("stale@example.com", "续费") != 0 {
		t.Fatalf("已关闭或未到期的订单不应扣款或通知: money=%s", u.Money)
	}
	if _, total := e.renewCount(uid); total != 1 {
		t.Fatal("不应生成新订单")
	}
	// 余额不足时订单保持开启，充值后下次检查再续费（只扣一次）。
	e.exec("UPDATE bought SET renew = UNIX_TIMESTAMP() - 10 WHERE userid = ?", uid)
	e.exec("UPDATE user SET money = 5 WHERE id = ?", uid)
	e.mustJob("checkjob")
	if enabled, _ := e.renewCount(uid); enabled != 1 || e.mailsTo("stale@example.com", "续费失败") != 1 {
		t.Fatal("余额不足时订单应保持开启并提醒一次")
	}
	e.exec("UPDATE user SET money = 15 WHERE id = ?", uid)
	e.mustJob("checkjob")
	e.mustJob("checkjob")
	if u, _ := e.app.Q.GetUser(ctx, uid); u.Money.String() != "5.00" {
		t.Fatalf("充值后应续费一次: money=%s", u.Money)
	}
}

// TestAutoRenewOnlyLatest 旧数据中同一帐号有多个开启续费的订单时，只有该帐号最新的订单（不论是否已到期）可以续费：
// 最新订单余额不足时等待充值，不回退续费较旧、较便宜的套餐；最新订单还没到期时，较旧的订单到期也不续费、不降级；
// 用户关闭了最新订单的续费、或最新订单的商品已删除时，较旧的订单同样不续费。较旧订单的续费被关闭，最新订单的续费保持不变。
func TestAutoRenewOnlyLatest(t *testing.T) {
	future := time.Now().Add(30 * 24 * time.Hour).Format(time.DateTime)
	for _, c := range []struct {
		name     string
		money    int
		bRenew   string // 最新订单 B 的 renew
		dropShop bool   // 删除 B 的商品
		wantB    bool   // B 仍开启续费
		mail     string // 应收到的通知
	}{
		{"最新订单余额不足（第三方复现一）", 15, "UNIX_TIMESTAMP()-10", false, true, "续费失败"},
		{"最新订单还没到期（第三方复现二）", 100, "UNIX_TIMESTAMP()+3600", false, true, ""},
		{"用户已关闭最新订单的续费", 100, "0", false, false, ""},
		{"最新订单的商品已删除", 100, "UNIX_TIMESTAMP()-10", true, false, "续费失败"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, func(c *config.Config) { c.Users.EnableAutoRenew = true })
			ctx := context.Background()
			uid := e.login("latest@example.com")
			renewShops(e)
			e.exec("UPDATE user SET money = ?, class = 2, class_expire = ? WHERE id = ?", c.money, future, uid)
			e.exec(`INSERT INTO bought (id,userid,shopid,datetime,renew,coupon,price) VALUES (101,?,1,UNIX_TIMESTAMP()-200,UNIX_TIMESTAMP()-20,'',10)`, uid)
			e.exec(`INSERT INTO bought (id,userid,shopid,datetime,renew,coupon,price) VALUES (102,?,2,UNIX_TIMESTAMP()-100,`+c.bRenew+`,'',20)`, uid)
			if c.dropShop {
				e.exec("DELETE FROM shop WHERE id = 2")
			}
			for range 2 {
				e.mustJob("checkjob")
			}
			u, _ := e.app.Q.GetUser(ctx, uid)
			if u.Money.String() != strconv.Itoa(c.money)+".00" || u.Class != 2 {
				t.Fatalf("不应扣款或降级: money=%s class=%d", u.Money, u.Class)
			}
			var aRenew, bRenew int64
			_ = e.app.DB.QueryRow("SELECT renew FROM bought WHERE id = 101").Scan(&aRenew)
			_ = e.app.DB.QueryRow("SELECT renew FROM bought WHERE id = 102").Scan(&bRenew)
			if _, total := e.renewCount(uid); total != 2 || aRenew != 0 || (bRenew != 0) != c.wantB {
				t.Fatalf("不应生成新订单；旧订单关闭续费、最新订单保持: 共 %d 个、A=%d、B=%d", total, aRenew, bRenew)
			}
			if c.mail != "" && e.mailsTo("latest@example.com", c.mail) != 1 || e.mailsTo("latest@example.com", "续费成功") != 0 {
				t.Fatal("通知不正确")
			}
			if c.name == "最新订单余额不足（第三方复现一）" { // 充值后续费最新的二级套餐
				e.exec("UPDATE user SET money = 25 WHERE id = ?", uid)
				e.mustJob("checkjob")
				e.mustJob("checkjob")
				if u, _ := e.app.Q.GetUser(ctx, uid); u.Money.String() != "5.00" || u.Class != 2 {
					t.Fatalf("充值后应续费二级一次: money=%s class=%d", u.Money, u.Class)
				}
			}
		})
	}
}

// TestAutoRenewShopGoneNoNotice 商品已删除时，只有最新、仍开启续费且已到期的订单才提醒“商品已下架”：
// 被新订单替代的旧订单只关闭续费；列出到期订单后用户关闭了续费的订单不处理。两种都不扣款、不提醒、不删除订单。
func TestAutoRenewShopGoneNoNotice(t *testing.T) {
	future := time.Now().Add(30 * 24 * time.Hour).Format(time.DateTime)
	setup := func(t *testing.T) (*testEnv, uint32) {
		e := newEnv(t, func(c *config.Config) { c.Users.EnableAutoRenew = true })
		uid := e.login("gone@example.com")
		renewShops(e)
		e.exec("UPDATE user SET money = 100, class = 2, class_expire = ? WHERE id = ?", future, uid)
		return e, uid
	}
	check := func(t *testing.T, e *testEnv, uid uint32, wantTotal int) {
		t.Helper()
		u, _ := e.app.Q.GetUser(context.Background(), uid)
		if u.Money.String() != "100.00" || u.Class != 2 {
			t.Fatalf("不应扣款或降级: money=%s class=%d", u.Money, u.Class)
		}
		if n := e.mailsTo("gone@example.com", "续费"); n != 0 {
			t.Fatalf("不应发送续费提醒: %d", n)
		}
		if _, total := e.renewCount(uid); total != wantTotal {
			t.Fatalf("不应删除或新增订单: 共 %d 个", total)
		}
	}

	t.Run("旧订单已被新订单替代（第三方复现一）", func(t *testing.T) {
		e, uid := setup(t)
		e.exec(`INSERT INTO bought (id,userid,shopid,datetime,renew,coupon,price) VALUES (101,?,1,UNIX_TIMESTAMP()-200,UNIX_TIMESTAMP()-20,'',10)`, uid)
		e.exec(`INSERT INTO bought (id,userid,shopid,datetime,renew,coupon,price) VALUES (102,?,2,UNIX_TIMESTAMP()-100,UNIX_TIMESTAMP()+3600,'',20)`, uid)
		e.exec("DELETE FROM shop WHERE id = 1")
		e.mustJob("checkjob")
		e.mustJob("checkjob")
		check(t, e, uid, 2)
		var aRenew, bRenew int64
		_ = e.app.DB.QueryRow("SELECT renew FROM bought WHERE id = 101").Scan(&aRenew)
		_ = e.app.DB.QueryRow("SELECT renew FROM bought WHERE id = 102").Scan(&bRenew)
		if aRenew != 0 || bRenew == 0 {
			t.Fatalf("应关闭旧订单的续费、保留最新订单的续费: A=%d B=%d", aRenew, bRenew)
		}
	})

	t.Run("列出到期订单后用户关闭了续费（第三方复现二）", func(t *testing.T) {
		e, uid := setup(t)
		ctx := context.Background()
		e.exec(`INSERT INTO bought (id,userid,shopid,datetime,renew,coupon,price) VALUES (101,?,1,UNIX_TIMESTAMP()-200,UNIX_TIMESTAMP()-20,'',10)`, uid)
		now := time.Now()
		list, err := e.app.Q.ListRenewDue(ctx, now.Unix())
		if err != nil || len(list) != 1 {
			t.Fatalf("应有一个到期订单: %v %d", err, len(list))
		}
		e.exec("UPDATE bought SET renew = 0 WHERE id = 101")
		e.exec("DELETE FROM shop WHERE id = 1")
		if err := e.app.renewOne(ctx, list[0], now); err != nil {
			t.Fatal(err)
		}
		check(t, e, uid, 1)
	})
}
