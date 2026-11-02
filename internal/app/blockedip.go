package app

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/blockedip"
	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// blockedIPWarnInterval 是同一节点“跳过非法封禁 IP”告警的最短间隔：节点每 30～60 秒拉取一次用户列表，
// 每次都记录会刷屏。间隔内的次数仍计入累计统计，下一次告警一并写出。
const blockedIPWarnInterval = 10 * time.Minute

// blockedIPWarnState 记录各节点上次告警的时间与累计跳过的项数（进程内，重启后重新计算）。
type blockedIPWarnState struct {
	mu      sync.Mutex
	last    map[uint32]time.Time
	skipped atomic.Int64 // 启动以来节点接口累计跳过的非法项
}

// blockedIPSkip 汇总一次用户列表中被跳过的非法项。
type blockedIPSkip struct {
	users []uint32
	items int
	kinds map[string]int
}

// nodeBlockedIPs 是节点接口输出的封禁 IP：user.disconnect_ip 中的合法项（标准写法）。
// 非法项（历史数据中的端口、网段、畸形文本等）不输出，计入 skip，不影响该用户的其他字段与其他用户。
func nodeBlockedIPs(u store.User, skip *blockedIPSkip) []string {
	l := blockedip.Split(u.DisconnectIp)
	if len(l.Invalid) > 0 {
		skip.users = append(skip.users, u.ID)
		skip.items += len(l.Invalid)
		if skip.kinds == nil {
			skip.kinds = map[string]int{}
		}
		for _, s := range l.Invalid {
			skip.kinds[blockedip.Kind(s)]++
		}
	}
	if l.Valid == nil {
		return []string{} // JSON 为 []
	}
	return l.Valid
}

// reportBlockedIPSkip 记录告警：节点、接口、用户 ID（最多 20 个）、项数与分类，不记录 IP 原文。同一节点限频。
func (a *App) reportBlockedIPSkip(nodeID uint32, api string, skip *blockedIPSkip) {
	if skip.items == 0 {
		return
	}
	total := a.blockedIPWarn.skipped.Add(int64(skip.items))
	a.blockedIPWarn.mu.Lock()
	if a.blockedIPWarn.last == nil {
		a.blockedIPWarn.last = map[uint32]time.Time{}
	}
	now := time.Now()
	if now.Sub(a.blockedIPWarn.last[nodeID]) < blockedIPWarnInterval {
		a.blockedIPWarn.mu.Unlock()
		return
	}
	a.blockedIPWarn.last[nodeID] = now
	a.blockedIPWarn.mu.Unlock()

	ids := skip.users
	more := ""
	if len(ids) > 20 {
		ids, more = ids[:20], " 等"
	}
	idText := make([]string, len(ids))
	for i, id := range ids {
		idText[i] = strconv.FormatUint(uint64(id), 10)
	}
	kinds := make([]string, 0, len(skip.kinds))
	for k, n := range skip.kinds {
		kinds = append(kinds, k+"×"+strconv.Itoa(n))
	}
	sort.Strings(kinds)
	a.Log.Warn("用户的封禁 IP（disconnect_ip）中有非法项，节点接口已跳过（其余封禁照常下发；修复见 panel xcat checkDisconnectIP）",
		"node", nodeID, "api", api, "users", len(skip.users), "user_ids", strings.Join(idText, ",")+more,
		"items", skip.items, "kinds", strings.Join(kinds, "、"), "skipped_total", total)
}
