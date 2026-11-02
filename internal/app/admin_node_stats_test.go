package app

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// nodeStatsFromPage 从后台首页「节点在线情况」读取节点数、在线数、离线数（没有节点时在线、离线为 -1）。
func nodeStatsFromPage(t *testing.T, e *testEnv) (total, alive, offline int) {
	t.Helper()
	body := e.page("/admin")
	if strings.Contains(body, "NaN") {
		t.Fatal("后台首页出现 NaN")
	}
	num := func(re string) int {
		m := regexp.MustCompile(re).FindStringSubmatch(body)
		if m == nil {
			return -1
		}
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		return n
	}
	return num(`节点在线情况\(节点数 (\d+)个\)`), num(`legendText: "在线节点 [\d.]+% (\d+)个"`), num(`legendText: "离线节点 [\d.]+% (\d+)个"`)
}

// TestAdminNodeStats 后台首页节点在线统计（第三方 P2）：只统计启用（type = 1）且是当前支持类型（VMess、SS2022、VLESS、AnyTLS）
// 的节点；最近 90 秒内有心跳为在线，过期与从未上报（心跳为 0）的为离线；停用节点、已停用 / 未知类型不计入。没有节点时为 0。
func TestAdminNodeStats(t *testing.T) {
	e, _ := adminEnv(t)
	e.exec("DELETE FROM ss_node")
	if total, alive, offline := nodeStatsFromPage(t, e); total != 0 || alive != -1 || offline != -1 {
		t.Fatalf("没有节点时应为节点数 0、不画在线 / 离线: %d %d %d", total, alive, offline)
	}
	add := func(sort, typ int, heartbeat string) {
		e.exec("INSERT INTO ss_node (name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group,node_heartbeat) "+
			"VALUES ('n',?,'1.1.1.1','','','',?,1,0,'',0,"+heartbeat+")", typ, sort)
	}
	for _, sort := range []int{11, 14, 15, 16} {
		add(sort, 1, "UNIX_TIMESTAMP() - 10")  // 在线
		add(sort, 1, "UNIX_TIMESTAMP() - 300") // 过期：离线
		add(sort, 1, "0")                      // 从未上报：离线
		add(sort, 0, "UNIX_TIMESTAMP() - 10")  // 停用：不计入
	}
	for _, sort := range []int{0, 10, 12, 13, 999} { // 已停用 / 未知类型：不计入
		add(sort, 1, "UNIX_TIMESTAMP() - 10")
	}
	if total, alive, offline := nodeStatsFromPage(t, e); total != 12 || alive != 4 || offline != 8 {
		t.Fatalf("节点数、在线、离线应为 12、4、8，实际 %d、%d、%d", total, alive, offline)
	}
}

// TestAdminNodeStatsBoundary 心跳时限的边界（固定时间，不受测试运行时间影响）：心跳正好是 90 秒前为离线，89 秒前为在线。
func TestAdminNodeStatsBoundary(t *testing.T) {
	e, _ := adminEnv(t)
	e.exec("DELETE FROM ss_node")
	const now = int64(1800000000)
	for _, hb := range []int64{now - 89, now - 90, now - 91} {
		e.exec("INSERT INTO ss_node (name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group,node_heartbeat) "+
			"VALUES ('n',1,'1.1.1.1','','','',14,1,0,'',0,?)", hb)
	}
	got, err := e.app.Q.AdminNodeStats(context.Background(), store.AdminNodeStatsParams{AliveAfter: now - nodeStatsAlive, Sorts: activeNodeSortValues()})
	if err != nil || got.Total != 3 || got.Alive != 1 {
		t.Fatalf("应为 3 个节点、1 个在线（89 秒前），实际 %+v %v", got, err)
	}
}
