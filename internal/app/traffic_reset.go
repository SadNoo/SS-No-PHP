package app

import (
	"context"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// planReset 是一条购买记录对应商品的周期重置设置（商品内容中的 reset、reset_value、reset_exp）。
type planReset struct {
	Datetime          int64
	Reset, Value, Exp float64
}

// nextTrafficReset 计算下次流量重置在哪一天（返回该日业务时区零点），规则与每日任务 dailyJob 相同：
//   - 商品设置了周期重置（reset、reset_value、reset_exp 都不为 0）：购买后 reset_exp 天内，每满 reset 天重置一次；
//   - 用户有这类购买记录时（不论是否已过期）不再按「自动重置流量日」重置；没有时按 auto_reset_day 每月该日重置。
//
// 每日任务按执行当天的零点计算（dailyRef），这里也逐日按零点计算。todayPending 表示今天的每日任务还没有执行，
// 此时从今天开始算，否则从明天开始算。ok 为 false 表示之后不会再重置。
func nextTrafficReset(now time.Time, loc *time.Location, autoResetDay int32, plans []planReset, todayPending bool) (day time.Time, ok bool) {
	hasPlan := false
	var end int64 // 最后一个仍可能重置的时间
	for _, p := range plans {
		if p.Reset != 0 && p.Value != 0 && p.Exp != 0 {
			hasPlan = true
			end = max(end, p.Datetime+int64(p.Exp*86400))
		}
	}
	run := dailyRef(now, loc)
	if !todayPending {
		run = run.AddDate(0, 0, 1)
	}
	// 每月重置最多一年内必定出现（31 号也会在一年内出现）；周期重置到最后一个有效期为止，最多算十年。
	limit := run.AddDate(1, 0, 1)
	if hasPlan {
		limit = run.AddDate(10, 0, 0)
		if e := time.Unix(end, 0).AddDate(0, 0, 1); e.Before(limit) {
			limit = e
		}
	}
	for ; run.Before(limit); run = run.AddDate(0, 0, 1) {
		if !hasPlan {
			if autoResetDay >= 1 && autoResetDay <= 31 && run.Day() == int(autoResetDay) {
				return run, true
			}
			continue
		}
		for _, p := range plans {
			// 与每日任务相同；reset 小于 1 时每日任务的取模会出错而不重置，这里同样视为不重置。
			if p.Reset == 0 || p.Value == 0 || p.Exp == 0 || int64(p.Reset) == 0 {
				continue
			}
			days := (run.Unix() - p.Datetime) / 86400
			if float64(run.Unix())-p.Exp*86400 < float64(p.Datetime) && days%int64(p.Reset) == 0 && days != 0 {
				return run, true
			}
		}
	}
	return time.Time{}, false
}

// dailyJobPendingToday 判断今天的每日任务是否还没执行。调度器在日期变化后先把日期写入 daily:last 再执行任务；
// 从未记录过时（首次部署）第一次只记录日期、不执行，所以视为今天不会执行。
func (a *App) dailyJobPendingToday(ctx context.Context, now time.Time) (bool, error) {
	st, err := a.Q.GetJobState(ctx, "daily:last")
	if isNoRows(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return st.Value != now.In(a.Loc).Format("2006-01-02"), nil
}

// nextTrafficResetText 是用户中心「下次流量重置时间」显示的文字。
func (a *App) nextTrafficResetText(ctx context.Context, u store.User, now time.Time) (string, error) {
	rows, err := a.Q.ListUserBoughtShops(ctx, u.ID)
	if err != nil {
		return "", err
	}
	pending, err := a.dailyJobPendingToday(ctx, now)
	if err != nil {
		return "", err
	}
	plans := make([]planReset, 0, len(rows))
	for _, r := range rows {
		sm := model.NewShop(store.Shop{Content: r.Content})
		plans = append(plans, planReset{Datetime: r.Datetime, Reset: sm.Num("reset"), Value: sm.Num("reset_value"), Exp: sm.Num("reset_exp")})
	}
	if next, ok := nextTrafficReset(now, a.Loc, u.AutoResetDay, plans, pending); ok {
		return next.Format("2006-01-02"), nil
	}
	if len(rows) == 0 {
		return "未购买套餐.", nil // 与旧正式版相同
	}
	return "不重置", nil
}
