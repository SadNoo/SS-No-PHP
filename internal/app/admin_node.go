package app

import (
	"context"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/cloudflare"
	"github.com/SadNoo/SS-No-PHP/internal/dec"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/sub"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

func (a *App) adminNodeRoutes(r chi.Router) {
	r.Get("/node", a.adminTablePage("admin/node/index", newTable("node/ajax", []string{"op", "id", "name", "sort"},
		"op", "操作", "id", "ID", "name", "节点名称", "type", "显示与隐藏", "sort", "类型", "server", "节点地址",
		"node_ip", "节点IP", "info", "节点信息", "status", "状态", "traffic_rate", "流量比率", "node_group", "节点群组",
		"node_class", "节点等级", "node_speedlimit", "节点限速/Mbps", "node_bandwidth", "已走流量/GB",
		"node_bandwidth_limit", "流量限制/GB", "bandwidthlimit_resetday", "流量重置日", "node_heartbeat", "上一次活跃时间",
		"custom_method", "自定义加密", "custom_rss", "自定义协议以及混淆", "mu_only", "只启用单端口多用户")))
	r.Get("/node/create", a.adminNodeCreate)
	r.Post("/node", a.adminNodeSave)
	r.Get("/node/{id}/edit", a.adminNodeEdit)
	r.Put("/node/{id}", a.adminNodeSave)
	r.Delete("/node", a.adminNodeDelete)
	r.Post("/node/ajax", a.adminNodeAjax)
}

var nodeSortNames = map[string]string{
	"0": "Shadowsocks", "1": "VPN/Radius基础", "2": "SSH", "5": "Anyconnect", "9": "Shadowsocks - 单端口多用户",
	"10": "Shadowsocks - 中转", "11": "V2Ray 节点", "12": "V2Ray - 中转", "13": "Shadowsocks - V2Ray-Plugin",
	"14": "SS2022", "15": "VLESS REALITY", "16": "AnyTLS",
}

func onOff(v string) string {
	if v == "1" {
		return "启用"
	}
	return "关闭"
}

// adminNodeAjax 对应 Admin\NodeController::ajax。
func (a *App) adminNodeAjax(w http.ResponseWriter, r *http.Request) {
	cols := []string{"op", "id", "name", "type", "sort", "server", "node_ip", "info", "status", "traffic_rate", "node_group",
		"node_class", "node_speedlimit", "node_bandwidth", "node_bandwidth_limit", "bandwidthlimit_resetday", "node_heartbeat",
		"custom_method", "custom_rss", "mu_only"}
	a.dataTable(w, r, tableSpec{
		Query:   "SELECT id AS op, " + strings.Join(cols[1:], ", ") + " FROM ss_node",
		Columns: cols,
		HTML:    []string{"op"},
		RowID:   true,
		Format: func(row map[string]string) map[string]string {
			reserved := row["sort"] == "999"
			op := `<a class="btn btn-brand" disabled>编辑</a>`
			if !reserved {
				op = editButton("/admin/node/" + row["id"] + "/edit")
			}
			op += "\n" + callButton("btn-brand-accent", "delete_modal_show", row["id"], "删除", reserved)
			sortName, ok := nodeSortNames[row["sort"]]
			if !ok {
				sortName = "系统保留"
			}
			typ := "隐藏"
			if row["type"] == "1" {
				typ = "显示"
			}
			bw, _ := strconv.ParseFloat(row["node_bandwidth"], 64)
			bwl, _ := strconv.ParseFloat(row["node_bandwidth_limit"], 64)
			return map[string]string{
				"op": op, "sort": sortName, "type": typ,
				"node_bandwidth": util.PHPFloat(bw / util.GB), "node_bandwidth_limit": util.PHPFloat(bwl / util.GB),
				"custom_method": onOff(row["custom_method"]), "custom_rss": onOff(row["custom_rss"]), "mu_only": onOff(row["mu_only"]),
				"node_heartbeat": a.fmtUnix(row["node_heartbeat"]),
			}
		},
	})
}

// nodeSort 是后台节点类型下拉框的一项。
type nodeSort struct {
	Value int32
	Name  string
}

// nodeSorts 是可以选择的节点类型（当前使用的四种协议）；其他类型已停用，不能新建或改成这些类型。
var nodeSorts = []nodeSort{{sub.SortV2Ray, "VMess（V2Ray）"}, {sub.SortSS2022, "SS2022"}, {sub.SortVLESS, "VLESS REALITY"}, {sub.SortAnyTLS, "AnyTLS"}}

// retiredSortNames 是已停用的节点类型。编辑这类旧节点时保留原类型，避免保存时被改掉。
var retiredSortNames = map[int32]string{0: "Shadowsocks", 1: "VPN/Radius基础", 2: "SSH", 5: "Anyconnect", 9: "Shadowsocks 单端口多用户",
	10: "Shadowsocks 中转", 12: "V2Ray 中转", 13: "Shadowsocks V2Ray-Plugin"}

// activeNodeSortValues 返回当前支持的节点类型（后台可选的类型）。
func activeNodeSortValues() []int32 {
	out := make([]int32, 0, len(nodeSorts))
	for _, s := range nodeSorts {
		out = append(out, s.Value)
	}
	return out
}

func activeNodeSort(sort int32) bool {
	for _, s := range nodeSorts {
		if s.Value == sort {
			return true
		}
	}
	return false
}

// adminNodeCreate 显示添加节点页面。
func (a *App) adminNodeCreate(w http.ResponseWriter, r *http.Request) {
	a.userPage(w, r, "admin/node/create", map[string]any{"node_sorts": nodeSorts})
}

func (a *App) adminNodeEdit(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(chi.URLParam(r, "id"), 10, 32)
	n, err := a.Q.GetNode(r.Context(), uint32(id))
	if isNoRows(err) {
		a.Srv.NotFound(w, r)
		return
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}
	// 节点地址拆成原节点配置与外部中转，分别显示在“节点地址”和“中转地址/中转端口”输入框中。
	base, relay, _ := sub.SplitRelay(n.Server, false)
	data := map[string]any{"node": n, "server_base": base, "relay_host": "", "relay_port": "", "node_sorts": nodeSorts, "retired_sort": ""}
	if !activeNodeSort(n.Sort) {
		name, ok := retiredSortNames[n.Sort]
		if !ok {
			name = "类型 " + strconv.Itoa(int(n.Sort))
		}
		data["retired_sort"] = name + "（已停用）"
	}
	if relay != nil {
		data["relay_host"], data["relay_port"] = relay.Host, strconv.Itoa(relay.Port)
	}
	a.userPage(w, r, "admin/node/edit", data)
}

var ipv4Re = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)

// lookupIPv4 解析域名的 IPv4 地址（测试时替换）。
var lookupIPv4 = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip4", host)
}

// gethostbyname 对应 PHP 的 gethostbyname：解析域名的 IPv4 地址，失败时原样返回。
func gethostbyname(ctx context.Context, host string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := lookupIPv4(ctx, host)
	if err != nil || len(ips) == 0 {
		return host
	}
	return ips[0].String()
}

func atoi32(s string) int32 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	return int32(v)
}

// adminNodeSave 对应 Admin\NodeController::add / update。
func (a *App) adminNodeSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	server := strings.TrimSpace(param(r, "server"))
	sort := atoi32(param(r, "sort"))
	server, msg := composeNodeServer(sort, server, param(r, "relay_host"), param(r, "relay_port"))
	if msg != "" {
		a.json(w, fail(msg))
		return
	}
	var old *store.SsNode
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		id, _ := strconv.ParseUint(idStr, 10, 32)
		n, err := a.Q.GetNode(ctx, uint32(id))
		if err != nil {
			a.json(w, fail("修改失败：节点不存在"))
			return
		}
		old = &n
	}
	// 只能新建当前使用的四种类型；已停用类型的旧节点可以保持原类型修改其他设置。
	if !activeNodeSort(sort) && (old == nil || old.Sort != sort) {
		a.json(w, fail("节点类型只能是 VMess、SS2022、VLESS REALITY 或 AnyTLS"))
		return
	}
	method := param(r, "method")
	if sort >= sub.SortSS2022 && sort <= sub.SortAnyTLS {
		c, err := sub.ParseProtocol(sort, server)
		if err != nil {
			a.json(w, fail(err.Error()))
			return
		}
		method = c.Method
	}
	nodeIP := ""
	if nodeIPSorts[sort] {
		var msg string
		if nodeIP, msg = a.resolveNodeIP(ctx, nodeHost(server), param(r, "node_ip"), old); msg != "" {
			a.json(w, fail(msg))
			return
		}
	}
	rate, _ := strconv.ParseFloat(param(r, "rate"), 64)
	speed, err := dec.Parse(param(r, "node_speedlimit"))
	if err != nil {
		speed = 0
	}
	limitGB, _ := strconv.ParseFloat(param(r, "node_bandwidth_limit"), 64)
	n := store.SsNode{
		Name: param(r, "name"), Type: atoi32(param(r, "type")), Server: server, Method: method, Info: param(r, "info"),
		Status: param(r, "status"), Sort: sort, CustomMethod: int8(atoi32(param(r, "custom_method"))), TrafficRate: rate, //nolint:gosec // 0 或 1
		NodeClass: atoi32(param(r, "class")), NodeSpeedlimit: speed, NodeBandwidthLimit: int64(limitGB * util.GB),
		BandwidthlimitResetday: atoi32(param(r, "bandwidthlimit_resetday")), NodeIp: nodeIP, NodeGroup: atoi32(param(r, "group")),
		CustomRss: atoi32(param(r, "custom_rss")), MuOnly: atoi32(param(r, "mu_only")),
	}
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		id, _ := strconv.ParseUint(idStr, 10, 32)
		n.ID = uint32(id)
		err := a.tx(ctx, func(q *store.Queries) error {
			cnt, err := q.UpdateNode(ctx, store.UpdateNodeParams{
				Name: n.Name, Type: n.Type, Server: n.Server, Method: n.Method, Info: n.Info, Status: n.Status, Sort: n.Sort,
				CustomMethod: n.CustomMethod, TrafficRate: n.TrafficRate, NodeClass: n.NodeClass, NodeSpeedlimit: n.NodeSpeedlimit,
				NodeBandwidthLimit: n.NodeBandwidthLimit, BandwidthlimitResetday: n.BandwidthlimitResetday, NodeIp: n.NodeIp,
				NodeGroup: n.NodeGroup, CustomRss: n.CustomRss, MuOnly: n.MuOnly, ID: n.ID,
			})
			if err != nil {
				return err
			}
			if cnt == 0 {
				if _, err := q.GetNode(ctx, n.ID); err != nil {
					return err
				}
			}
			// 原版 Tools::updateRelayRuleIp：目标节点 IP 变化后，更新以它为终点的中转规则。
			if n.Sort == 0 || (n.Sort >= 10 && n.Sort <= 13) {
				rules, err := q.ListRelayByDist(ctx, int32(n.ID)) //nolint:gosec // 节点 ID 范围很小
				if err != nil {
					return err
				}
				for _, rule := range rules {
					src, _ := q.GetNode(ctx, uint32(max(rule.SourceNodeID, 0))) //nolint:gosec // 已保证非负
					if err := q.UpdateRelayDistIP(ctx, store.UpdateRelayDistIPParams{DistIp: relayDistIP(src, n.NodeIp), ID: rule.ID}); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if isNoRows(err) {
			a.json(w, fail("修改失败"))
			return
		}
		if err != nil {
			a.jsonError(w, r, err)
			return
		}
		if n.Sort == 1 {
			a.radiusNas(ctx, nodeIPPrimary(n.NodeIp), true)
		}
		a.tg("节点信息被修改~"+n.Name, false)
		a.json(w, ok("修改成功"))
		return
	}
	if _, err := a.Q.InsertNode(ctx, store.InsertNodeParams{
		Name: n.Name, Type: n.Type, Server: n.Server, Method: n.Method, Info: n.Info, Status: n.Status, Sort: n.Sort,
		CustomMethod: n.CustomMethod, TrafficRate: n.TrafficRate, NodeClass: n.NodeClass, NodeSpeedlimit: n.NodeSpeedlimit,
		NodeBandwidthLimit: n.NodeBandwidthLimit, BandwidthlimitResetday: n.BandwidthlimitResetday, NodeIp: n.NodeIp,
		NodeGroup: n.NodeGroup, CustomRss: n.CustomRss, MuOnly: n.MuOnly,
	}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	if n.Sort == 1 {
		a.radiusNas(ctx, nodeIPPrimary(n.NodeIp), true)
	}
	a.cloudflareSync(n)
	a.tg("新节点添加~"+n.Name, false)
	a.json(w, ok("节点添加成功"))
}

func (a *App) adminNodeDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(param(r, "id"), 10, 32)
	n, err := a.Q.GetNode(r.Context(), uint32(id))
	if err != nil {
		a.json(w, fail("删除失败"))
		return
	}
	if cnt, err := a.Q.DeleteNode(r.Context(), n.ID); err != nil || cnt == 0 {
		a.json(w, fail("删除失败"))
		return
	}
	if n.Sort == 1 {
		a.radiusNas(r.Context(), nodeIPPrimary(n.NodeIp), false)
	}
	a.tg("节点被删除~"+n.Name, false)
	a.json(w, ok("删除成功"))
}

// cloudflareSync 对应添加节点时的 CloudflareDriver::updateRecord：把节点域名的 A 记录指向节点 IP。
//
// 原版用“域名去掉 .cloudflare_name 后的部分”查询记录，Cloudflare 按完整域名匹配，查不到已有记录，
// 每次都会新增一条重复的 A 记录；节点地址不在该域名下时也会新增。这里使用完整域名，且只处理该域名下的节点。
func (a *App) cloudflareSync(n store.SsNode) {
	cf := a.Cfg.DNS
	if !cf.CloudflareEnable || cf.CloudflareKey == "" || cf.CloudflareName == "" {
		return
	}
	host := strings.ToLower(nodeHost(n.Server))
	ip := nodeIPPrimary(n.NodeIp)
	if !strings.HasSuffix(host, "."+strings.ToLower(cf.CloudflareName)) || !ipv4Re.MatchString(ip) {
		return
	}
	c := a.CF
	if c == nil {
		c = cloudflare.New(cf.CloudflareEmail, cf.CloudflareKey, cf.CloudflareName)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := c.UpdateA(ctx, host, ip); err != nil {
			a.Log.Warn("更新 Cloudflare DNS 失败", "host", host, "err", err)
			return
		}
		a.Log.Info("已更新 Cloudflare DNS", "host", host, "ip", ip)
	}()
}

// resolveNodeIP 计算保存节点时的节点 IP（规则见 nodeip.go）。返回非空 msg 表示不能保存。
//   - 节点地址为 IP：使用管理员填写的节点 IP，留空则等于节点地址。
//   - 节点地址为域名：第一项为解析结果，第二项起保留管理员填写的手动项；
//     解析失败时使用填写的第一项或原有的主 IP（保留原有效 IP），都没有则提示失败，不会把域名写入节点 IP。
func (a *App) resolveNodeIP(ctx context.Context, host, form string, old *store.SsNode) (string, string) {
	if host == "" {
		return "", "节点地址不能为空"
	}
	entries := nodeIPEntries(form)
	if err := validateNodeIP(entries); err != nil {
		return "", err.Error()
	}
	if net.ParseIP(host) != nil {
		if len(entries) == 0 {
			return host, ""
		}
		return strings.Join(entries, ","), ""
	}
	formIP := strings.Join(entries, ",")
	primary := gethostbyname(ctx, host)
	if primary == host || net.ParseIP(primary) == nil {
		switch {
		case nodeIPPrimary(formIP) != "":
			primary = nodeIPPrimary(formIP)
		case old != nil && nodeIPPrimary(old.NodeIp) != "":
			primary = nodeIPPrimary(old.NodeIp)
		default:
			return "", "获取节点IP失败：无法解析节点地址 " + host + "，请检查地址，或在“节点 IP”中手动填写"
		}
	}
	// 主 IP 取解析结果；表单中的 # 专用项与其余手动项原样保留。
	return nodeIPWithPrimary(formIP, primary), ""
}

// relaySorts 是支持外部中转的节点类型（VMess、SS2022、VLESS、AnyTLS）。
var relaySorts = map[int32]bool{11: true, 12: true, 14: true, 15: true, 16: true}

// composeNodeServer 把“节点地址”与“中转地址/中转端口”两个输入框拼成保存的节点地址（原节点配置;||地址;端口）。
// 中转输入框留空时节点地址可以已带中转后缀（兼容 “||” 与 “;||” 写法，后缀必须合法），保存时统一写成 “;||”，
// 原节点配置原样保留（只在本次保存的这一条记录上统一，不批量改写历史数据）。
func composeNodeServer(sort int32, server, relayHost, relayPort string) (string, string) {
	relay, err := sub.ParseRelayInput(relayHost, relayPort)
	if err != nil {
		return "", err.Error()
	}
	if strings.Contains(server, "||") {
		if relay != nil {
			return "", "节点地址中已包含中转后缀，请只在“中转地址/中转端口”中填写"
		}
		base, r, err := sub.SplitRelay(server, true)
		if err != nil {
			return "", err.Error()
		}
		if !relaySorts[sort] {
			return "", "该节点类型不支持外部中转"
		}
		server = sub.JoinRelay(base, r)
	}
	if relay != nil {
		if !relaySorts[sort] {
			return "", "该节点类型不支持外部中转（支持 VMess、SS2022、VLESS、AnyTLS）"
		}
		server = sub.JoinRelay(server, relay)
	}
	if nodeIPSorts[sort] {
		if err := sub.ValidateServerText(server); err != nil {
			return "", err.Error()
		}
	}
	return server, ""
}
