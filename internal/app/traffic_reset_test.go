package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

func TestNextTrafficReset(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04:05", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	noon := at("2026-10-20 12:00:00").Unix()
	for _, c := range []struct {
		name    string
		now     string
		pending bool // 今天的每日任务是否还没执行
		day     int32
		plans   []planReset
		want    string // 空表示不再重置
	}{
		{"没有购买、没有每月重置", "2026-10-27 10:00:00", false, 0, nil, ""},
		{"每月 15 日", "2026-10-27 10:00:00", false, 15, nil, "2026-11-15"},
		{"每月重置日就是今天，今天已执行", "2026-10-27 10:00:00", false, 27, nil, "2026-11-27"},
		{"每月重置日就是今天，零点后今天的任务已执行（第三方复现二）", "2026-10-27 00:00:30", false, 27, nil, "2026-11-27"},
		{"每月重置日就是今天，零点后今天的任务还没执行", "2026-10-27 00:00:30", true, 27, nil, "2026-10-27"},
		{"零点前一秒，明天是重置日", "2026-10-26 23:59:59", false, 27, nil, "2026-10-27"},
		{"31 日跳过没有 31 日的月份", "2026-11-05 10:00:00", false, 31, nil, "2026-12-31"},
		{"每 7 天，中午购买", "2026-10-27 10:00:00", false, 0, []planReset{{noon, 7, 100, 30}}, "2026-10-28"},
		{"每 7 天，00:00:30 购买（第三方复现一）", "2026-10-26 12:00:00", false, 0, []planReset{{at("2026-10-20 00:00:30").Unix(), 7, 50, 30}}, "2026-10-28"},
		{"每 7 天，00:00:00 购买", "2026-10-26 12:00:00", false, 0, []planReset{{at("2026-10-20 00:00:00").Unix(), 7, 50, 30}}, "2026-10-27"},
		{"每 7 天，00:00:59 购买", "2026-10-26 12:00:00", false, 0, []planReset{{at("2026-10-20 00:00:59").Unix(), 7, 50, 30}}, "2026-10-28"},
		{"每 7 天，23:59:59 购买", "2026-10-26 12:00:00", false, 0, []planReset{{at("2026-10-20 23:59:59").Unix(), 7, 50, 30}}, "2026-10-28"},
		{"今天就是重置日、今天的任务还没执行", "2026-10-27 00:00:10", true, 0, []planReset{{at("2026-10-20 00:00:00").Unix(), 7, 50, 30}}, "2026-10-27"},
		{"今天就是重置日、今天的任务已执行", "2026-10-27 00:00:10", false, 0, []planReset{{at("2026-10-20 00:00:00").Unix(), 7, 50, 30}}, "2026-11-03"},
		{"有效期已过，不再重置，也不按每月重置", "2026-10-27 10:00:00", false, 15, []planReset{{noon, 7, 100, 5}}, ""},
		{"有效期内最后一次", "2026-11-12 10:00:00", false, 0, []planReset{{noon, 7, 100, 30}}, "2026-11-18"},
		{"有效期到第 30 天，第 35 天不再重置", "2026-11-19 10:00:00", false, 0, []planReset{{noon, 7, 100, 30}}, ""},
		{"多个套餐取最早", "2026-10-27 10:00:00", false, 0, []planReset{{noon, 10, 100, 60}, {noon, 7, 100, 30}}, "2026-10-28"},
		{"没有周期重置的套餐不影响每月重置", "2026-10-27 10:00:00", false, 3, []planReset{{noon, 0, 0, 0}}, "2026-11-03"},
		{"reset 小于 1 天时每日任务不会重置", "2026-10-27 10:00:00", false, 3, []planReset{{noon, 0.5, 100, 30}}, ""},
	} {
		next, ok := nextTrafficReset(at(c.now), loc, c.day, c.plans, c.pending)
		got := ""
		if ok {
			got = next.Format("2006-01-02")
		}
		if got != c.want {
			t.Errorf("%s：得到 %q，应为 %q", c.name, got, c.want)
		}
	}
}

// TestNextTrafficResetMatchesDailyJob 确认显示的日期与每日任务实际重置的日期一致。每日任务在零点后一分钟内的不同秒数、
// 以及停机后较晚执行，都在显示的日期当天重置；前一天（零点与 23:59:59）都不重置。每种情况用单独的数据库。
func TestNextTrafficResetMatchesDailyJob(t *testing.T) {
	type tc struct {
		name    string
		bought  time.Duration // 购买时间：3 天前的零点加上这个时长；-1 表示没有套餐，按每月重置
		dayOff  int           // 每月重置日：今天加几天的日期（bought 为 -1 时使用）
		ranOK   bool          // 今天的每日任务是否已执行（daily:last 为今天）
		wantOff int           // 期望的重置日：今天加几天
		wantGB  float64
	}
	for _, c := range []tc{
		{"套餐每 7 天，00:00:00 购买", 0, 0, true, 4, 50},
		{"套餐每 7 天，00:00:30 购买（第 7 天零点还差 30 秒满 7 天）", 30 * time.Second, 0, true, 5, 50},
		{"套餐每 7 天，00:00:59 购买", 59 * time.Second, 0, true, 5, 50},
		{"套餐每 7 天，23:59:59 购买", 24*time.Hour - time.Second, 0, true, 5, 50},
		{"每月重置日在 5 天后", -1, 5, true, 5, 20},
		{"每月重置日是今天、今天的任务还没执行", -1, 0, false, 0, 20},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			uid := e.login("reset@example.com")
			now := time.Now()
			today := dailyRef(now, e.app.Loc)
			lastRun := today
			if !c.ranOK {
				lastRun = today.AddDate(0, 0, -1)
			}
			if err := e.app.Q.SetJobState(ctx, store.SetJobStateParams{Name: "daily:last", Value: lastRun.Format("2006-01-02"), UpdatedAt: now.Unix()}); err != nil {
				t.Fatal(err)
			}
			if c.bought >= 0 {
				e.exec(`INSERT INTO shop (id,name,price,content,auto_renew,auto_reset_bandwidth,status) VALUES (1,'周期重置',10.00,'{"bandwidth":"100","reset":"7","reset_value":"50","reset_exp":"30"}',0,0,1)`)
				e.exec(`INSERT INTO bought (userid,shopid,datetime,renew,coupon,price) VALUES (?,1,?,0,'',10)`, uid, today.AddDate(0, 0, -3).Add(c.bought).Unix())
			} else {
				e.exec(`UPDATE user SET auto_reset_day = ?, auto_reset_bandwidth = 20 WHERE id = ?`, today.AddDate(0, 0, c.dayOff).Day(), uid)
			}
			restore := func() {
				e.exec(`UPDATE user SET transfer_enable = ?, u = 123, d = 456 WHERE id = ?`, util.GBToBytes(1), uid)
			}
			restore()
			u, _ := e.app.Q.GetUser(ctx, uid)
			text, err := e.app.nextTrafficResetText(ctx, u, now)
			if err != nil {
				t.Fatal(err)
			}
			day := today.AddDate(0, 0, c.wantOff)
			if text != day.Format("2006-01-02") {
				t.Fatalf("显示 %q，应为 %s", text, day.Format("2006-01-02"))
			}
			if !strings.Contains(e.page("/user"), `<p class="traffic-reset">下次流量重置时间：`+text+`</p>`) {
				t.Error("首页显示的下次流量重置时间不正确")
			}
			for _, run := range []time.Time{day.AddDate(0, 0, -1), day.Add(-time.Second)} {
				if c.wantOff == 0 && run.Before(today) {
					continue // 重置日是今天时，前一天已在过去
				}
				if err := e.app.dailyJob(ctx, run); err != nil {
					t.Fatal(err)
				}
				if u, _ := e.app.Q.GetUser(ctx, uid); u.TransferEnable != util.GBToBytes(1) {
					t.Fatalf("在 %s 执行的每日任务提前重置了（显示 %s）", run.Format(time.DateTime), text)
				}
			}
			for _, run := range []time.Time{day, day.Add(30 * time.Second), day.Add(59 * time.Second), day.Add(9 * time.Hour)} {
				if err := e.app.dailyJob(ctx, run); err != nil {
					t.Fatal(err)
				}
				if u, _ := e.app.Q.GetUser(ctx, uid); u.TransferEnable != util.GBToBytes(c.wantGB) || u.U != 0 || u.D != 0 {
					t.Fatalf("在 %s 执行的每日任务应重置为 %v GB：%d %d %d", run.Format(time.DateTime), c.wantGB, u.TransferEnable, u.U, u.D)
				}
				restore()
			}
		})
	}

	// 当天已经完成重置：每月重置日是今天、今天的任务已执行时，显示下个月。
	e := newEnv(t)
	ctx := context.Background()
	uid := e.login("reset-done@example.com")
	now := time.Now()
	today := dailyRef(now, e.app.Loc)
	if err := e.app.Q.SetJobState(ctx, store.SetJobStateParams{Name: "daily:last", Value: today.Format("2006-01-02"), UpdatedAt: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	e.exec(`UPDATE user SET auto_reset_day = ?, auto_reset_bandwidth = 20 WHERE id = ?`, today.Day(), uid)
	u, _ := e.app.Q.GetUser(ctx, uid)
	if text, _ := e.app.nextTrafficResetText(ctx, u, now); text == today.Format("2006-01-02") || !strings.HasSuffix(text, today.Format("-02")) {
		t.Errorf("今天已重置后应显示之后的月份，得到 %q", text)
	}
	// 首次部署（还没有 daily:last）：调度器第一次只记录日期、不执行，所以今天不算。
	if err := e.app.Q.DeleteJobState(ctx, "daily:last"); err != nil {
		t.Fatal(err)
	}
	if text, _ := e.app.nextTrafficResetText(ctx, u, now); text == today.Format("2006-01-02") {
		t.Errorf("首次部署当天不应显示今天，得到 %q", text)
	}

	// 没有购买记录也没有每月重置时与旧正式版相同；有购买记录但不会再重置时显示“不重置”。
	e.get("/user/logout")
	nu := e.login("reset-none@example.com")
	if !strings.Contains(e.page("/user"), `<p class="traffic-reset">下次流量重置时间：未购买套餐.</p>`) {
		t.Error("没有购买记录时应显示“未购买套餐.”")
	}
	e.exec(`INSERT INTO shop (id,name,price,content,auto_renew,auto_reset_bandwidth,status) VALUES (1,'月付',10.00,'{"bandwidth":"100"}',0,0,1)`)
	e.exec(`INSERT INTO bought (userid,shopid,datetime,renew,coupon,price) VALUES (?,1,UNIX_TIMESTAMP(),0,'',10)`, nu)
	if !strings.Contains(e.page("/user"), `<p class="traffic-reset">下次流量重置时间：不重置</p>`) {
		t.Error("有购买记录但不会重置时应显示“不重置”")
	}
}
