package app

import (
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// ticketPolicy 过滤工单 HTML（原版用 voku/anti-xss 在保存时过滤）。工单内容由 editor.md 生成，
// 保存和显示时都经过过滤，旧数据中残留的危险内容也不会输出。
var ticketPolicy = bluemonday.UGCPolicy()

// sanitizeTicket 过滤工单内容。
func sanitizeTicket(s string) string { return ticketPolicy.Sanitize(s) }

// badTicketWords 对应原版：内容中（非开头位置）出现 admin 或 user 时拒绝提交。
func badTicketWords(content string) bool {
	return strings.Index(content, "admin") > 0 || strings.Index(content, "user") > 0
}
