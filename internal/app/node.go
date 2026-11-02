package app

import (
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/auth"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/sub"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// nodeRow 是节点列表页的一行（原版 UserController::node 中的 $array_node，以及模板里的计算结果）。
type nodeRow struct {
	ID          uint32
	Class       int32
	Name        string
	Info        string
	Status      string
	Flag        string
	OnlineUser  string // 在线人数，没有数据时为 "N/A"
	Online      int    // 1 在线，0 未知（新节点或非 SS 类节点），-1 离线
	LoadText    string // 负载，"N/A" 或 "52%"
	TrafficUsed int64
	TrafficMax  int64
	Bandwidth   string // 端口限速，0 表示不限
	TrafficRate string

	GroupStart bool // 新的一组（按节点等级分组）
	GroupEnd   bool
	Index      int

	Locked        bool // 用户等级不足
	ShowLink      bool // 显示普通端口入口
	LinkRelayID   uint64
	LinkRelayName string
	V2Title       bool // 原版 displayNodeLinkV2
	MuEntries     []muEntry
	V2            *v2Info
	AjaxID        uint32 // 展开时加载图表的节点（原版 $point_node），0 表示没有
}

type muEntry struct {
	Port      string
	RelayID   uint64
	RelayName string
}

// v2Info 对应原版模板函数 displayV2rayNode。
type v2Info struct {
	Add, Port, Aid, Net, Path, Type string
	TLS                             bool
	URL                             string
}

// flagFile 从节点名称中取出国家/地区，返回 /images/prefix/ 下的图片文件名。
// 图片文件名是地区名 UTF-8 编码的十六进制（原版直接用中文文件名，打包进程序时不便处理）。
func flagFile(re *regexp.Regexp, name string) string {
	if m := strings.TrimSpace(re.FindString(name)); m != "" {
		return hex.EncodeToString([]byte(m)) + ".png"
	}
	return hex.EncodeToString([]byte("unknown")) + ".png"
}

// userNode 对应 UserController::node。
func (a *App) userNode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	now := time.Now()

	nodes, err := a.Q.ListUserNodes(ctx)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	all, err := a.nodeMap(r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	// Tools::is_protocol_relay 在原版中恒为 true。
	relays, err := a.Q.ListRelayRulesVisible(ctx, u.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	infos, err := a.Q.ListLatestNodeInfo(ctx, now.Unix()-300)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	onlines, err := a.Q.ListLatestNodeOnline(ctx, now.Unix()-300)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	loadOf := map[uint32]string{}
	for _, x := range infos {
		loadOf[x.NodeID] = x.Load
	}
	onlineOf := map[uint32]int32{}
	for _, x := range onlines {
		onlineOf[x.NodeID] = x.OnlineUser
	}

	// 单端口承载节点（sort = 9）及其承载用户。
	type muPort struct {
		server store.SsNode
		user   store.User
	}
	var muPorts []muPort
	var ports []int32
	for _, n := range nodes {
		if n.Sort == sub.SortMuPort && (u.IsAdmin() || n.NodeGroup == u.NodeGroup || n.NodeGroup == 0) {
			if p, err := strconv.Atoi(n.Server); err == nil {
				ports = append(ports, int32(p)) //nolint:gosec // 端口范围很小
			}
		}
	}
	muUsers := map[int32]store.User{}
	if len(ports) > 0 {
		list, err := a.Q.ListUsersByPorts(ctx, ports)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		for i := len(list) - 1; i >= 0; i-- { // 同端口多个用户时取第一个（原版 first()）
			muUsers[list[i].Port] = list[i]
		}
	}

	relayName := func(rule *store.Relay) string {
		if rule == nil {
			return ""
		}
		return all[uint32(rule.DistNodeID)].Name //nolint:gosec // 阻断规则不会被选出
	}
	flagRe := regexp.MustCompile(a.Cfg.Display.FlagRegex)

	var rows []nodeRow
	for _, n := range nodes {
		if !u.IsAdmin() && n.NodeGroup != u.NodeGroup && n.NodeGroup != 0 {
			continue
		}
		if n.Sort == sub.SortMuPort {
			p, _ := strconv.Atoi(n.Server)
			if mu, ok := muUsers[int32(p)]; ok { //nolint:gosec // 端口范围很小
				muPorts = append(muPorts, muPort{server: n, user: mu})
			}
			continue
		}
		row := nodeRow{
			ID: n.ID, Class: n.NodeClass, Name: n.Name, Info: n.Info, Status: n.Status,
			Flag:        flagFile(flagRe, n.Name),
			TrafficUsed: int64(util.BytesToGB(float64(n.NodeBandwidth))),
			TrafficMax:  int64(util.BytesToGB(float64(n.NodeBandwidthLimit))),
			TrafficRate: util.PHPFloat(n.TrafficRate),
			OnlineUser:  "0",
			LoadText:    "N/A",
		}
		ssLike := n.Sort == 0 || (n.Sort >= 7 && n.Sort <= 16 && n.Sort != 9)
		if c, ok := onlineOf[n.ID]; ok {
			if ssLike {
				row.OnlineUser = strconv.Itoa(int(c))
			} else {
				row.OnlineUser = "N/A"
			}
		}
		switch {
		case !ssLike || n.NodeHeartbeat == 0:
			row.Online = 0
		case n.NodeHeartbeat+300 > now.Unix():
			row.Online = 1
		default:
			row.Online = -1
		}
		if l, ok := loadOf[n.ID]; ok {
			first, _, _ := strings.Cut(l, " ")
			f, _ := strconv.ParseFloat(first, 64)
			row.LoadText = util.PHPFloat(f*100) + "%"
		}
		switch sp := n.NodeSpeedlimit.Float(); {
		case sp == 0:
			row.Bandwidth = "0"
		case sp >= 1024:
			row.Bandwidth = util.PHPFloat(util.PHPRound(sp/1024, 1)) + "Gbps"
		default:
			row.Bandwidth = n.NodeSpeedlimit.String() + "Mbps"
		}

		row.Locked = n.NodeClass > u.Class
		if !row.Locked {
			var rule *store.Relay
			if n.Sort == sub.SortRelay {
				rule = sub.PickOutRelayRule(n.ID, u.Port, relays)
			}
			// 原版条件 ($sort != 11 || $sort != 12) 恒为真，只要不是“仅单端口”就显示普通端口入口。
			if n.MuOnly != 1 {
				row.ShowLink = true
				if rule != nil {
					row.LinkRelayID, row.LinkRelayName = rule.ID, relayName(rule)
				}
			} else if n.Sort == sub.SortV2Ray || n.Sort == sub.SortV2RayRelay {
				row.V2Title = true
				row.AjaxID = n.ID
			}
			if n.Sort == sub.SortSS || n.Sort == sub.SortRelay || n.Sort == sub.SortSSV2Ray {
				row.AjaxID = n.ID
			}
			if (n.Sort == sub.SortSS || n.Sort == sub.SortRelay) && n.MuOnly != -1 {
				for _, mp := range muPorts {
					serverOK := mp.server.NodeClass <= u.Class && (mp.server.NodeGroup == 0 || mp.server.NodeGroup == u.NodeGroup)
					userOK := mp.user.Class >= n.NodeClass && (n.NodeGroup == 0 || mp.user.NodeGroup == n.NodeGroup)
					if !serverOK || !userOK {
						continue
					}
					e := muEntry{Port: mp.server.Server}
					if n.Sort == sub.SortRelay && mp.user.IsMultiUser != 2 {
						if rule := sub.PickOutRelayRule(n.ID, mp.user.Port, relays); rule != nil {
							e.RelayID, e.RelayName = rule.ID, relayName(rule)
						}
					}
					row.MuEntries = append(row.MuEntries, e)
				}
			}
			if n.Sort == sub.SortV2Ray || n.Sort == sub.SortV2RayRelay {
				v := a.Sub.V2Item(u.User, n)
				row.V2 = &v2Info{
					Add: v.Str("add"), Port: v.Str("port"), Aid: v.Str("aid"), Net: v.Str("net"),
					Path: v.Str("path"), Type: v.Str("type"),
					TLS: (v.Str("net") == "ws" && v.Str("tls") == "tls") || v.Str("net") == "tls",
					URL: a.Sub.V2URL(u.User, n),
				}
			}
		}
		rows = append(rows, row)
	}
	// 分组标记：原版模板在节点等级变化处开始新的一组。
	for i := range rows {
		rows[i].Index = i
		rows[i].GroupStart = i == 0 || rows[i].Class != rows[i-1].Class
		rows[i].GroupEnd = i == len(rows)-1 || rows[i].Class != rows[i+1].Class
	}
	a.userPage(w, r, "user/node", map[string]any{"nodes": rows})
}

// nodeAccessible 对应 nodeInfo 中的权限判断：等级与分组符合（或管理员），且节点流量未超限。
func nodeAccessible(u store.User, n store.SsNode) bool {
	return ((u.Class >= n.NodeClass && (u.NodeGroup == n.NodeGroup || n.NodeGroup == 0)) || u.IsAdmin != 0) &&
		(n.NodeBandwidthLimit == 0 || n.NodeBandwidth < n.NodeBandwidthLimit)
}

// nodeInfo 对应 UserController::nodeInfo：节点配置详情（在节点列表页的弹窗中打开）。
func (a *App) nodeInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.User(r)
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 32)
	if err != nil {
		return
	}
	n, err := a.Q.GetNode(ctx, uint32(id))
	if isNoRows(err) {
		return // 原版返回 null：空白页面
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}
	mu64, _ := strconv.ParseInt(r.URL.Query().Get("ismu"), 10, 32)
	mu := int32(mu64)
	ruleID, _ := strconv.ParseUint(r.URL.Query().Get("relay_rule"), 10, 64)

	switch n.Sort {
	case sub.SortSS2022, sub.SortVLESS, sub.SortAnyTLS:
		if !sub.NodeAllowed(u.User, n) || u.Enable != 1 || !u.ExpireIn.After(time.Now()) || u.TransferEnable <= u.U+u.D {
			return
		}
		p, err := sub.ProtocolProxy(u.User, n)
		if err != nil {
			a.Srv.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		a.userPage(w, r, "user/nodeinfo-custom", map[string]any{"proxy": p, "uri": p.URI()})
	case sub.SortSS, sub.SortRelay, sub.SortSSV2Ray:
		if !nodeAccessible(u.User, n) {
			return
		}
		data := map[string]any{"mu": mu}
		ssrCan, err := a.Sub.SSRCanConnectMu(ctx, u.User, mu)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		ssCan, err := a.Sub.SSCanConnectMu(ctx, u.User, mu)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		data["ssr_prefer"] = ssrCan
		if ssrCan {
			it, err := a.Sub.GetItem(ctx, u.User, n, mu, ruleID, false)
			if err != nil {
				a.serverError(w, r, err)
				return
			}
			if it == nil {
				ssrCan = false
			} else {
				data["ssr_item"], data["ssr_url"] = it, a.Sub.ItemURL(*it, 0)
			}
		}
		if ssCan {
			it, err := a.Sub.GetItem(ctx, u.User, n, mu, ruleID, true)
			if err != nil {
				a.serverError(w, r, err)
				return
			}
			if it == nil {
				ssCan = false
			} else {
				data["ss_item"], data["ss_url"], data["ss_url_win"] = it, a.Sub.ItemURL(*it, 1), a.Sub.ItemURL(*it, 2)
				data["ss_json_obfs"] = sub.JSONObfs(*it)
			}
		}
		data["ssr_can"], data["ss_can"] = ssrCan, ssCan
		data["method_connect"] = sub.CanMethodConnect(u.Method)
		a.userPage(w, r, "user/nodeinfo", data)
	case 1, 2, 5:
		if u.Class < n.NodeClass || (u.NodeGroup != n.NodeGroup && n.NodeGroup != 0) {
			return
		}
		kind, page := "VPN", "user/nodeinfovpn"
		switch n.Sort {
		case 2:
			kind, page = "SSH", "user/nodeinfossh"
		case 5:
			kind, page = "Anyconnect", "user/nodeinfoanyconnect"
		}
		esc := template.HTMLEscapeString
		show := kind + " 信息<br>地址：" + esc(n.Server) + "<br>用户名：" + esc(radiusUserName(u.Email)) +
			"<br>密码：" + esc(u.Passwd) + "<br>支持方式：" + esc(n.Method) + "<br>备注：" + esc(n.Info)
		a.userPage(w, r, page, map[string]any{"json_show": template.HTML(show)}) //nolint:gosec // 各字段均已转义
	default:
		_, _ = w.Write([]byte("微笑"))
	}
}

// radiusUserName 对应 Radius::GetUserName：邮箱去掉 @ 和 . 作为 Radius 用户名。
func radiusUserName(email string) string {
	return strings.NewReplacer("@", "", ".", "").Replace(email)
}

// nodeAjax 对应 UserController::nodeAjax：节点列表页展开节点时加载的图表。
//
// 原版返回带内联脚本的 HTML 片段，严格 CSP 下无法执行；这里只返回图表容器和 JSON 数据，
// 由 /assets/js/node-charts.js 绘制同样的图表（K15）。
func (a *App) nodeAjax(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 32)
	if err != nil {
		return
	}
	n, err := a.Q.GetNode(ctx, uint32(id))
	if isNoRows(err) {
		return
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}
	loads, err := a.Q.ListNodeLoadSamples(ctx, n.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	alive, err := a.Q.ListNodeAliveSamples(ctx, n.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	tests, err := a.Q.ListNodeSpeedtests(ctx, n.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	uptime := "暂无数据"
	if last, err := a.Q.LastNodeInfo(ctx, n.ID); err == nil {
		uptime = util.SecondsToTime(int64(last.Uptime))
	} else if !isNoRows(err) {
		a.serverError(w, r, err)
		return
	}
	onlineCount, err := a.Q.CountNodeOnlineLogsSince(ctx, store.CountNodeOnlineLogsSinceParams{NodeID: n.ID, LogTime: time.Now().Unix() - 86400})
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	type point struct {
		X int64   `json:"x"`
		Y float64 `json:"y"`
	}
	firstNum := func(s string) float64 {
		f, _ := strconv.ParseFloat(strings.SplitN(s, " ", 2)[0], 64)
		return f
	}
	payload := map[string]any{
		"id":      n.ID,
		"prefix":  strings.SplitN(n.Name, " - ", 2)[0],
		"uptime":  uptime,
		"up_rate": float64(onlineCount) / 1440,
	}
	lp := make([]point, 0, len(loads))
	for _, x := range loads {
		lp = append(lp, point{x.LogTime * 1000, firstNum(x.Load)})
	}
	ap := make([]point, 0, len(alive))
	for _, x := range alive {
		ap = append(ap, point{x.LogTime * 1000, float64(x.OnlineUser)})
	}
	series := map[string][]point{}
	for _, t := range tests {
		ms := t.Datetime * 1000
		for k, v := range map[string]string{
			"telecom_ping": t.Telecomping, "unicom_ping": t.Unicomping, "cmcc_ping": t.Cmccping,
			"telecom_up": t.Telecomeupload, "telecom_down": t.Telecomedownload,
			"unicom_up": t.Unicomupload, "unicom_down": t.Unicomdownload,
			"cmcc_up": t.Cmccupload, "cmcc_down": t.Cmccdownload,
		} {
			series[k] = append(series[k], point{ms, firstNum(v)})
		}
	}
	payload["load"], payload["alive"], payload["speedtest"] = lp, ap, series
	js, err := json.Marshal(payload)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, "user/nodeajax", map[string]any{"id": n.ID, "chart_json": template.JS(js)}) //nolint:gosec // json.Marshal 已转义 < > &
}
