package app

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/sub"
)

// nodeOnlineAccessible 对应 Node::isNodeAccessable：流量未超限且在线。
func nodeOnlineAccessible(n store.SsNode, now time.Time) bool {
	trafficOut := n.NodeBandwidthLimit != 0 && n.NodeBandwidth >= n.NodeBandwidthLimit
	ssLike := n.Sort == 0 || (n.Sort >= 7 && n.Sort <= 13 && n.Sort != 9)
	online := n.NodeHeartbeat != 0 && ssLike && n.NodeHeartbeat > now.Unix()-300
	return !trafficOut && online
}

// relayNodes 对应 RelayController 中按条件查询节点：分组匹配、type = 1、等级不高于用户，sort 在给定范围内，按名称排序。
func relayNodes(all []store.SsNode, u store.User, sorts ...int32) []store.SsNode {
	var out []store.SsNode
	for _, n := range all {
		if n.Type != 1 || n.NodeClass > u.Class || (n.NodeGroup != u.NodeGroup && n.NodeGroup != 0) {
			continue
		}
		for _, s := range sorts {
			if n.Sort == s {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

// relayPath 是中转链路表中的一行（原版 Tools::insertPathRule 中的 stdClass）。
type relayPath struct {
	Port      int32
	BeginNode store.SsNode
	EndNode   store.SsNode
	Path      template.HTML
	Status    string
}

func redName(name string) string {
	return `<font color="#FF0000">` + template.HTMLEscapeString(name) + `</font>`
}

// relayPaths 计算链路表（原版 index 中的 insertPathRule 与合并循环）。
func (a *App) relayPaths(r *http.Request, u store.User, all []store.SsNode, byID map[int32]store.SsNode, rules []store.Relay, now time.Time) ([]*relayPath, error) {
	var paths []*relayPath
	// insert 对应 Tools::insertPathRule。原版规则的起点为“所有节点”（0）时会对空对象调用方法而报错，
	// 这里以当前计算的节点作为起点。
	insert := func(rule *store.Relay, port int32, current store.SsNode) {
		if rule.DistNodeID == -1 {
			return
		}
		src, ok := byID[rule.SourceNodeID]
		if !ok {
			src = current
		}
		dst := byID[rule.DistNodeID]
		for _, path := range paths {
			if path.Port != port {
				continue
			}
			if rule.DistNodeID == int32(path.BeginNode.ID) { //nolint:gosec // 节点 ID 范围很小
				path.BeginNode = src
				if !nodeOnlineAccessible(src, now) {
					path.Path = template.HTML(redName(src.Name)) + " → " + path.Path //nolint:gosec // 已转义
					path.Status = "阻断"
				} else {
					path.Path = template.HTML(template.HTMLEscapeString(src.Name)) + " → " + path.Path //nolint:gosec // 已转义
					path.Status = "通畅"
				}
				return
			}
			if int32(path.EndNode.ID) == rule.SourceNodeID { //nolint:gosec // 节点 ID 范围很小
				path.EndNode = dst
				if !nodeOnlineAccessible(dst, now) {
					path.Path += template.HTML(" → " + redName(dst.Name)) //nolint:gosec // 已转义
					path.Status = "阻断"
				} else {
					path.Path += template.HTML(" → " + template.HTMLEscapeString(dst.Name)) //nolint:gosec // 已转义
				}
				return
			}
		}
		np := &relayPath{Port: port, BeginNode: src, EndNode: dst, Status: "通畅"}
		if !nodeOnlineAccessible(src, now) {
			np.Path, np.Status = template.HTML(redName(src.Name)), "阻断" //nolint:gosec // 已转义
		} else {
			np.Path = template.HTML(template.HTMLEscapeString(src.Name)) //nolint:gosec // 已转义
		}
		if !nodeOnlineAccessible(dst, now) {
			np.Path += template.HTML(" -> " + redName(dst.Name)) //nolint:gosec // 已转义
			np.Status = "阻断"
		} else {
			np.Path += template.HTML(" -> " + template.HTMLEscapeString(dst.Name)) //nolint:gosec // 已转义
		}
		paths = append(paths, np)
	}

	relayNodesList := relayNodes(all, u, sub.SortRelay, sub.SortV2RayRelay)
	muNodes := relayNodes(all, u, sub.SortMuPort)
	muUsers := map[string]store.User{}
	if len(muNodes) > 0 {
		var ports []int32
		for _, n := range muNodes {
			if v, err := strconv.Atoi(n.Server); err == nil {
				ports = append(ports, int32(v)) //nolint:gosec // 端口范围很小
			}
		}
		if len(ports) > 0 {
			list, err := a.Q.ListUsersByPorts(r.Context(), ports)
			if err != nil {
				return nil, err
			}
			for i := len(list) - 1; i >= 0; i-- {
				muUsers[strconv.Itoa(int(list[i].Port))] = list[i]
			}
		}
	}
	for _, n := range relayNodesList {
		if n.MuOnly == 0 {
			if rule := sub.PickOutRelayRule(n.ID, u.Port, rules); rule != nil {
				insert(rule, u.Port, n)
			}
		}
		if n.CustomRss == 1 {
			for _, mn := range muNodes {
				mu, ok := muUsers[mn.Server]
				usable := mu.Class >= n.NodeClass && (n.NodeGroup == 0 || n.NodeGroup == mu.NodeGroup)
				if !ok || !usable {
					continue
				}
				if mu.IsMultiUser != 2 {
					if rule := sub.PickOutRelayRule(n.ID, mu.Port, rules); rule != nil {
						insert(rule, mu.Port, n)
					}
				}
			}
		}
	}

	// 合并首尾相接的链路。
	removed := make([]bool, len(paths))
	for i, path := range paths {
		if removed[i] {
			continue
		}
		for j, single := range paths {
			if removed[j] || i == j || path.Port != single.Port {
				continue
			}
			if single.EndNode.ID == path.BeginNode.ID {
				path.BeginNode = single.BeginNode
				if !nodeOnlineAccessible(path.BeginNode, now) {
					path.Path = template.HTML(redName(single.BeginNode.Name)) + " → " + path.Path //nolint:gosec // 已转义
					path.Status = "阻断"
				} else {
					path.Path = template.HTML(template.HTMLEscapeString(single.BeginNode.Name)) + " → " + path.Path //nolint:gosec // 已转义
					path.Status = "通畅"
				}
				removed[j] = true
				continue
			}
			if path.EndNode.ID == single.BeginNode.ID {
				path.EndNode = single.EndNode
				if !nodeOnlineAccessible(single.EndNode, now) {
					path.Path += template.HTML(" → " + redName(single.EndNode.Name)) //nolint:gosec // 已转义
					path.Status = "阻断"
				} else {
					path.Path += template.HTML(" → " + template.HTMLEscapeString(single.EndNode.Name)) //nolint:gosec // 已转义
				}
				removed[j] = true
			}
		}
	}
	var out []*relayPath
	for i, path := range paths {
		if !removed[i] {
			out = append(out, path)
		}
	}
	return out, nil
}

// relayDistIP 对应 Tools::getRelayNodeIp：目标节点的 node_ip 可以写成 “IP#起点ID|起点ID,IP”，按起点选择 IP。
func relayDistIP(source store.SsNode, distIP string) string {
	// 默认为节点主 IP（第一个不带 # 的项）；带 #源节点ID 的专用目标 IP 优先，与先后顺序无关。
	// 原版默认取最后一个不带 # 的项，且排在后面的不带 # 的项会覆盖前面匹配的专用项；
	// 新版不带 # 的其余项是节点出口 IP，不能作为中转目标，所以改为主 IP。差异见 xcat checkNodeIP。
	ret := nodeIPPrimary(distIP)
	for _, part := range strings.Split(distIP, ",") {
		ip, ids, hasHash := strings.Cut(part, "#")
		if !hasHash {
			continue
		}
		for _, id := range strings.Split(ids, "|") {
			if id == strconv.FormatUint(uint64(source.ID), 10) {
				ret = ip
			}
		}
	}
	return ret
}

// relayConflict 对应 Tools::has_conflict_rule：检查新规则是否会与已有规则形成回环。
// 返回冲突规则的 ID，-1 表示可能冲突，0 表示没有冲突。原版遇到环形规则时会无限递归，这里记录已访问的规则。
func relayConflict(input store.Relay, rules []store.Relay, editID uint64, origin int32, nodes map[int32]store.SsNode, visited map[uint64]bool) int64 {
	for _, rule := range rules {
		if rule.SourceNodeID == input.DistNodeID && (rule.Port == input.Port || input.Port == 0 || rule.Port == 0) {
			if rule.DistNodeID == origin && rule.ID != editID {
				return int64(rule.ID) //nolint:gosec // 规则 ID 范围很小
			}
			if visited[rule.ID] {
				continue
			}
			visited[rule.ID] = true
			if id := relayConflict(rule, rules, editID, origin, nodes, visited); id != 0 {
				return id
			}
		}
	}
	if (input.ID == editID || editID == 0) && input.DistNodeID != -1 {
		dist := nodes[input.DistNodeID]
		if input.SourceNodeID == 0 && (dist.Sort == sub.SortRelay || dist.Sort == sub.SortV2RayRelay) {
			return -1
		}
		if input.DistNodeID == input.SourceNodeID {
			return -1
		}
	}
	return 0
}
