// Package blockedip 解析用户的 disconnect_ip（面板判定超出同时在线 IP 数、暂时禁止连接的来源 IP，逗号分隔）。
//
// 规则（节点接口输出、检查与修复命令、导入报告共用）：
//   - 按逗号拆分，去掉每项首尾空白，空项忽略；
//   - 每项必须是单个 IPv4 或 IPv6 地址（net/netip 解析）：不接受端口、CIDR、IPv6 zone、前导零、
//     不完整或超过四段的 IPv4、数字段超过 255、分号等其他文本；
//   - 合法项统一为标准写法（IPv6 压缩小写，IPv4 映射地址转为 IPv4），重复项只保留一个。
//
// 与后端（ss 分支 netip.ParseAddr 并拒绝 zone）的判断一致，面板输出的每一项后端都能接受。
package blockedip

import (
	"net/netip"
	"strconv"
	"strings"
)

// Parse 校验单个地址，返回标准写法。
func Parse(s string) (string, bool) {
	ip, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || ip.Zone() != "" {
		return "", false
	}
	return ip.Unmap().String(), true
}

// List 是拆分后的结果。
type List struct {
	Valid   []string // 合法项（标准写法，去重，保持原顺序）
	Invalid []string // 非法项（原文，已去掉首尾空白）
}

// Split 拆分逗号分隔的列表。
func Split(raw string) List {
	var l List
	seen := map[string]bool{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		ip, ok := Parse(item)
		if !ok {
			l.Invalid = append(l.Invalid, item)
			continue
		}
		if !seen[ip] {
			seen[ip] = true
			l.Valid = append(l.Valid, ip)
		}
	}
	return l
}

// Kind 给非法项分类，用于告警与报告（不输出原文）。
func Kind(s string) string {
	switch {
	case strings.ContainsRune(s, ';'):
		return "混入分号"
	case strings.ContainsRune(s, '/'):
		return "CIDR 网段"
	case strings.ContainsRune(s, '%'):
		return "IPv6 zone"
	case strings.ContainsAny(s, "[]"):
		return "带方括号或端口"
	case strings.IndexFunc(s, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }) >= 0:
		return "含空白"
	}
	if s != "" && strings.Trim(s, "0123456789.") == "" { // 只有数字与点：按 IPv4 判断
		parts := strings.Split(s, ".")
		if len(parts) < 4 {
			return "IPv4 不完整"
		}
		if len(parts) > 4 {
			return "IPv4 超过四段"
		}
		for _, p := range parts {
			if p == "" {
				return "IPv4 有空段"
			}
		}
		for _, p := range parts {
			if n, err := strconv.Atoi(p); err != nil || n > 255 {
				return "数字段超过 255"
			}
		}
		for _, p := range parts {
			if len(p) > 1 && p[0] == '0' {
				return "前导零"
			}
		}
	}
	if strings.ContainsRune(s, ':') {
		if strings.Count(s, ":") == 1 {
			return "带端口或混入冒号"
		}
		return "IPv6 格式错误或混入冒号"
	}
	return "其他格式"
}

// Masked 是非法项的脱敏描述：分类与长度，不包含原文。
func Masked(s string) string {
	return Kind(s) + "（" + strconv.Itoa(len(s)) + " 字符）"
}
