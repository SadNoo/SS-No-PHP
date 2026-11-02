package app

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/auth"
	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// XCatUsage 是 panel xcat 的帮助信息（与原版 php xcat 的命令名一致，便于迁移）。
const XCatUsage = `用法: panel xcat <命令> [参数]

运维命令:
  createAdmin <邮箱> <密码>   创建管理员帐号
  resetTraffic               重置所有已启用用户的已用流量
  resetPort <用户ID>          重置单个用户端口
  resetAllPort               重置所有用户端口
  cleanRelayRule             清除冲突的中转规则
  userga                     为所有用户重新生成两步验证密钥
  reall                      记录所有用户当前已用流量为“今日之前已用”
  checkClassExpire           列出等级大于 0 却没有等级到期时间的异常用户
  checkNodeIP                列出目标 IP 与当前节点 IP 规则不一致、或新旧规则结果不同的中转规则（只读）
  checkDisconnectIP          检查用户的封禁 IP（disconnect_ip）：非法项、没有对应记录的合法项（只读，不输出 IP 原文）
  repairDisconnectIP [--orphans] [--apply --rollback-file 文件]
                             去掉 disconnect_ip 中的非法项（--orphans 时也去掉没有对应记录的项）；
                             不带 --apply 时只显示影响清单；--apply 先把回滚 SQL 写入新文件并刷盘，成功后才修改
  extendAccounts [--apply] [--include-expired]
                             把帐号有效期统一调整为 100 年（不带 --apply 时只显示影响范围）
  setTelegram                设置 Telegram 机器人 webhook
  sendDailyUsageByTG         向绑定 Telegram 的用户私聊发送流量情况

定时任务（内置调度会自动执行，这里用于手动执行一次）:
  checkjob  dailyjob  sendDiaryMail  syncnode  detectGFW  synclogin  syncvpn
  sendFinanceMail_day  sendFinanceMail_week  sendFinanceMail_month

Radius（radius.enable = true）:
  syncusers                  为所有用户创建 Radius 帐号并邮件通知用户名
  syncnasnode                解析使用域名的节点并加入 NAS

initQQWry、initdownload、updatedownload、update、npmbuild、getCookie、backup、backupfull 在新版中不再需要；
syncnas（重启 radiusd、修改 portsentry）属于服务器运维，见部署文档。`

// XCat 执行运维命令（原版 php xcat）。
func (a *App) XCat(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(out, XCatUsage)
		return nil
	}
	if _, ok := a.jobs()[args[0]]; ok {
		return a.RunJob(ctx, args[0])
	}
	switch args[0] {
	case "createAdmin":
		if len(args) != 3 {
			return fmt.Errorf("用法: panel xcat createAdmin <邮箱> <密码>")
		}
		return a.createAdmin(ctx, args[1], args[2], out)
	case "resetTraffic":
		if err := a.Q.ResetAllEnabledTraffic(ctx); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "reset traffic successful")
		return nil
	case "resetPort":
		if len(args) != 2 {
			return fmt.Errorf("用法: panel xcat resetPort <用户ID>")
		}
		id, err := strconv.ParseUint(args[1], 10, 32)
		if err != nil {
			return fmt.Errorf("用户 ID 无效: %s", args[1])
		}
		port, err := a.resetUserPort(ctx, uint32(id), true)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "重置成功! 新端口 %d\n", port)
		return nil
	case "resetAllPort":
		ids, err := a.Q.ListUserIDs(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			// 原版重置全部端口时不同步中转规则端口，这里与单个重置一致地同步。
			port, err := a.resetUserPort(ctx, id, true)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "%d -> %d\n", id, port)
		}
		return nil
	case "cleanRelayRule":
		return a.cleanRelayRules(ctx, out)
	case "userga":
		ids, err := a.Q.ListUserIDs(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := a.Q.UpdateUserGaToken(ctx, store.UpdateUserGaTokenParams{GaToken: auth.NewTOTPSecret(), ID: id}); err != nil {
				return err
			}
		}
		_, _ = fmt.Fprintln(out, "ok")
		return nil
	case "reall":
		return a.Q.SnapshotLastDayTraffic(ctx)
	case "checkClassExpire":
		return a.checkClassExpire(ctx, out)
	case "checkNodeIP":
		return a.checkNodeIP(ctx, out)
	case "checkDisconnectIP":
		return a.checkDisconnectIP(ctx, out)
	case "repairDisconnectIP":
		return a.repairDisconnectIP(ctx, args[1:], out)
	case "extendAccounts":
		return a.extendAccounts(ctx, args[1:], out)
	case "setTelegram":
		if err := a.setTelegramWebhook(ctx); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "设置成功！")
		return nil
	case "syncusers":
		return a.radiusSyncUsers(ctx, out)
	case "syncnasnode":
		return a.radiusSyncNasNode(ctx)
	case "sendDailyUsageByTG":
		return a.sendDailyUsageByTG(ctx)
	default:
		return fmt.Errorf("未知命令 %q\n\n%s", args[0], XCatUsage)
	}
}

// createAdmin 对应 XCat::createAdmin。
func (a *App) createAdmin(ctx context.Context, email, password string, out io.Writer) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return fmt.Errorf("邮箱无效")
	}
	if len(password) < 8 {
		return fmt.Errorf("密码至少 8 位")
	}
	if n, err := a.Q.CountUsersByEmail(ctx, email); err != nil {
		return err
	} else if n > 0 {
		return fmt.Errorf("邮箱已经被注册了")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	port, err := a.availablePort(ctx)
	if err != nil {
		return err
	}
	p := a.newUserParams("admin", email, hash, "127.0.0.1", port, 1, "", time.Now())
	p.IsAdmin, p.Class, p.NodeSpeedlimit = 1, 0, 0
	if _, err := a.Q.CreateUser(ctx, p); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "Successful/添加成功!")
	return nil
}

// resetUserPort 为用户分配新端口；syncRelay 为 true 时同步中转规则中的端口。
func (a *App) resetUserPort(ctx context.Context, uid uint32, syncRelay bool) (int32, error) {
	var port int32
	err := a.tx(ctx, func(q *store.Queries) error {
		u, err := q.GetUserForUpdate(ctx, uid)
		if err != nil {
			return err
		}
		if port, err = a.availablePortQ(ctx, q); err != nil {
			return err
		}
		if syncRelay {
			if err := q.UpdateRelayPortForUser(ctx, store.UpdateRelayPortForUserParams{NewPort: port, UserID: uid, OldPort: u.Port}); err != nil {
				return err
			}
		}
		return q.UpdateUserPort(ctx, store.UpdateUserPortParams{Port: port, ID: uid})
	})
	return port, err
}

// cleanRelayRules 对应 XCat::cleanRelayRule：删除起点为“所有节点”的规则及与其他规则冲突的规则。
func (a *App) cleanRelayRules(ctx context.Context, out io.Writer) error {
	rules, err := a.Q.ListAllRelayRules(ctx)
	if err != nil {
		return err
	}
	nodes, err := a.Q.ListNodes(ctx)
	if err != nil {
		return err
	}
	byID := map[int32]store.SsNode{}
	for _, n := range nodes {
		byID[int32(n.ID)] = n //nolint:gosec // 节点 ID 范围很小
	}
	deleted := map[uint64]bool{}
	for _, rule := range rules {
		_, _ = fmt.Fprintln(out, rule.ID)
		del := rule.SourceNodeID == 0
		if !del {
			var set []store.Relay
			for _, x := range rules {
				if !deleted[x.ID] && (x.UserID == rule.UserID || x.UserID == 0) {
					set = append(set, x)
				}
			}
			del = relayConflict(rule, set, rule.ID, 0, byID, map[uint64]bool{}) != 0
		}
		if del {
			if _, err := a.Q.DeleteRelayRule(ctx, rule.ID); err != nil {
				return err
			}
			deleted[rule.ID] = true
			_, _ = fmt.Fprintf(out, "%d被删除！\n", rule.ID)
		}
	}
	return nil
}

// checkClassExpire 列出等级大于 0 却没有等级到期时间（1989 哨兵）的异常用户，便于逐个修正。
func (a *App) checkClassExpire(ctx context.Context, out io.Writer) error {
	users, err := a.Q.ListInvalidClassUsers(ctx)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		_, _ = fmt.Fprintln(out, "没有异常用户")
		return nil
	}
	_, _ = fmt.Fprintf(out, "共 %d 个用户等级大于 0 但没有等级到期时间，请在后台为其设置等级到期时间或把等级改为 0：\n", len(users))
	for _, u := range users {
		_, _ = fmt.Fprintf(out, "  ID %d  %s  等级 %d\n", u.ID, u.Email, u.Class)
	}
	return nil
}

// checkNodeIP 对比面板中转规则保存的目标 IP、原版规则与新版规则的计算结果，列出不一致的规则。只读，不修改任何数据。
// 迁移旧数据库后、保存目标节点之前运行：保存节点会按新版规则重新计算目标 IP（见 nodeip.go）。
func (a *App) checkNodeIP(ctx context.Context, out io.Writer) error {
	rules, err := a.Q.ListAllRelayRules(ctx)
	if err != nil {
		return err
	}
	nodes, err := a.Q.ListNodes(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int32]store.SsNode, len(nodes))
	for _, n := range nodes {
		byID[int32(n.ID)] = n //nolint:gosec // 节点 ID 范围很小
	}
	found := 0
	for _, rule := range rules {
		dist, ok := byID[rule.DistNodeID]
		if !ok || rule.DistNodeID <= 0 {
			continue
		}
		src := store.SsNode{ID: uint32(max(rule.SourceNodeID, 0))} //nolint:gosec // 已保证非负
		legacy, current := legacyRelayDistIP(src.ID, dist.NodeIp), relayDistIP(src, dist.NodeIp)
		if rule.DistIp == current && legacy == current {
			continue
		}
		if found == 0 {
			_, _ = fmt.Fprintln(out, "以下中转规则的目标 IP 需要核对（保存目标节点时会改为“新版规则”的结果）：")
		}
		found++
		_, _ = fmt.Fprintf(out, "  规则 %d  源节点 %d → 目标节点 %d（节点 IP：%s）  当前保存 %s  原版规则 %s  新版规则 %s\n",
			rule.ID, rule.SourceNodeID, rule.DistNodeID, dist.NodeIp, rule.DistIp, legacy, current)
	}
	if found == 0 {
		_, _ = fmt.Fprintln(out, "所有中转规则的目标 IP 与新旧规则一致")
	}
	return nil
}

// extendAccounts 把帐号有效期统一调整为 100 年（与新注册帐号一致）。默认只显示影响范围；
// 2099 永久哨兵与已经超过目标的帐号不变；已过期的帐号默认不处理（会让其恢复可用），需 --include-expired。
func (a *App) extendAccounts(ctx context.Context, args []string, out io.Writer) error {
	apply, includeExpired := false, false
	for _, arg := range args {
		switch arg {
		case "--apply":
			apply = true
		case "--include-expired":
			includeExpired = true
		default:
			return fmt.Errorf("未知参数 %q，用法: panel xcat extendAccounts [--apply] [--include-expired]", arg)
		}
	}
	now := time.Now().UTC()
	target := now.AddDate(100, 0, 0).Truncate(time.Second)
	n, err := a.Q.CountAccountsToExtend(ctx, store.CountAccountsToExtendParams{Now: now, Target: target})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "目标有效期：%s\n仍有效、需要调整的帐号：%d 个\n已过期的帐号：%d 个（%s）\n",
		target.In(a.Loc).Format(time.DateTime), n.Valid, n.Expired, map[bool]string{true: "将一并调整", false: "不处理，需要时加 --include-expired"}[includeExpired])
	if !apply {
		_, _ = fmt.Fprintln(out, "以上为预览，确认后加 --apply 执行（执行前请先备份数据库）")
		return nil
	}
	return a.tx(ctx, func(q *store.Queries) error {
		rows, err := q.ExtendValidAccounts(ctx, store.ExtendValidAccountsParams{Now: now, Target: target})
		if err != nil {
			return err
		}
		var expired int64
		if includeExpired {
			if expired, err = q.ExtendExpiredAccounts(ctx, store.ExtendExpiredAccountsParams{Now: now, Target: target}); err != nil {
				return err
			}
		}
		_, _ = fmt.Fprintf(out, "已调整：有效帐号 %d 个，已过期帐号 %d 个\n", rows, expired)
		return nil
	})
}
