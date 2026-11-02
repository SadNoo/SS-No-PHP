package app

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/radius"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// Radius 帐号与面板同步。Radius 数据库出错只记录日志，不影响面板本身的操作（原版出错时整个请求失败）。

// radiusAdd 对应 Radius::Add：创建或更新帐号；被封禁（radius_ban）的用户不创建。
func (a *App) radiusAdd(ctx context.Context, u store.User, pwd string) {
	if a.Radius == nil {
		return
	}
	if err := a.radiusAddErr(ctx, u, pwd); err != nil {
		a.Log.Warn("同步 Radius 帐号失败", "user", u.ID, "err", err)
	}
}

func (a *App) radiusAddErr(ctx context.Context, u store.User, pwd string) error {
	exists, err := a.Radius.Exists(ctx, u.Email)
	if err != nil {
		return err
	}
	if exists {
		return a.Radius.SetPassword(ctx, u.Email, pwd)
	}
	if n, err := a.Q.CountRadiusBan(ctx, u.ID); err != nil || n > 0 {
		return err
	}
	return a.Radius.Create(ctx, u.Email, pwd)
}

// radiusDelete 对应 Radius::Delete。
func (a *App) radiusDelete(ctx context.Context, email string) {
	if a.Radius == nil {
		return
	}
	if err := a.Radius.Delete(ctx, email); err != nil {
		a.Log.Warn("删除 Radius 帐号失败", "err", err)
	}
}

// radiusRename 对应 Radius::ChangeUserName（后台修改用户邮箱时）。
func (a *App) radiusRename(ctx context.Context, u store.User, newEmail, pwd string) {
	if a.Radius == nil {
		return
	}
	err := func() error {
		exists, err := a.Radius.Exists(ctx, u.Email)
		if err != nil {
			return err
		}
		if exists {
			return a.Radius.Rename(ctx, u.Email, newEmail, pwd)
		}
		u.Email = newEmail
		return a.radiusAddErr(ctx, u, pwd)
	}()
	if err != nil {
		a.Log.Warn("同步 Radius 用户名失败", "user", u.ID, "err", err)
	}
}

// radiusNas 对应 Radius::AddNas / DelNas（VPN 节点，sort = 1）。
func (a *App) radiusNas(ctx context.Context, ip string, add bool) {
	if a.Radius == nil || ip == "" {
		return
	}
	var err error
	if add {
		err = a.Radius.AddNas(ctx, ip)
	} else {
		err = a.Radius.DelNas(ctx, ip)
	}
	if err != nil {
		a.Log.Warn("同步 Radius NAS 失败", "ip", ip, "err", err)
	}
}

// radiusBanCheck 对应 CheckJob 中维护 radius_ban 的部分：流量用完、被禁用或已过期的用户删除 Radius 帐号，
// 恢复正常后重新创建。radius_ban 表在未启用 Radius 时也照常维护（与原版一致）。
func (a *App) radiusBanCheck(ctx context.Context, now time.Time) error {
	users, err := a.Q.ListAllUsers(ctx)
	if err != nil {
		return err
	}
	byID := make(map[uint32]store.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	bans, err := a.Q.ListRadiusBans(ctx)
	if err != nil {
		return err
	}
	banned := map[uint32]bool{}
	for _, b := range bans {
		banned[b.Userid] = true
	}
	blocked := func(u store.User) bool {
		expired := u.ExpireIn.Before(now) && u.ExpireIn.Unix() > 644447105
		return u.TransferEnable <= u.U+u.D || u.Enable == 0 || expired
	}
	for _, u := range users {
		if blocked(u) && !banned[u.ID] {
			if err := a.Q.InsertRadiusBan(ctx, u.ID); err != nil {
				return err
			}
			banned[u.ID] = true
			a.radiusDelete(ctx, u.Email)
		}
	}
	for _, b := range bans {
		u, ok := byID[b.Userid]
		if ok && blocked(u) {
			continue
		}
		if err := a.Q.DeleteRadiusBan(ctx, b.ID); err != nil {
			return err
		}
		if ok {
			a.radiusAdd(ctx, u, u.Passwd)
		}
	}
	return nil
}

// radiusUserIDs 返回 Radius 用户名到用户 ID 的映射。
func (a *App) radiusUserIDs(ctx context.Context) (map[string]uint32, error) {
	rows, err := a.Q.ListUserEmails(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]uint32, len(rows))
	for _, r := range rows {
		m[radius.UserName(r.Email)] = r.ID
	}
	return m, nil
}

// radiusWindow 返回上次处理到的时间与 Radius 数据库的当前时间，并在 fn 成功后记录新的处理位置。
//
// 原版每分钟处理“最近 60 秒”的记录，定时任务稍有延迟就会漏记或重复记录；这里记录处理位置。
func (a *App) radiusWindow(ctx context.Context, key string, fn func(from, to time.Time) error) error {
	to, err := a.Radius.Now(ctx)
	if err != nil {
		return err
	}
	st, err := a.Q.GetJobState(ctx, key)
	if isNoRows(err) { // 首次执行：从现在开始，不补记历史
		return a.Q.SetJobState(ctx, store.SetJobStateParams{Name: key, Value: to.Format(time.DateTime), UpdatedAt: time.Now().Unix()})
	}
	if err != nil {
		return err
	}
	from, err := time.Parse(time.DateTime, st.Value)
	if err != nil {
		return fmt.Errorf("job_state %s: %w", key, err)
	}
	if err := fn(from, to); err != nil {
		return err
	}
	return a.Q.SetJobState(ctx, store.SetJobStateParams{Name: key, Value: to.Format(time.DateTime), UpdatedAt: time.Now().Unix()})
}

// addRadiusTraffic 记录一条流量并累加到用户。
func (a *App) addRadiusTraffic(ctx context.Context, uid, nodeID uint32, u, d int64, now time.Time) error {
	return a.tx(ctx, func(q *store.Queries) error {
		if err := q.InsertTrafficLog(ctx, store.InsertTrafficLogParams{
			UserID: uid, U: u, D: d, NodeID: nodeID, Rate: 1, Traffic: util.FlowAutoShow(float64(u + d)), LogTime: now.Unix(),
		}); err != nil {
			return err
		}
		return q.AddUserTraffic(ctx, store.AddUserTrafficParams{U: u, D: d, T: now.Unix(), ID: uid})
	})
}

// radiusSyncLogin 对应 SyncRadius::synclogin：每次 VPN 认证成功记 10000 字节流量（节点 1）。
// 原版把认证失败也计入。
func (a *App) radiusSyncLogin(ctx context.Context, now time.Time) error {
	if a.Radius == nil {
		return nil
	}
	ids, err := a.radiusUserIDs(ctx)
	if err != nil {
		return err
	}
	return a.radiusWindow(ctx, "radius:synclogin", func(from, to time.Time) error {
		names, err := a.Radius.AcceptedLogins(ctx, from, to)
		if err != nil {
			return err
		}
		for _, n := range names {
			if uid, ok := ids[n]; ok {
				if err := a.addRadiusTraffic(ctx, uid, 1, 0, 10000, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// radiusSyncVPN 对应 SyncRadius::syncvpn：把结束的 VPN 会话流量记到用户（节点 2）。
// 原版遇到面板中不存在的用户名会出错中断。
func (a *App) radiusSyncVPN(ctx context.Context, now time.Time) error {
	if a.Radius == nil {
		return nil
	}
	ids, err := a.radiusUserIDs(ctx)
	if err != nil {
		return err
	}
	return a.radiusWindow(ctx, "radius:syncvpn", func(from, to time.Time) error {
		sessions, err := a.Radius.FinishedSessions(ctx, from, to)
		if err != nil {
			return err
		}
		for _, s := range sessions {
			if uid, ok := ids[s.UserName]; ok {
				if err := a.addRadiusTraffic(ctx, uid, 2, s.Input, s.Output, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// radiusSyncUsers 对应 SyncRadius::syncusers：为所有用户创建 Radius 帐号并邮件通知用户名。
//
// 原版在邮件中明文发送连接密码（K49），这里只告知用户名，密码请用户在用户中心查看。
func (a *App) radiusSyncUsers(ctx context.Context, out io.Writer) error {
	if a.Radius == nil {
		return fmt.Errorf("未启用 Radius（radius.enable）")
	}
	users, err := a.Q.ListAllUsers(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		if err := a.radiusAddErr(ctx, u, u.Passwd); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "Send sync mail to user:", u.ID)
		text := "您好，为了保证密码系统的统一，刚刚系统已经将您 vpn 等连接方式的用户名重置为：" + radius.UserName(u.Email) +
			"，密码与您 ss 的连接密码相同（可在用户中心查看），以后您修改 ss 密码就会自动修改 vpn 等连接方式的密码了，感谢您的支持。"
		if err := a.Mail.Send(ctx, u.Email, a.Cfg.App.Name+"-密码更新通知", "password_vpn", map[string]any{"user": u, "text": text}); err != nil {
			a.Log.Warn("发送邮件失败", "to", u.Email, "err", err)
		}
	}
	return nil
}

// radiusSyncNasNode 对应 Job::syncnasnode：把使用域名的节点解析为 IP 并加入 NAS。
func (a *App) radiusSyncNasNode(ctx context.Context) error {
	if a.Radius == nil {
		return fmt.Errorf("未启用 Radius（radius.enable）")
	}
	nodes, err := a.Q.ListNodes(ctx)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		first := nodeHost(n.Server)
		if ipv4Re.MatchString(first) || (n.Sort != 0 && n.Sort != 10 && n.Sort != 12 && n.Sort != 13) {
			continue
		}
		ip := gethostbyname(ctx, first)
		if ip == first { // 原版解析失败时把域名写入 node_ip
			a.Log.Warn("解析节点地址失败", "node", n.ID, "server", n.Server)
			continue
		}
		if err := a.Q.UpdateNodeIP(ctx, store.UpdateNodeIPParams{NodeIp: ip, ID: n.ID}); err != nil {
			return err
		}
		if err := a.Radius.AddNas(ctx, ip); err != nil {
			return err
		}
	}
	return nil
}
