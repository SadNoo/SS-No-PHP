package app

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/SadNoo/SS-No-PHP/internal/sub"
)

// 节点 IP（ss_node.node_ip）规则：逗号分隔的列表。
//   - 带 #源节点ID（多个用 | 分隔）的项是面板中转规则的专用目标 IP（原版语法），系统自动维护时永远保留，不参与主 IP 的判断。
//   - 第一个不带 # 的项是节点主 IP，也是面板中转规则的默认目标。节点地址为域名时由系统按解析结果自动维护
//     （保存节点、每分钟同步、来源 IP 不符时当场解析），只替换这一项；节点地址为 IP 时由管理员填写（留空则等于节点地址）。
//   - 其余不带 # 的项是手动添加的节点出口 IP，只用于节点认证，系统自动维护时永远保留。
//
// 与原版的区别：原版中转默认目标取最后一个不带 # 的项，且后面的项会覆盖前面匹配的 # 专用项。
// 原版结果还受排列顺序影响，只有一个不带 # 的项也可能不同（专用项排在默认项前面时）；
// 迁移后用 xcat checkNodeIP 列出新旧结果不同的中转规则（见 relay.go relayDistIP）。

// nodeIPSorts 是使用节点 IP 的节点类型（SS、VPN、中转、V2Ray、SS2022、VLESS、AnyTLS）。
var nodeIPSorts = map[int32]bool{0: true, 1: true, 10: true, 11: true, 12: true, 13: true, 14: true, 15: true, 16: true}

// nodeIPPrimary 返回节点主 IP（第一个不带 # 的项），没有时返回空字符串。
func nodeIPPrimary(nodeIP string) string {
	for _, e := range nodeIPEntries(nodeIP) {
		if !strings.Contains(e, "#") {
			return e
		}
	}
	return ""
}

// nodeIPWithPrimary 把主 IP（第一个不带 # 的项）替换为 ip，保留 # 专用项与手动添加的项及其顺序；
// 没有不带 # 的项时把 ip 加在最前面。
func nodeIPWithPrimary(nodeIP, ip string) string {
	entries := nodeIPEntries(nodeIP)
	for i, e := range entries {
		if !strings.Contains(e, "#") {
			entries[i] = ip
			return strings.Join(entries, ",")
		}
	}
	return strings.Join(append([]string{ip}, entries...), ",")
}

// nodeIPEntries 拆分节点 IP 列表，去掉空项。
func nodeIPEntries(nodeIP string) []string {
	var out []string
	for _, part := range strings.Split(nodeIP, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

var errNodeIPFormat = errors.New("节点 IP 格式无效：应为逗号分隔的 IP，可带 #源节点ID")

// validateNodeIP 检查节点 IP 列表的每一项都是 IP（可带 #源节点ID）。
func validateNodeIP(entries []string) error {
	for _, e := range entries {
		addr, ids, hasHash := strings.Cut(e, "#")
		if net.ParseIP(addr) == nil {
			return errNodeIPFormat
		}
		if hasHash {
			for _, id := range strings.Split(ids, "|") {
				if id == "" || strings.Trim(id, "0123456789") != "" {
					return errNodeIPFormat
				}
			}
		}
	}
	return nil
}

// nodeHost 返回节点真实地址（去掉外部中转后缀后的第一段）。客户端连接的中转地址不参与节点 IP 的计算。
func nodeHost(server string) string {
	return strings.TrimSpace(strings.Split(sub.RealServer(server), ";")[0])
}

// nodeIPHas 判断 ip 是否是节点 IP 列表中某一项的地址（去掉 # 后缀比较）。
func nodeIPHas(nodeIP, ip string) bool {
	for _, e := range nodeIPEntries(nodeIP) {
		if addr, _, _ := strings.Cut(e, "#"); addr == ip {
			return true
		}
	}
	return false
}

// legacyRelayDistIP 是原版 Tools::getRelayNodeIp 的结果：依次处理每一项，不带 # 的项和匹配源节点的 # 项都会覆盖之前的结果。
// 只用于 xcat checkNodeIP 对比新旧规则，不参与业务。
func legacyRelayDistIP(sourceID uint32, nodeIP string) string {
	ret := ""
	for _, part := range strings.Split(nodeIP, ",") {
		ip, ids, hasHash := strings.Cut(part, "#")
		if !hasHash {
			ret = ip
			continue
		}
		for _, id := range strings.Split(ids, "|") {
			if id == strconv.FormatUint(uint64(sourceID), 10) {
				ret = ip
			}
		}
	}
	return ret
}
