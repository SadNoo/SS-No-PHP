package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/blockedip"
	"github.com/SadNoo/SS-No-PHP/internal/dec"
	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/notify"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

func htmlText(s string) template.HTML { return template.HTML(s) } //nolint:gosec // 程序生成的邮件内容

func escHTML(s string) string { return template.HTMLEscapeString(s) }

// ---- checkjob（原版 Job::CheckJob，每分钟执行）----

func (a *App) checkJob(ctx context.Context, now time.Time) error {
	for _, step := range []struct {
		name string
		fn   func(context.Context, time.Time) error
	}{
		// 先解除到期的限制再检查：解除后仍然超出的 IP 在同一次检查中立即重新限制，不会留出一两分钟的空档（原版顺序相反）。
		{"解除临时限制", a.releaseDisconnects},
		{"在线设备数限制", a.checkConnector},
		{"自动续费", a.autoRenew},
		{"清理过期记录", a.cleanShortLived},
		{"节点掉线检测", a.detectOffline},
		{"异地登录检测", a.loginWarn},
		{"用户状态检查", a.checkUsers},
		{"Radius 封禁维护", a.radiusBanCheck},
	} {
		if err := safely(func() error { return step.fn(ctx, now) }); err != nil {
			a.Log.Error("每分钟检查出错", "step", step.name, "err", err)
		}
	}
	return nil
}

// checkConnector 限制同时在线的 IP 数：最近 60 秒在线的 IP 超过用户的 node_connector 时，
// 保留先上线的 IP，超出的（后加入的）暂时写入用户的 disconnect_ip，5 分钟后解除。
// 先后按本次连续在线的开始时间判断（ip_online 表，见 updateIPOnline），持续在线多久都不会被后加入的 IP 挤掉；
// 离线超过 10 分钟后再出现视为重新上线。同一秒开始的按 IP 排序。
// IP 统一为标准写法后比较（IPv6 的不同写法、IPv4 映射地址视为同一个 IP）；节点自己的 IP（面板中转）不计入。
// 原版判断记录是否存在时把 IP 与记录 ID 比较，导致每分钟重复写入（K42）；
// 原版按 IP 字符串顺序挑选超出的 IP，可能断开早已在线的设备而放行新设备（K51）。
// 在线 IP 由旧接口 /mod_mu/users/aliveip 与 v2 接口 /mod_mu/v2/nodes/{id}/alive_ip 上报，两者一起计算。
func (a *App) checkConnector(ctx context.Context, now time.Time) error {
	users, err := a.Q.ListUsersWithConnector(ctx, now.Unix()-60)
	if err != nil || len(users) == 0 {
		return err
	}
	if h := a.disconnectHooks.afterConnectorUsers; h != nil {
		h()
	}
	if err := a.updateIPOnline(ctx, now); err != nil {
		return err
	}
	alive, err := a.Q.ListIPOnlineSince(ctx, now.Unix()-60)
	if err != nil {
		return err
	}
	nodes, err := a.Q.ListNodeIPs(ctx)
	if err != nil {
		return err
	}
	nodeIP := map[string]bool{}
	for _, list := range nodes {
		// 节点 IP 可以是逗号分隔的多个地址，可带 #源节点ID（见 nodeip.go）；逐个拆开并统一写法。
		for _, e := range nodeIPEntries(list) {
			addr, _, _ := strings.Cut(e, "#")
			if ip, ok := normalizeIP(addr); ok {
				nodeIP[ip] = true
			}
		}
	}
	byUser := map[uint32][]string{}
	for _, x := range alive {
		if !nodeIP[x.Ip] {
			byUser[x.Userid] = append(byUser[x.Userid], x.Ip)
		}
	}
	// 每个用户一个事务；某个用户失败时记录错误并继续处理其他用户，最后一并返回。
	var errs []error
	for _, u := range users {
		ips := byUser[u.ID]
		if len(ips) <= int(u.NodeConnector) { // 在线 IP 没有超过限制（上面读到的数据只用来筛选，锁内重新读取）
			continue
		}
		if err := a.disconnectTx(ctx, func(q *store.Queries) error { return limitUserIPs(ctx, q, u.ID, ips, now) }); err != nil {
			errs = append(errs, fmt.Errorf("用户 %d: %w", u.ID, err))
			if ctx.Err() != nil {
				break
			}
		}
	}
	return errors.Join(errs...)
}

// limitUserIPs 在事务中处理一个用户的超限 IP：锁住用户行，读取最新的 disconnect_ip 与限制数，加锁读取该用户的
// 限制记录，按在线先后找出超出的 IP，新增记录并把它们追加到字段末尾。字段在锁内读取，不会用之前读到的旧值覆盖
// 期间被修复命令或到期解除改动过的内容（第三方 P2）。字段中的非法项（历史数据）原样保留、不在这里删除：
// 节点接口不会下发它们，清理用 panel xcat repairDisconnectIP（见 blockedip 包的规则）。
func limitUserIPs(ctx context.Context, q *store.Queries, uid uint32, ips []string, now time.Time) error {
	cur, err := q.LockUserDisconnect(ctx, uid)
	if isNoRows(err) || (err == nil && cur.NodeConnector <= 0) {
		return nil // 用户已删除，或期间改为不限制
	} else if err != nil {
		return err
	}
	records, err := q.ListUserDisconnectRecordsForShare(ctx, uid)
	if err != nil {
		return err
	}
	recorded := map[string]bool{}
	for _, ip := range records {
		recorded[blockedip.RecordKey(ip)] = true
	}
	disconnected := blockedip.Split(cur.DisconnectIp).Valid // 已限制的 IP（标准写法）
	seen := map[string]bool{}
	list := cur.DisconnectIp
	for _, ip := range ips {
		if seen[ip] || inList(disconnected, ip) {
			continue
		}
		seen[ip] = true
		if int32(len(seen)) <= cur.NodeConnector || recorded[ip] { //nolint:gosec // 在线 IP 数很小
			continue
		}
		if err := q.InsertDisconnect(ctx, store.InsertDisconnectParams{Userid: uid, Ip: ip, Datetime: now.Unix()}); err != nil {
			return err
		}
		recorded[ip] = true
		if list == "" {
			list = ip
		} else {
			list += "," + ip
		}
	}
	if list == cur.DisconnectIp {
		return nil
	}
	return q.UpdateUserDisconnectIP(ctx, store.UpdateUserDisconnectIPParams{DisconnectIp: list, ID: uid})
}

// aliveIPRetention 是在线 IP 上报记录（alive_ip）的保留时间（秒），见 cleanShortLived。
// 连续在线的开始时间单独保存在 ip_online 中，不依赖上报记录的保留时间。
const aliveIPRetention = 300

// ipOnlineGap 是视为离线的时间（秒）：超过这个时间没有上报的 IP 删除在线状态，再次出现时视为重新上线。
// 必须长于超限 IP 的限制时间（5 分钟），被限制的设备解除后重新连接时仍排在原来的位置。
const ipOnlineGap = 600

type aliveKey struct {
	user uint32
	ip   string
}

// updateIPOnline 根据最近 5 分钟的上报更新 ip_online：最近 60 秒有上报的（用户, IP）更新最后上报时间，
// 新出现或离线超过 ipOnlineGap 后再出现的，以本次上报中的最早时间作为开始时间。
func (a *App) updateIPOnline(ctx context.Context, now time.Time) error {
	rows, err := a.Q.ListAliveIPSpans(ctx, now.Unix()-aliveIPRetention)
	if err != nil {
		return err
	}
	type span struct{ first, last int64 }
	spans := map[aliveKey]span{}
	invalid := 0
	for _, x := range rows {
		ip, ok := normalizeIP(x.Ip)
		if !ok {
			// 旧接口原样保存节点上报的文本，可能不是 IP（包括数据库中已有的记录）：跳过，不计入统计，
			// 也不影响其他记录与其他用户的检查。
			invalid++
			continue
		}
		k := aliveKey{x.Userid, ip}
		s, ok := spans[k]
		if !ok || x.FirstSeen < s.first {
			s.first = x.FirstSeen
		}
		s.last = max(s.last, x.LastSeen)
		spans[k] = s
	}
	if invalid > 0 {
		a.Log.Warn("在线 IP 记录中有无效的 IP，已跳过", "count", invalid)
	}
	return a.tx(ctx, func(q *store.Queries) error {
		for k, s := range spans {
			if s.last < now.Unix()-60 {
				continue
			}
			if err := q.UpsertIPOnline(ctx, store.UpsertIPOnlineParams{Userid: k.user, Ip: k.ip, FirstSeen: s.first, LastSeen: s.last, ResetBefore: now.Unix() - ipOnlineGap}); err != nil {
				return err
			}
		}
		return q.CleanIPOnline(ctx, now.Unix()-ipOnlineGap)
	})
}

// normalizeIP 把 IP 统一为标准写法：IPv6 使用压缩小写形式，IPv4 映射地址（::ffff:1.2.3.4）转为 IPv4。
// 不是有效的 IP（含空白、非 ASCII、带区域的 IPv6 等）时 ok 为 false，调用方必须跳过，不能把原文当作 IP 使用。
// 标准写法只含 ASCII，长度不超过 39。
func normalizeIP(s string) (ip string, ok bool) {
	parsed := net.ParseIP(strings.TrimSpace(s))
	if parsed == nil {
		return "", false
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String(), true
	}
	return parsed.String(), true
}

func inList(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// releaseDisconnects 解除 5 分钟前的临时限制：从 user.disconnect_ip 中去掉与 disconnect_ip 表记录相同的 IP
// （按标准写法比较，同一 IP 的不同写法一并去掉），其余项（包括非法项与没有对应记录的项）原样保留。
// 没有对应记录的项不会自动解除，由 panel xcat checkDisconnectIP / repairDisconnectIP 检查与处理。
func (a *App) releaseDisconnects(ctx context.Context, now time.Time) error {
	list, err := a.Q.ListDisconnectsBefore(ctx, now.Unix()-300)
	if err != nil {
		return err
	}
	// 每条记录一个事务；某条失败时记录错误并继续处理其他记录（下一分钟会再次处理），最后一并返回。
	var errs []error
	for _, d := range list {
		if err := a.disconnectTx(ctx, func(q *store.Queries) error { return a.releaseOne(ctx, q, d) }); err != nil {
			errs = append(errs, fmt.Errorf("记录 %d: %w", d.ID, err))
			if ctx.Err() != nil {
				break
			}
		}
	}
	return errors.Join(errs...)
}

// releaseOne 在事务中解除一条记录：先锁住用户行并读取最新字段，再锁住这条记录确认仍然存在（已被其他操作删除时跳过），
// 去掉字段中与它相同的 IP 后删除记录。字段在锁内读取，不会用旧值覆盖期间新增的限制。
func (a *App) releaseOne(ctx context.Context, q *store.Queries, d store.DisconnectIp) error {
	cur, err := q.GetUserDisconnectIPForUpdate(ctx, d.Userid)
	exists := err == nil
	if err != nil && !isNoRows(err) {
		return err
	}
	if _, err := q.GetDisconnectForUpdate(ctx, d.ID); isNoRows(err) {
		return nil
	} else if err != nil {
		return err
	}
	if h := a.disconnectHooks.releaseLocked; h != nil {
		h()
	}
	if exists {
		release, _ := blockedip.Parse(d.Ip)
		var keep []string
		for _, ip := range strings.Split(cur, ",") {
			if ip == "" || ip == d.Ip {
				continue
			}
			if n, ok := blockedip.Parse(ip); ok && n == release {
				continue
			}
			keep = append(keep, ip)
		}
		if v := strings.Join(keep, ","); v != cur {
			if err := q.UpdateUserDisconnectIP(ctx, store.UpdateUserDisconnectIPParams{DisconnectIp: v, ID: d.Userid}); err != nil {
				return err
			}
		}
	}
	return q.DeleteDisconnect(ctx, d.ID)
}

// autoRenew 自动续费：余额足够时扣款并按续费规则生效，否则发一次提醒邮件。没有开启自动续费（enable_auto_renew）时不执行。
func (a *App) autoRenew(ctx context.Context, now time.Time) error {
	if !a.Cfg.Users.EnableAutoRenew {
		return nil
	}
	list, err := a.Q.ListRenewDue(ctx, now.Unix())
	if err != nil {
		return err
	}
	for _, b := range list {
		b := b
		if err := safely(func() error { return a.renewOne(ctx, b, now) }); err != nil {
			a.Log.Error("自动续费失败", "bought", b.ID, "err", err)
		}
	}
	return nil
}

// renewOutcome 是一张到期订单的处理结果。
type renewOutcome int

const (
	renewSkipped  renewOutcome = iota // 不处理：已关闭续费、尚未到期，或被更新的订单替代（替代时关闭它的续费）
	renewNoMoney                      // 余额不足：回滚，订单保持开启，充值后下次检查再续费
	renewShopGone                     // 商品已删除：关闭续费、保留订单
	renewDone                         // 已扣款续费
)

// errRenewNoMoney 用于回滚余额不足的续费事务。
var errRenewNoMoney = errors.New("余额不足")

// renewOne 续费一张到期订单。每个帐号只有最新的一张订单（不论是否开启续费、是否到期）可以续费：
// 购买、后台添加套餐、续费都会生成新订单并关闭其他订单的续费，只有旧面板导入的数据会有多个开启续费的订单。
// 全部判断都在锁住用户后的同一个事务中进行，通知在事务结束后发送：
//   - 较旧的订单：不续费、不扣款、不提醒，关闭它的续费（避免最新订单余额不足、尚未到期或被用户关闭续费时，
//     改为续费较旧的套餐、把等级改回旧套餐）；
//   - 已关闭续费或尚未到期（列出订单后用户关闭了续费等）：不处理、不提醒；
//   - 商品已删除：关闭续费、保留订单（删除订单会让较旧的订单变成最新而接着续费），提醒一次；
//   - 余额不足：回滚，提醒一次（之后不重复提醒，直到续费成功或订单不再需要续费）。
func (a *App) renewOne(ctx context.Context, b store.Bought, now time.Time) error {
	u, err := a.Q.GetUser(ctx, b.Userid)
	if isNoRows(err) {
		return a.Q.DeleteBought(ctx, b.ID)
	} else if err != nil {
		return err
	}
	flag := "renew:bought:" + strconv.FormatUint(b.ID, 10)
	outcome := renewSkipped
	var s store.Shop
	err = a.tx(ctx, func(q *store.Queries) error {
		outcome = renewSkipped
		cur, err := q.GetUserForUpdate(ctx, u.ID)
		if err != nil {
			return err
		}
		// 新订单都在锁住用户后写入，这里读到的最新订单在本事务内不会改变。
		latest, err := q.GetLatestBoughtID(ctx, u.ID)
		if err != nil {
			return err
		}
		// 确认订单仍开启自动续费且已到期并关闭它（之后由新订单接续）；用户关闭续费只把 renew 改为 0，这里会看到。
		n, err := q.ClaimBoughtRenew(ctx, store.ClaimBoughtRenewParams{ID: b.ID, Renew: now.Unix()})
		if err != nil || n == 0 || latest != b.ID {
			return err // 较旧的订单：续费已在上面关闭，提交
		}
		s, err = q.GetShop(ctx, b.Shopid)
		if isNoRows(err) {
			outcome = renewShopGone
			return nil
		} else if err != nil {
			return err
		}
		if cur.Money < s.Price {
			outcome = renewNoMoney
			return errRenewNoMoney
		}
		if _, err := q.DeductUserMoney(ctx, store.DeductUserMoneyParams{Amount: s.Price, ID: u.ID}); err != nil {
			return err
		}
		cur.Money -= s.Price
		shopModel(s).Apply(&cur, true, a.Cfg.Users.EnableBoughtReset, a.Cfg.Users.EnableBoughtExtend, now)
		if err := q.UpdateUserAfterBuy(ctx, afterBuyParams(cur)); err != nil {
			return err
		}
		// 每个帐号只有最新的套餐自动续费：关闭其他订单（旧数据可能有多个）的自动续费，由新订单接续。
		if err := q.DisableUserRenew(ctx, u.ID); err != nil {
			return err
		}
		if err := q.InsertBought(ctx, store.InsertBoughtParams{
			Userid: u.ID, Shopid: s.ID, Datetime: now.Unix(), Renew: now.Unix() + int64(s.AutoRenew)*86400, Coupon: "", Price: s.Price,
		}); err != nil {
			return err
		}
		outcome = renewDone
		return nil
	})
	if err != nil && !errors.Is(err, errRenewNoMoney) {
		return err
	}
	switch outcome {
	case renewDone:
		a.warnMail(ctx, u, a.Cfg.App.Name+"-续费成功", "您好，系统已经为您自动续费，商品名："+s.Name+",金额:"+util.PHPFloat(s.Price.Float())+" 元。")
	case renewShopGone:
		a.warnMail(ctx, u, a.Cfg.App.Name+"-续费失败", "您好，系统为您自动续费商品时，发现该商品已被下架，为能继续正常使用，建议您登录用户面板购买新的商品。")
	case renewNoMoney:
		if a.flag(ctx, flag) {
			return nil
		}
		a.warnMail(ctx, u, a.Cfg.App.Name+"-续费失败", "您好，系统为您自动续费商品名："+s.Name+",金额:"+util.PHPFloat(s.Price.Float())+" 元 时，发现您余额不足，请及时充值。充值后请稍等系统便会自动为您续费。")
		return a.setFlag(ctx, flag)
	}
	return a.clearFlag(ctx, flag)
}

// cleanShortLived 清理短期记录：在线 IP、解封记录、封禁记录、Telegram 登录会话。
func (a *App) cleanShortLived(ctx context.Context, now time.Time) error {
	for _, c := range []struct {
		fn  func(context.Context, int64) error
		ago int64
	}{
		{a.Q.CleanAliveIP, aliveIPRetention}, {a.Q.CleanUnblockIP, 300}, {a.Q.CleanBlockIP, 86400}, {a.Q.CleanTelegramSession, 900},
	} {
		if err := c.fn(ctx, now.Unix()-c.ago); err != nil {
			return err
		}
	}
	return nil
}

// nodeOnlineState 对应 Node::isNodeOnline：从未上报为离线（false）；非 SS 类节点无法判断（第二个返回值为 false）。
func nodeOnlineState(n store.SsNode, now time.Time) (online, known bool) {
	if n.NodeHeartbeat == 0 {
		return false, true
	}
	switch n.Sort {
	case 0, 7, 8, 10, 11, 12, 13:
	default:
		return false, false
	}
	return n.NodeHeartbeat > now.Unix()-300, true
}

// adminNotice 通知全部管理员（邮件）、Server 酱（可选）与 Telegram 群。
func (a *App) adminNotice(ctx context.Context, subject, mailText, scTitle, tgText string, useSc bool) {
	if useSc && a.Cfg.Contact.UseScFtqq {
		if err := (notify.ScFtqq{Key: a.Cfg.Contact.ScFtqqSCKEY}).Send(ctx, scTitle, mailText); err != nil {
			a.Log.Warn("Server 酱推送失败", "err", err)
		}
	}
	if admins, err := a.Q.ListAdmins(ctx); err == nil {
		for _, ad := range admins {
			a.warnMail(ctx, ad, subject, mailText)
		}
	}
	a.tg(tgText, false)
}

// detectOffline 节点掉线与恢复通知（原版 enable_detect_offline）。原版的 CloudXNS 自动切换解析已无法使用（服务已关闭），未移植。
func (a *App) detectOffline(ctx context.Context, now time.Time) error {
	if !a.Cfg.Detect.EnableDetectOffline {
		return nil
	}
	nodes, err := a.Q.ListNodes(ctx)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		online, known := nodeOnlineState(n, now)
		if !known {
			continue
		}
		flag := "offline:node:" + strconv.FormatUint(uint64(n.ID), 10)
		flagged := a.flag(ctx, flag)
		switch {
		case !online && !flagged:
			text := "管理员您好，系统发现节点 " + n.Name + " 掉线了，请您及时处理。"
			a.adminNotice(ctx, a.Cfg.App.Name+"-系统警告", text, a.Cfg.App.Name+"-节点掉线了", "喵喵喵~ "+n.Name+" 节点掉线了喵~", a.Cfg.Detect.DetectOfflineUseScFtqq)
			if err := a.setFlag(ctx, flag); err != nil {
				return err
			}
		case online && flagged:
			text := "管理员您好，系统发现节点 " + n.Name + " 恢复上线了。"
			a.adminNotice(ctx, a.Cfg.App.Name+"-系统提示", text, a.Cfg.App.Name+"-节点恢复上线了", "喵喵喵~ "+n.Name+" 节点恢复了喵~", a.Cfg.Detect.DetectOfflineUseScFtqq)
			if err := a.clearFlag(ctx, flag); err != nil {
				return err
			}
		}
	}
	return nil
}

// loginWarn 异地登录提醒（原版 login_warn）：最近一次与上一次登录的国家不同且都不是节点 IP 时提醒用户。
func (a *App) loginWarn(ctx context.Context, now time.Time) error {
	if !a.Cfg.Display.LoginWarn || !a.Geo.Loaded() {
		return nil
	}
	logs, err := a.Q.ListRecentLogins(ctx, now.Unix()-60)
	if err != nil {
		return err
	}
	nodes, err := a.Q.ListNodes(ctx)
	if err != nil {
		return err
	}
	isNode := func(ip string) bool {
		for _, n := range nodes {
			if nodeIPMatches(n.NodeIp, ip) {
				return true
			}
		}
		return false
	}
	for _, l := range logs {
		two, err := a.Q.ListLastTwoLogins(ctx, l.Userid)
		if err != nil || len(two) != 2 {
			continue
		}
		cur, prev := a.Geo.Country(two[0].Ip), a.Geo.Country(two[1].Ip)
		if cur != prev && !isNode(two[0].Ip) && !isNode(two[1].Ip) {
			if u, err := a.Q.GetUser(ctx, l.Userid); err == nil {
				a.warnMail(ctx, u, a.Cfg.App.Name+"-系统警告", "您好，系统发现您的账号在 "+cur+" 有异常登录，请您自己自行核实登录行为。有异常请及时修改密码。")
			}
		}
	}
	return nil
}

// checkUsers 逐个检查用户：账户过期、流量不足提醒、过期删除、长期未签到 / 未使用清理、等级过期。
func (a *App) checkUsers(ctx context.Context, now time.Time) error {
	users, err := a.Q.ListAllUsers(ctx)
	if err != nil {
		return err
	}
	// 用户标记（已发过期 / 流量不足提醒）一次读出；原来每个用户每分钟各查一次，用户多时每分钟数千条查询。
	flags := map[string]bool{}
	for _, prefix := range []string{"expire:user:", "traffic:user:"} {
		names, err := a.Q.ListJobStateNamesByPrefix(ctx, prefix)
		if err != nil {
			return err
		}
		for _, name := range names {
			flags[name] = true
		}
	}
	for _, u := range users {
		u := u
		if err := safely(func() error { return a.checkUser(ctx, u, now, flags) }); err != nil {
			a.Log.Error("检查用户失败", "user", u.ID, "err", err)
		}
	}
	return nil
}

// checkUser 检查单个用户（原版 CheckJob 中逐个用户处理的部分）。flags 为本轮开始时读出的用户标记。
func (a *App) checkUser(ctx context.Context, u store.User, now time.Time, flags map[string]bool) error {
	id := strconv.FormatUint(uint64(u.ID), 10)
	name := a.Cfg.App.Name
	orig := u

	expireFlag := "expire:user:" + id
	switch {
	case u.ExpireIn.Before(now) && !flags[expireFlag]:
		u.TransferEnable, u.U, u.D, u.LastDayT = 0, 0, 0, 0
		a.warnMail(ctx, u, name+"-您的用户账户已经过期了", "您好，系统发现您的账号已经过期了。")
		if err := a.setFlag(ctx, expireFlag); err != nil {
			return err
		}
	case u.ExpireIn.After(now) && flags[expireFlag]:
		if err := a.clearFlag(ctx, expireFlag); err != nil {
			return err
		}
	}

	if mode := a.Cfg.Users.NotifyLimitMode; mode == "per" || mode == "mb" {
		left := u.TransferEnable - u.U - u.D
		under, unit := false, ""
		if u.TransferEnable != 0 && mode == "per" && float64(left)/float64(u.TransferEnable)*100 < a.Cfg.Users.NotifyLimitValue {
			under, unit = true, "%"
		} else if mode == "mb" && util.BytesToMB(float64(left)) < a.Cfg.Users.NotifyLimitValue {
			under, unit = true, "MB"
		}
		flag := "traffic:user:" + id
		switch {
		case under && !flags[flag]:
			a.warnMail(ctx, u, name+" - 您的剩余流量过低", "您好，系统发现您剩余流量已经低于 "+util.PHPFloat(a.Cfg.Users.NotifyLimitValue)+unit+" 。")
			if err := a.setFlag(ctx, flag); err != nil {
				return err
			}
		case !under && flags[flag]:
			if err := a.clearFlag(ctx, flag); err != nil {
				return err
			}
		}
	}

	if days := a.Cfg.Users.AccountExpireDeleteDays; days >= 0 && u.ExpireIn.Add(time.Duration(days)*24*time.Hour).Before(now) {
		a.warnMail(ctx, u, name+"-您的用户账户已经被删除了", "您好，系统发现您的账户已经过期 "+strconv.Itoa(days)+" 天了，帐号已经被删除。")
		return a.killUser(ctx, u)
	}
	minMoney := dec.FromFloat(a.Cfg.Users.AutoCleanMinMoney)
	if days := a.Cfg.Users.AutoCleanUncheckDays; days > 0 && u.Class == 0 && u.Money <= minMoney &&
		max(u.LastCheckInTime, u.RegDate.Unix())+int64(days)*86400 < now.Unix() {
		a.warnMail(ctx, u, name+"-您的用户账户已经被删除了", "您好，系统发现您的账号已经 "+strconv.Itoa(days)+" 天没签到了，帐号已经被删除。")
		return a.killUser(ctx, u)
	}
	if days := a.Cfg.Users.AutoCleanUnusedDays; days > 0 && u.Class == 0 && u.Money <= minMoney &&
		max(u.T, u.RegDate.Unix())+int64(days)*86400 < now.Unix() {
		a.warnMail(ctx, u, name+"-您的用户账户已经被删除了", "您好，系统发现您的账号已经 "+strconv.Itoa(days)+" 天没使用了，帐号已经被删除。")
		return a.killUser(ctx, u)
	}

	// 等级过期：1420041600 为 2015-01-01，等级到期时间早于此视为“未设置”（如 1989-06-04）。
	if u.Class != 0 && u.ClassExpire.Before(now) && u.ClassExpire.Unix() > 1420041600 {
		text := "您好，系统发现您的账号等级已经过期了。"
		if reset := a.Cfg.Users.ClassExpireResetTraffic; reset >= 0 {
			u.TransferEnable, u.U, u.D, u.LastDayT = util.GBToBytes(reset), 0, 0, 0
			text += "流量已经被重置为" + util.PHPFloat(reset) + "GB"
		}
		u.Class = 0
		a.warnMail(ctx, u, name+"-您的账户等级已经过期了", text)
	}

	if u.TransferEnable != orig.TransferEnable || u.U != orig.U || u.D != orig.D || u.LastDayT != orig.LastDayT || u.Class != orig.Class {
		return a.Q.SaveUserJobFields(ctx, store.SaveUserJobFieldsParams{
			TransferEnable: u.TransferEnable, U: u.U, D: u.D, LastDayT: u.LastDayT, Class: u.Class, ID: u.ID,
		})
	}
	return nil
}

// ---- detectGFW（原版 Job::detectGFW）----

// detectGFW 通过外部 TCPing 接口检测节点是否被墙。原版用 eval 执行配置中的判断表达式，
// 这里固定为原版默认的判断方式：接口返回 JSON 的 status 字段为 "true"。
func (a *App) detectGFW(ctx context.Context, _ time.Time) error {
	nodes, err := a.Q.ListNodes(ctx)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	probe := func(ip string) bool {
		url := strings.NewReplacer("{ip}", ip, "{port}", strconv.Itoa(a.Cfg.Detect.GFWPort)).Replace(a.Cfg.Detect.GFWURL)
		for i := 0; i < max(a.Cfg.Detect.GFWCount, 1); i++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return true
			}
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
			var res map[string]any
			if json.Unmarshal(body, &res) == nil {
				if v := res["status"]; v == "true" || v == true {
					return true
				}
			}
		}
		return false
	}
	for _, n := range nodes {
		if n.NodeIp == "" || a.flag(ctx, "offline:node:"+strconv.FormatUint(uint64(n.ID), 10)) {
			continue
		}
		ip := strings.Split(strings.Split(n.NodeIp, ",")[0], "#")[0]
		ok := probe(ip)
		flag := "gfw:node:" + strconv.FormatUint(uint64(n.ID), 10)
		flagged := a.flag(ctx, flag)
		switch {
		case !ok && !flagged:
			a.adminNotice(ctx, a.Cfg.App.Name+"-系统警告", "管理员您好，系统发现节点 "+n.Name+" 被墙了，请您及时处理。", "", "喵喵喵~ "+n.Name+" 节点被墙了喵~", false)
			if err := a.setFlag(ctx, flag); err != nil {
				return err
			}
		case ok && flagged:
			a.adminNotice(ctx, a.Cfg.App.Name+"-系统提示", "管理员您好，系统发现节点 "+n.Name+" 溜出墙了。", "", "喵喵喵~ "+n.Name+" 节点恢复了喵~", false)
			if err := a.clearFlag(ctx, flag); err != nil {
				return err
			}
		}
	}
	return nil
}

func shopModel(s store.Shop) *model.Shop { return model.NewShop(s) }
