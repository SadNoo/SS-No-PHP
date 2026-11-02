package app

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// 节点来源 IP 校验，以及动态 IP（DDNS）节点的处理。
//
// 节点请求必须来自后台登记的“节点 IP”。节点使用 DDNS 时 IP 会变化，处理顺序为：
//  1. 来源 IP 与某个节点的 node_ip 一致：通过。
//  2. 不一致时立即重新解析节点域名（30 秒缓存）：域名已指向该 IP 则通过，并更新 node_ip。
//  3. DDNS 尚未更新时的宽限期（security.node_ip_grace）：请求带有正确的密钥和明确的 node_id，
//     且该节点已有一段时间（nodeQuiet）没有从登记的 IP 发来请求、距最后一次从登记 IP 发来请求不超过宽限期，则通过。
//     节点仍在从登记 IP 正常上报时宽限期不生效，密钥泄露也无法冒充正常运行中的节点。

// nodeQuiet 是判断“节点已不再从登记 IP 发来请求”的静默时间（节点后端通常每 60 秒请求一轮）。
const nodeQuiet = 90 * time.Second

// dnsCacheTTL 是校验失败时重新解析域名的缓存时间，防止伪造来源的请求触发大量 DNS 查询。
const dnsCacheTTL = 30 * time.Second

type dnsEntry struct {
	ips     []string
	expires time.Time
}

// nodeAuthState 记录各节点最后一次从登记 IP 发来请求的时间，以及域名解析缓存（仅在本进程内）。
type nodeAuthState struct {
	mu       sync.Mutex
	verified map[uint32]time.Time
	dns      map[string]dnsEntry
}

func (s *nodeAuthState) markVerified(id uint32, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.verified == nil {
		s.verified = map[uint32]time.Time{}
	}
	s.verified[id] = now
}

func (s *nodeAuthState) lastVerified(id uint32) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verified[id]
}

// resolve 解析域名的 IPv4 / IPv6 地址（带缓存）。
func (a *App) resolve(ctx context.Context, host string, now time.Time) []string {
	s := &a.nodeAuth
	s.mu.Lock()
	if e, ok := s.dns[host]; ok && now.Before(e.expires) {
		s.mu.Unlock()
		return e.ips
	}
	s.mu.Unlock()
	lookup := a.lookupIP
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]string, error) {
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			out := make([]string, 0, len(ips))
			for _, ip := range ips {
				out = append(out, ip.String())
			}
			return out, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ips, _ := lookup(ctx, host)
	s.mu.Lock()
	if s.dns == nil {
		s.dns = map[string]dnsEntry{}
	}
	s.dns[host] = dnsEntry{ips: ips, expires: now.Add(dnsCacheTTL)}
	s.mu.Unlock()
	return ips
}

// nodeByFreshDNS 重新解析使用域名的节点，返回域名已指向 ip 的节点（并把 ip 写入其 node_ip）。
func (a *App) nodeByFreshDNS(ctx context.Context, nodes []store.SsNode, ip string, now time.Time) (*store.SsNode, error) {
	for i := range nodes {
		n := &nodes[i]
		host := nodeHost(n.Server)
		if host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
			continue
		}
		for _, got := range a.resolve(ctx, host, now) {
			if got != ip {
				continue
			}
			updated := nodeIPWithPrimary(n.NodeIp, ip) // 只替换主 IP，保留手动添加的项
			if err := a.Q.UpdateNodeIP(ctx, store.UpdateNodeIPParams{NodeIp: updated, ID: n.ID}); err != nil {
				return nil, err
			}
			a.Log.Info("节点 IP 已变化，按域名解析结果更新", "node", n.ID, "server", host, "old", n.NodeIp, "new", updated)
			n.NodeIp = updated
			return n, nil
		}
	}
	return nil, nil
}

// authorizeNode 校验节点请求的来源。返回 ok 以及拒绝时附带的 IP（原版的错误信息格式）。
func (a *App) authorizeNode(ctx context.Context, ip, nodeIDParam string, keyOK bool) (bool, error) {
	if isLoopback(ip) {
		return true, nil
	}
	now := time.Now()
	nodes, err := a.Q.ListNodes(ctx)
	if err != nil {
		return false, err
	}
	reqID, _ := strconv.ParseUint(nodeIDParam, 10, 32)
	var matched *store.SsNode
	for i := range nodes {
		if nodeIPMatches(nodes[i].NodeIp, ip) {
			if matched == nil || uint64(nodes[i].ID) == reqID {
				matched = &nodes[i]
			}
		}
	}
	if matched == nil && keyOK {
		if matched, err = a.nodeByFreshDNS(ctx, nodes, ip, now); err != nil {
			return false, err
		}
	}
	if matched != nil {
		if keyOK {
			a.nodeAuth.markVerified(matched.ID, now)
			if reqID != 0 && uint64(matched.ID) != reqID {
				for i := range nodes { // 同一 IP 上的其他节点
					if uint64(nodes[i].ID) == reqID && nodeIPMatches(nodes[i].NodeIp, ip) {
						a.nodeAuth.markVerified(nodes[i].ID, now)
					}
				}
			}
		}
		return true, nil
	}
	grace := a.Cfg.Security.NodeIPGrace.D()
	if !keyOK || grace <= 0 || reqID == 0 {
		return false, nil
	}
	last := a.nodeAuth.lastVerified(uint32(reqID)) //nolint:gosec // ParseUint 已限制为 32 位
	if last.IsZero() || now.Sub(last) < nodeQuiet || now.Sub(last) > grace {
		return false, nil
	}
	a.Log.Warn("节点来源 IP 与登记不符，宽限期内放行（等待 DDNS 更新）", "node", reqID, "ip", ip, "last_verified", last.Format(time.DateTime))
	return true, nil
}
