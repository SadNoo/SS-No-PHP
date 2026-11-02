package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/db"
	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// 定时任务（原版 crontab + xcat：checkjob、dailyjob、sendDiaryMail、sendFinanceMail_*、syncnode、detectGFW）。
//
// 与原版的差异（K03）：
//   - 每个用户、每个节点单独处理，一个出错只记录日志，不影响其他用户（原版任一用户出错或写文件失败会 die，后面的用户全部不处理）；
//   - 每个任务执行时持有 MySQL 命名锁，同一任务不会重叠执行，多实例部署时也只有一个实例在执行；
//   - “已发送提醒”等状态保存在 job_state 表中，不再依赖服务器上的空文件。

type jobFunc func(ctx context.Context, now time.Time) error

// jobs 返回全部任务，键为原版 xcat 命令名。
func (a *App) jobs() map[string]jobFunc {
	return map[string]jobFunc{
		"checkjob":              a.checkJob,
		"dailyjob":              a.dailyJob,
		"sendDiaryMail":         a.sendDailyMail,
		"sendFinanceMail_day":   func(ctx context.Context, now time.Time) error { return a.financeMail(ctx, now, "day") },
		"sendFinanceMail_week":  func(ctx context.Context, now time.Time) error { return a.financeMail(ctx, now, "week") },
		"sendFinanceMail_month": func(ctx context.Context, now time.Time) error { return a.financeMail(ctx, now, "month") },
		"syncnode":              a.syncNode,
		"detectGFW":             a.detectGFW,
		"synclogin":             a.radiusSyncLogin,
		"syncvpn":               a.radiusSyncVPN,
	}
}

// JobNames 返回全部任务名（命令行帮助使用）。
func (a *App) JobNames() []string {
	var names []string
	for k := range a.jobs() {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// RunJob 执行一个任务。任务正在其他进程中执行时返回 db.ErrLockNotAcquired。
func (a *App) RunJob(ctx context.Context, name string) error {
	fn, ok := a.jobs()[name]
	if !ok {
		return fmt.Errorf("未知任务 %q，可用任务：%s", name, strings.Join(a.JobNames(), "、"))
	}
	lock, err := db.AcquireLock(ctx, a.DB, "niuniupanel:job:"+name, 0)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release(context.WithoutCancel(ctx)) }()
	start := time.Now()
	err = fn(ctx, start)
	level := slog.LevelInfo
	if name == "checkjob" || name == "syncnode" || name == "synclogin" || name == "syncvpn" { // 每分钟执行的任务只在调试级别记录
		level = slog.LevelDebug
	}
	a.Log.Log(ctx, level, "定时任务完成", "job", name, "cost", time.Since(start).Round(time.Millisecond), "err", err)
	return err
}

// StartScheduler 启动内置调度器，每分钟检查一次需要执行的任务，ctx 取消时退出。
func (a *App) StartScheduler(ctx context.Context) {
	if !a.Cfg.Jobs.Enable {
		a.Log.Info("定时任务未启用（jobs.enable = false）")
		return
	}
	go func() {
		for {
			now := time.Now()
			a.schedule(ctx, now)
			next := now.Truncate(time.Minute).Add(time.Minute)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
			}
		}
	}()
}

// schedule 决定本分钟需要执行哪些任务。
func (a *App) schedule(ctx context.Context, now time.Time) {
	run := func(name string) {
		if err := a.RunJob(ctx, name); err != nil && !errors.Is(err, db.ErrLockNotAcquired) {
			a.Log.Error("定时任务失败", "job", name, "err", err)
		}
	}
	local := now.In(a.Loc)
	today := local.Format("2006-01-02")

	run("checkjob")
	if a.Cfg.Jobs.SyncNode {
		run("syncnode")
	}
	if a.Radius != nil {
		run("synclogin")
		run("syncvpn")
	}
	// 每日任务：日期变化后执行一次。首次部署时只记录日期不执行，避免在白天清零“今日已用流量”。
	if a.onceADay(ctx, "daily:last", today, true) {
		run("dailyjob")
	}
	if t := a.Cfg.Jobs.DailyMailTime; t != "" && local.Format("15:04") >= t {
		if a.onceADay(ctx, "dailymail:last", today, false) {
			run("sendDiaryMail")
		}
	}
	if a.Cfg.Jobs.FinanceMail {
		if a.onceADay(ctx, "finance_day:last", today, true) {
			run("sendFinanceMail_day")
		}
		if local.Weekday() == time.Sunday && a.onceADay(ctx, "finance_week:last", today, false) {
			run("sendFinanceMail_week")
		}
		if local.Day() == 1 && a.onceADay(ctx, "finance_month:last", today, false) {
			run("sendFinanceMail_month")
		}
	}
	if a.Cfg.Jobs.DetectGFW {
		interval := int64(max(a.Cfg.Detect.GFWInterval, 60))
		last := int64(0)
		if st, err := a.Q.GetJobState(ctx, "gfw:last"); err == nil {
			last = st.UpdatedAt
		}
		if now.Unix()-last >= interval {
			_ = a.Q.SetJobState(ctx, store.SetJobStateParams{Name: "gfw:last", Value: "", UpdatedAt: now.Unix()})
			run("detectGFW")
		}
	}
}

// onceADay 当天尚未执行时记录并返回 true。skipFirst 为 true 时，从未执行过的任务只记录日期、不执行。
func (a *App) onceADay(ctx context.Context, key, today string, skipFirst bool) bool {
	st, err := a.Q.GetJobState(ctx, key)
	switch {
	case err == nil && st.Value == today:
		return false
	case err != nil && !isNoRows(err):
		a.Log.Error("读取任务状态失败", "key", key, "err", err)
		return false
	}
	if err := a.Q.SetJobState(ctx, store.SetJobStateParams{Name: key, Value: today, UpdatedAt: time.Now().Unix()}); err != nil {
		a.Log.Error("保存任务状态失败", "key", key, "err", err)
		return false
	}
	return !isNoRows(err) || !skipFirst
}

// flag / setFlag / clearFlag 读写 job_state 中的标记（原版 storage/ 下的空文件）。
func (a *App) flag(ctx context.Context, name string) bool {
	_, err := a.Q.GetJobState(ctx, name)
	return err == nil
}

func (a *App) setFlag(ctx context.Context, name string) error {
	return a.Q.SetJobState(ctx, store.SetJobStateParams{Name: name, Value: "1", UpdatedAt: time.Now().Unix()})
}

func (a *App) clearFlag(ctx context.Context, name string) error { return a.Q.DeleteJobState(ctx, name) }

// safely 执行 fn，panic 时转为错误（单个对象出错不影响整个任务）。
func safely(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	return fn()
}

// warnMail 发送原版 news/warn.tpl 格式的通知邮件，失败只记录日志。
func (a *App) warnMail(ctx context.Context, u store.User, subject, text string) {
	if err := a.Mail.Send(ctx, u.Email, subject, "warn", map[string]any{"user": u, "text": text}); err != nil {
		a.Log.Warn("发送邮件失败", "to", u.Email, "subject", subject, "err", err)
	}
}

// ---- dailyjob（原版 Job::DailyJob）----

// dailyRef 是每日任务计算套餐周期重置时使用的时间：执行当天的业务时区零点。
// 原版由 crontab 在 00:00 执行；新版调度器在日期变化后的第一次检查（零点后一分钟内，面板停机时更晚）执行，
// 统一按当天零点计算，结果与实际执行的秒数无关，首页也按同样的口径显示下次重置时间。
func dailyRef(now time.Time, loc *time.Location) time.Time {
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

func (a *App) dailyJob(ctx context.Context, now time.Time) error {
	local := now.In(a.Loc)
	if err := a.Q.ResetNodeBandwidthOnDay(ctx, int32(local.Day())); err != nil { //nolint:gosec // 1～31
		return err
	}
	cut := now.Unix() - 86400*3
	for _, clean := range []func(context.Context, int64) error{
		a.Q.CleanNodeInfo, a.Q.CleanNodeOnlineLog, a.Q.CleanTrafficLog, a.Q.CleanDetectLog, a.Q.CleanSpeedtest, a.Q.CleanEmailVerify,
	} {
		if err := clean(ctx, cut); err != nil {
			return err
		}
	}
	if err := a.cleanTrafficBatches(ctx, now); err != nil {
		return err
	}
	a.tg("姐姐姐姐，数据库被清理了，感觉身体被掏空了呢~", false)

	// 套餐中的“每 N 天重置流量”。
	boughts, err := a.Q.ListAllBought(ctx)
	if err != nil {
		return err
	}
	shops := map[uint64]*store.Shop{}
	resetUsers := map[uint32]bool{}
	for _, b := range boughts {
		b := b
		if err := safely(func() error {
			u, err := a.Q.GetUser(ctx, b.Userid)
			if isNoRows(err) {
				return a.Q.DeleteBought(ctx, b.ID)
			} else if err != nil {
				return err
			}
			s, ok := shops[b.Shopid]
			if !ok {
				got, err := a.Q.GetShop(ctx, b.Shopid)
				if isNoRows(err) {
					return a.Q.DeleteBought(ctx, b.ID)
				} else if err != nil {
					return err
				}
				s = &got
				shops[b.Shopid] = s
			}
			sm := model.NewShop(*s)
			reset, value, exp := sm.Num("reset"), sm.Num("reset_value"), sm.Num("reset_exp")
			if reset == 0 || value == 0 || exp == 0 {
				return nil
			}
			resetUsers[u.ID] = true
			ref := dailyRef(now, a.Loc).Unix()
			days := (ref - b.Datetime) / 86400
			if float64(ref)-exp*86400 < float64(b.Datetime) && days%int64(reset) == 0 && days != 0 {
				if err := a.Q.ResetUserTraffic(ctx, store.ResetUserTrafficParams{TransferEnable: util.GBToBytes(value), ID: u.ID}); err != nil {
					return err
				}
				a.warnMail(ctx, u, a.Cfg.App.Name+"-您的流量被重置了", "您好，根据您所订购的订单 ID:"+strconv.FormatUint(b.ID, 10)+"，流量已经被重置为"+sm.ResetValue()+"GB")
			}
			return nil
		}); err != nil {
			a.Log.Error("套餐流量重置失败", "bought", b.ID, "err", err)
		}
	}

	// 每日结算与按日重置流量。
	if err := a.Q.SnapshotLastDayTraffic(ctx); err != nil {
		return err
	}
	users, err := a.Q.ListUsersAutoResetOnDay(ctx, int32(local.Day())) //nolint:gosec // 1～31
	if err != nil {
		return err
	}
	for _, u := range users {
		if resetUsers[u.ID] {
			continue
		}
		u := u
		if err := safely(func() error {
			if err := a.Q.ResetUserTraffic(ctx, store.ResetUserTrafficParams{TransferEnable: util.GBToBytes(u.AutoResetBandwidth.Float()), ID: u.ID}); err != nil {
				return err
			}
			a.warnMail(ctx, u, a.Cfg.App.Name+"-您的流量被重置了", "您好，根据管理员的设置，流量已经被重置为"+util.PHPFloat(u.AutoResetBandwidth.Float())+"GB")
			return nil
		}); err != nil {
			a.Log.Error("按日重置流量失败", "user", u.ID, "err", err)
		}
	}
	// 原版此处还会从已停止服务的镜像下载纯真 IP 库、更新 SSR 下载目录，新版不再执行（见部署文档）。
	return nil
}

// ---- sendDiaryMail（原版 DailyMail::sendDailyMail）----

func (a *App) sendDailyMail(ctx context.Context, now time.Time) error {
	anns, err := a.Q.ListAnnouncements(ctx)
	if err != nil {
		return err
	}
	sort.Slice(anns, func(i, j int) bool { return anns[i].ID > anns[j].ID })
	var text1 strings.Builder
	for _, ann := range anns {
		if !strings.Contains(ann.Content, "Links") {
			text1.WriteString(ann.Content + "<br><br>")
		}
	}
	users, err := a.Q.ListDailyMailUsers(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		lastday := float64(u.U+u.D-u.LastDayT) / 1024 / 1024
		text := "下面是系统中目前的公告:<br><br>" + text1.String() + "<br><br>晚安！"
		if err := a.Mail.Send(ctx, u.Email, a.Cfg.App.Name+"-每日流量报告以及公告", "daily_traffic_report", map[string]any{
			"user": model.NewUser(u, a.Loc), "text": htmlText(text), "lastday": util.PHPFloat(lastday),
		}); err != nil {
			a.Log.Warn("发送每日流量报告失败", "to", u.Email, "err", err)
		}
	}
	y, m, d := now.In(a.Loc).Date()
	checkin, err := a.Q.CountTodayCheckin(ctx, time.Date(y, m, d, 0, 0, 0, 0, a.Loc).Unix())
	if err != nil {
		return err
	}
	total, err := a.Q.SumTodayTraffic(ctx)
	if err != nil {
		return err
	}
	a.tg("各位老爷少奶奶，我来为大家报告一下系统今天的运行状况哈~\n今日签到人数:"+strconv.FormatInt(checkin, 10)+
		"\n今日使用总流量:"+util.FlowAutoShow(float64(total))+"\n晚安~", false)
	return nil
}

// ---- 财务报表（原版 FinanceMail）----

// financeMail 统计充值码收入并发给管理员。原版按数据库 NOW() 统计，数据库时区与 PHP 不一致时会错开（K09），
// 这里按业务时区计算统计区间：日报为昨天，周报为过去 7 天（不含今天），月报为上个月。
func (a *App) financeMail(ctx context.Context, now time.Time, kind string) error {
	local := now.In(a.Loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, a.Loc)
	var from, to time.Time
	var title, period, tgTail string
	switch kind {
	case "day":
		from, to, title, period, tgTail = today.AddDate(0, 0, -1), today, "财务日报", "昨日", "凌晨也在努力工作~"
	case "week":
		from, to, title, period, tgTail = today.AddDate(0, 0, -7), today, "财务周报", "上周", "周末也在努力工作~"
	default:
		first := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, a.Loc)
		from, to, title, period, tgTail = first.AddDate(0, -1, 0), first, "财务月报", "上月", "月初也在努力工作~"
	}
	var codes []store.Code
	var err error
	if kind == "week" { // 原版周报不限定充值类型
		codes, err = a.Q.ListUsedCodesAnyTypeBetween(ctx, store.ListUsedCodesAnyTypeBetweenParams{Usedatetime: from.UTC(), Usedatetime_2: to.UTC()})
	} else {
		codes, err = a.Q.ListUsedCodesBetween(ctx, store.ListUsedCodesBetweenParams{Usedatetime: from.UTC(), Usedatetime_2: to.UTC()})
	}
	if err != nil {
		return err
	}
	var html strings.Builder
	var total float64
	if kind == "day" {
		html.WriteString("<table border=1><tr><td>金额</td><td>用户ID</td><td>用户名</td><td>充值时间</td>")
		for _, c := range codes {
			name := ""
			if u, err := a.Q.GetUser(ctx, c.Userid); err == nil {
				name = u.UserName
			}
			fmt.Fprintf(&html, "<tr><td>%s</td><td>%d</td><td>%s</td><td>%s</td></tr>", c.Number, c.Userid, escHTML(name), c.Usedatetime.In(a.Loc).Format("2006-01-02 15:04:05"))
		}
		html.WriteString("</table>")
	}
	for _, c := range codes {
		total += c.Number.Float()
	}
	fmt.Fprintf(&html, "<br>%s总收入笔数：%d<br>%s总收入金额：%s", period, len(codes), period, util.PHPFloat(total))
	admins, err := a.Q.ListAdmins(ctx)
	if err != nil {
		return err
	}
	for _, ad := range admins {
		if err := a.Mail.Send(ctx, ad.Email, a.Cfg.App.Name+"-"+title, "finance", map[string]any{"user": ad, "title": title, "text": htmlText(html.String())}); err != nil {
			a.Log.Warn("发送财务报表失败", "to", ad.Email, "err", err)
		}
	}
	if a.Cfg.Telegram.FinancePublic {
		a.tg("新鲜出炉的"+title+"~\n"+period+"总收入笔数:"+strconv.Itoa(len(codes))+"\n"+period+"总收入金额:"+util.PHPFloat(total)+"\n"+tgTail, false)
	}
	return nil
}

// ---- syncnode（原版 Job::syncnode）----

// syncNode 解析节点域名并更新节点主 IP（节点 IP 第一项），同时更新以该节点为终点的中转规则。
// 手动添加的节点 IP 项（出口 IP、#源节点ID）保持不变，规则见 nodeip.go。
func (a *App) syncNode(ctx context.Context, _ time.Time) error {
	nodes, err := a.Q.ListNodes(ctx)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if !nodeIPSorts[n.Sort] {
			continue
		}
		n := n
		if err := safely(func() error {
			host := nodeHost(n.Server)
			if net.ParseIP(host) == nil && host != "" {
				// 原版解析失败时会把域名本身写入 node_ip，导致节点认证失败；这里解析失败时保持原值。
				if ip := gethostbyname(ctx, host); ip != host && net.ParseIP(ip) != nil && ip != nodeIPPrimary(n.NodeIp) {
					n.NodeIp = nodeIPWithPrimary(n.NodeIp, ip)
					if err := a.Q.UpdateNodeIP(ctx, store.UpdateNodeIPParams{NodeIp: n.NodeIp, ID: n.ID}); err != nil {
						return err
					}
				}
			}
			if n.Sort == 0 || n.Sort == 10 || n.Sort == 12 {
				rules, err := a.Q.ListRelayByDist(ctx, int32(n.ID)) //nolint:gosec // 节点 ID 范围很小
				if err != nil {
					return err
				}
				for _, rule := range rules {
					// 只更新目标 IP 已不在节点 IP 中的规则（节点 IP 变化后）。目标仍有效的规则不改，
					// 迁移来的旧规则不会因为新旧默认目标规则不同而被静默改变（差异见 xcat checkNodeIP）。
					if nodeIPHas(n.NodeIp, rule.DistIp) {
						continue
					}
					src, _ := a.Q.GetNode(ctx, uint32(max(rule.SourceNodeID, 0))) //nolint:gosec // 已保证非负
					if ip := relayDistIP(src, n.NodeIp); ip != rule.DistIp {
						if err := a.Q.UpdateRelayDistIP(ctx, store.UpdateRelayDistIPParams{DistIp: ip, ID: rule.ID}); err != nil {
							return err
						}
					}
				}
			}
			return nil
		}); err != nil {
			a.Log.Error("同步节点 IP 失败", "node", n.ID, "err", err)
		}
	}
	return nil
}
