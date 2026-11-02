package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/server"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/sub"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// 节点后端接口（原版 /mod_mu 与旧版 /mu），供 SSR / V2Ray 节点程序拉取用户、上报流量等。
// 输出格式按原版 json_encode 的规则生成，与原版逐字一致，节点程序无需修改。

func (a *App) nodeAPIRoutes(r chi.Router) {
	r.Route("/mod_mu", func(r chi.Router) {
		// WebAPI v2 自行完成认证（密钥 + 路径中的节点 ID + 该节点的来源 IP），不经过旧接口的中间件：
		// 旧中间件从查询参数 node_id 判断节点，与 v2 绑定路径节点 ID 的规则不同。
		r.Get("/v2/nodes/{id}/config", a.v2NodeConfig)
		r.Get("/v2/nodes/{id}/users", a.v2NodeUsers)
		r.Get("/v2/nodes/{id}/policy", a.v2NodePolicy)
		r.Post("/v2/nodes/{id}/traffic", a.v2Traffic)
		r.Post("/v2/nodes/{id}/alive_ip", a.v2AliveIP)
		r.Group(func(r chi.Router) {
			r.Use(a.modMuAuth)
			a.legacyModMuRoutes(r)
		})
	})
	r.Route("/mu", func(r chi.Router) {
		r.Use(a.muAuth)
		r.Get("/users", a.legacyMuUsers)
		r.Post("/users/{id}/traffic", a.legacyMuAddTraffic)
		r.Post("/nodes/{id}/online_count", a.legacyMuOnlineCount)
		r.Post("/nodes/{id}/info", a.legacyMuNodeInfo)
	})
}

// legacyModMuRoutes 是原版 /mod_mu 接口（行为保持不变）。
func (a *App) legacyModMuRoutes(r chi.Router) {
	r.Get("/nodes/{id}/info", a.muGetInfo)
	r.Get("/users", a.muUsers)
	r.Post("/users/traffic", a.muAddTraffic)
	r.Post("/users/aliveip", a.muAddAliveIP)
	r.Post("/users/detectlog", a.muAddDetectLog)
	r.Post("/nodes/{id}/info", a.muNodeInfo)
	r.Get("/nodes", a.muAllNodes)
	r.Post("/nodes/config", a.muGetConfig)
	r.Get("/func/detect_rules", a.muDetectRules)
	r.Get("/func/relay_rules", a.muRelayRules)
	r.Post("/func/block_ip", a.muAddBlockIP)
	r.Get("/func/block_ip", a.muBlockIPs)
	r.Get("/func/unblock_ip", a.muUnblockIPs)
	r.Post("/func/speedtest", a.muAddSpeedtest)
	r.Get("/func/autoexec", a.muGetAutoexec)
	r.Post("/func/autoexec", a.muAddAutoexec)
	r.Get("/func/ping", a.muPing)
}

// writePHPJSON 输出与原版 json_encode($res) 相同的 JSON。
func writePHPJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = io.WriteString(w, sub.EncodeJSON(v, 0)) //nolint:gosec // JSON 响应，Content-Type 为 application/json
}

func retData(ret int, data any) *sub.OMap {
	return sub.NewOMap().Set("ret", ret).Set("data", data)
}

// validMuKey 校验节点密钥（原版 Config::getMuKey：mu_key 逗号分隔 + mu_key_list）。
// 原版未设置 muKeyList 时默认包含一个全角空格作为密钥，且 mu_key 为空时空字符串也能通过校验（K36），这里都不接受。
func (a *App) validMuKey(key string) bool {
	if key == "" {
		return false
	}
	keys := append(strings.Split(a.Cfg.Security.MuKey, ","), a.Cfg.Security.MuKeyList...)
	ok := false
	for _, k := range keys {
		if k != "" && subtle.ConstantTimeCompare([]byte(k), []byte(key)) == 1 {
			ok = true
		}
	}
	return ok
}

// nodeIPMatches 判断 IP 是否为某个节点的地址。node_ip 可以是逗号分隔的多个地址，每个地址可带 #起点ID 后缀。
// 原版用 LIKE 'IP%' 前缀匹配，1.2.3.4 能匹配 1.2.3.45（K04）。
func nodeIPMatches(nodeIP, ip string) bool {
	for _, part := range strings.Split(nodeIP, ",") {
		addr, _, _ := strings.Cut(strings.TrimSpace(part), "#")
		if addr != "" && addr == ip {
			return true
		}
	}
	return false
}

// nodeByIP 按来源 IP 查找节点（原版 node_id = 0 时 Node::where('node_ip', REMOTE_ADDR)）。
func (a *App) nodeByIP(ctx context.Context, ip string) (*store.SsNode, error) {
	nodes, err := a.Q.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		if nodeIPMatches(nodes[i].NodeIp, ip) {
			return &nodes[i], nil
		}
	}
	return nil, nil
}

func isLoopback(ip string) bool { return ip == "127.0.0.1" || ip == "::1" }

// modMuAuth 对应 Middleware\Mod_Mu。
func (a *App) modMuAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, has := r.URL.Query()["key"]
		if !has {
			writePHPJSON(w, retData(0, "null"))
			return
		}
		ip := server.ClientIP(r)
		keyOK := a.validMuKey(key[0])
		ok, err := a.authorizeNode(r.Context(), ip, r.URL.Query().Get("node_id"), keyOK)
		if err != nil {
			a.Log.Error("节点认证查询失败", "err", err)
			writePHPJSON(w, retData(0, "token or source is invalid"))
			return
		}
		if !ok {
			writePHPJSON(w, retData(0, "token or source is invalid, Your ip address is "+ip))
			return
		}
		if !keyOK {
			writePHPJSON(w, retData(0, "token or source is invalid"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// muAuth 对应 Middleware\Mu（旧版接口，返回 msg 字段）。
func (a *App) muAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, has := r.URL.Query()["key"]
		if !has {
			writePHPJSON(w, sub.NewOMap().Set("ret", 0).Set("msg", "key is null"))
			return
		}
		if !a.validMuKey(key[0]) {
			writePHPJSON(w, sub.NewOMap().Set("ret", 0).Set("msg", "token or source is invalid"))
			return
		}
		ok, err := a.authorizeNode(r.Context(), server.ClientIP(r), r.URL.Query().Get("node_id"), true)
		if err != nil || !ok {
			writePHPJSON(w, sub.NewOMap().Set("ret", 0).Set("msg", "token or source is invalid"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestNode 取得请求中的节点：node_id 为 0 时按来源 IP 查找。找不到时返回 nil（原版此处会因空对象报错）。
func (a *App) requestNode(r *http.Request, idStr string) (*store.SsNode, error) {
	ctx := r.Context()
	if idStr == "0" {
		return a.nodeByIP(ctx, server.ClientIP(r))
	}
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return nil, nil
	}
	n, err := a.Q.GetNode(ctx, uint32(id))
	if isNoRows(err) {
		return nil, nil
	}
	return &n, err
}

// nodeParam 对应 Slim 的 $request->getParam：JSON 请求体、表单、查询参数依次查找。
type nodeRequest struct {
	body map[string]any
	r    *http.Request
}

func parseNodeRequest(r *http.Request) *nodeRequest {
	nr := &nodeRequest{r: r}
	if strings.Contains(r.Header.Get("Content-Type"), "json") {
		dec := json.NewDecoder(io.LimitReader(r.Body, 16<<20))
		dec.UseNumber()
		_ = dec.Decode(&nr.body)
	} else {
		_ = r.ParseForm()
	}
	return nr
}

func (nr *nodeRequest) param(key string) string {
	if v, ok := nr.body[key]; ok {
		return anyString(v)
	}
	return nr.r.FormValue(key)
}

var phpKeyRe = regexp.MustCompile(`^data\[(\d+)\]\[(\w+)\]$`)

// data 返回请求中的 data 数组（JSON 数组，或 PHP 风格表单 data[0][user_id]=...）。
func (nr *nodeRequest) data() []map[string]any {
	if raw, ok := nr.body["data"]; ok {
		var out []map[string]any
		switch x := raw.(type) {
		case []any:
			for _, e := range x {
				if m, ok := e.(map[string]any); ok {
					out = append(out, m)
				}
			}
		case map[string]any: // PHP 数组也可能以对象形式发送
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if m, ok := x[k].(map[string]any); ok {
					out = append(out, m)
				}
			}
		}
		return out
	}
	byIdx := map[int]map[string]any{}
	for k, v := range nr.r.PostForm {
		if m := phpKeyRe.FindStringSubmatch(k); m != nil && len(v) > 0 {
			i, _ := strconv.Atoi(m[1])
			if byIdx[i] == nil {
				byIdx[i] = map[string]any{}
			}
			byIdx[i][m[2]] = v[0]
		}
	}
	idx := make([]int, 0, len(byIdx))
	for i := range byIdx {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	out := make([]map[string]any, 0, len(idx))
	for _, i := range idx {
		out = append(out, byIdx[i])
	}
	return out
}

func anyString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "1"
		}
		return ""
	}
	return fmt.Sprint(v)
}

func anyFloat(v any) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(anyString(v)), 64)
	return f
}

func anyInt(v any) int64 { return int64(anyFloat(v)) }

// muUserJSON 对应原版 Tools::keyFilter 后的用户（按原版字段顺序与类型：u、d、node_speedlimit 为浮点，port、is_multi_user 为整数）。
// disconnect_ip 只输出合法项（逗号分隔、标准写法），非法项计入 skip。
func (a *App) muUserJSON(u store.User, skip *blockedIPSkip) *sub.OMap {
	return sub.NewOMap().
		Set("id", u.ID).
		Set("email", u.Email).
		Set("passwd", u.Passwd).
		Set("u", sub.PHPFloat(u.U)).
		Set("d", sub.PHPFloat(u.D)).
		Set("port", u.Port).
		Set("method", u.Method).
		Set("node_speedlimit", sub.PHPFloat(u.NodeSpeedlimit.Float())).
		Set("protocol", u.Protocol).
		Set("protocol_param", u.ProtocolParam).
		Set("obfs", u.Obfs).
		Set("obfs_param", u.ObfsParam).
		Set("forbidden_ip", u.ForbiddenIp).
		Set("forbidden_port", u.ForbiddenPort).
		Set("disconnect_ip", strings.Join(nodeBlockedIPs(u, skip), ",")).
		Set("is_multi_user", u.IsMultiUser).
		Set("uuid", model.NewUser(u, a.Loc).UUID())
}

// nodeUsers 节点可用的用户列表；节点流量超限时返回 nil。
func (a *App) nodeUsers(ctx context.Context, n store.SsNode, now time.Time) ([]store.User, bool, error) {
	if err := a.Q.TouchNodeHeartbeat(ctx, store.TouchNodeHeartbeatParams{NodeHeartbeat: now.Unix(), ID: n.ID}); err != nil {
		return nil, false, err
	}
	users, err := a.Q.ListNodeUsers(ctx, store.ListNodeUsersParams{NodeClass: n.NodeClass, NodeGroup: n.NodeGroup, Now: now.UTC()})
	if err != nil {
		return nil, false, err
	}
	if n.NodeBandwidthLimit != 0 && n.NodeBandwidthLimit < n.NodeBandwidth {
		return nil, false, nil
	}
	var out []store.User
	for _, u := range users {
		if u.TransferEnable > u.U+u.D {
			out = append(out, u)
		}
	}
	return out, true, nil
}

// muUsers 对应 Mod_Mu\UserController::index：节点拉取用户列表（同时记录节点心跳）。
func (a *App) muUsers(w http.ResponseWriter, r *http.Request) {
	n, err := a.requestNode(r, r.URL.Query().Get("node_id"))
	if err != nil {
		a.muError(w, r, err)
		return
	}
	if n == nil {
		writePHPJSON(w, sub.NewOMap().Set("ret", 0))
		return
	}
	users, ok, err := a.nodeUsers(r.Context(), *n, time.Now())
	if err != nil {
		a.muError(w, r, err)
		return
	}
	if !ok {
		writePHPJSON(w, retData(1, nil))
		return
	}
	list := make([]any, 0, len(users))
	var skip blockedIPSkip
	for _, u := range users {
		list = append(list, a.muUserJSON(u, &skip))
	}
	a.reportBlockedIPSkip(n.ID, "mod_mu/users", &skip)
	writePHPJSON(w, retData(1, list))
}

// muError 记录错误并返回原版“更新失败”的格式。
func (a *App) muError(w http.ResponseWriter, r *http.Request, err error) {
	a.Log.Error("节点接口处理失败", "path", r.URL.Path, "err", err)
	writePHPJSON(w, retData(0, "update failed"))
}

// muAddTraffic 对应 Mod_Mu\UserController::addTraffic：节点上报用户流量。
//
// 原版逐个用户先读后写，并发上报会丢失流量，中途失败时已处理的部分不会回滚，节点重试后会重复计费（K01）。
// 这里在一个事务中原子累加，全部成功或全部不生效。
func (a *App) muAddTraffic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nr := parseNodeRequest(r)
	n, err := a.requestNode(r, r.URL.Query().Get("node_id"))
	if err != nil {
		a.muError(w, r, err)
		return
	}
	if n == nil {
		writePHPJSON(w, sub.NewOMap().Set("ret", 0))
		return
	}
	data := nr.data()
	// 按用户 ID 排序加锁，避免多个节点同时上报时死锁。
	sort.SliceStable(data, func(i, j int) bool { return anyInt(data[i]["user_id"]) < anyInt(data[j]["user_id"]) })
	now := time.Now().Unix()
	rate := n.TrafficRate
	err = a.tx(ctx, func(q *store.Queries) error {
		var total int64
		for _, log := range data {
			u, d, uid := anyInt(log["u"]), anyInt(log["d"]), anyInt(log["user_id"])
			if uid <= 0 || uid > math.MaxUint32 {
				continue
			}
			rows, err := q.AddUserTrafficRows(ctx, store.AddUserTrafficRowsParams{
				U: int64(math.Round(float64(u) * rate)), D: int64(math.Round(float64(d) * rate)), T: now, ID: uint32(uid),
			})
			if err != nil {
				return err
			}
			if rows == 0 {
				continue // 用户已删除
			}
			total += u + d
			if err := q.InsertTrafficLog(ctx, store.InsertTrafficLogParams{
				UserID: uint32(uid), U: u, D: d, NodeID: n.ID, Rate: rate,
				Traffic: util.FlowAutoShow(float64(u+d) * rate), LogTime: now,
			}); err != nil {
				return err
			}
		}
		if err := q.AddNodeBandwidth(ctx, store.AddNodeBandwidthParams{NodeBandwidth: total, ID: n.ID}); err != nil {
			return err
		}
		return q.InsertNodeOnlineLog(ctx, store.InsertNodeOnlineLogParams{NodeID: n.ID, OnlineUser: int32(len(data)), LogTime: now}) //nolint:gosec // 上报条数有限
	})
	if err != nil {
		a.muError(w, r, err)
		return
	}
	writePHPJSON(w, retData(1, "ok"))
}

// muAddAliveIP 对应 Mod_Mu\UserController::addAliveIp：节点上报用户在线 IP。
// 不是有效 IP 的条目跳过不保存（原版原样保存任意文本）；响应与原版相同，其余条目照常写入。
func (a *App) muAddAliveIP(w http.ResponseWriter, r *http.Request) {
	a.muInsertEach(w, r, func(ctx context.Context, q *store.Queries, n store.SsNode, log map[string]any, now int64) error {
		ip := anyString(log["ip"])
		if _, ok := normalizeIP(ip); !ok {
			a.Log.Debug("节点上报的在线 IP 无效，已跳过", "node", n.ID, "ip", limitLen(ip, 64))
			return nil
		}
		return q.InsertAliveIP(ctx, store.InsertAliveIPParams{Nodeid: n.ID, Userid: uint32(anyInt(log["user_id"])), Ip: limitLen(strings.TrimSpace(ip), 255), Datetime: now}) //nolint:gosec // 用户 ID
	})
}

// muAddDetectLog 对应 Mod_Mu\UserController::addDetectLog：节点上报审计记录。
func (a *App) muAddDetectLog(w http.ResponseWriter, r *http.Request) {
	a.muInsertEach(w, r, func(ctx context.Context, q *store.Queries, n store.SsNode, log map[string]any, now int64) error {
		return q.InsertDetectLog(ctx, store.InsertDetectLogParams{UserID: uint32(anyInt(log["user_id"])), ListID: uint64(anyInt(log["list_id"])), Datetime: now, NodeID: n.ID}) //nolint:gosec // ID
	})
}

// muInsertEach 是“节点上报一组记录”的公共部分：找到节点后在事务中逐条写入。
func (a *App) muInsertEach(w http.ResponseWriter, r *http.Request, insert func(context.Context, *store.Queries, store.SsNode, map[string]any, int64) error) {
	ctx := r.Context()
	nr := parseNodeRequest(r)
	n, err := a.requestNode(r, r.URL.Query().Get("node_id"))
	if err != nil {
		a.muError(w, r, err)
		return
	}
	if n == nil {
		writePHPJSON(w, sub.NewOMap().Set("ret", 0))
		return
	}
	now := time.Now().Unix()
	err = a.tx(ctx, func(q *store.Queries) error {
		for _, log := range nr.data() {
			if err := insert(ctx, q, *n, log, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		a.muError(w, r, err)
		return
	}
	writePHPJSON(w, retData(1, "ok"))
}

// muNodeInfo 对应 Mod_Mu\NodeController::info：节点上报负载与运行时间。
func (a *App) muNodeInfo(w http.ResponseWriter, r *http.Request) {
	nr := parseNodeRequest(r)
	n, err := a.requestNode(r, chi.URLParam(r, "id"))
	if err != nil {
		a.muError(w, r, err)
		return
	}
	if n == nil {
		writePHPJSON(w, retData(0, "update failed"))
		return
	}
	if err := a.Q.InsertNodeInfo(r.Context(), store.InsertNodeInfoParams{
		NodeID: n.ID, Uptime: anyFloat(nr.param("uptime")), Load: limitLen(nr.param("load"), 32), LogTime: time.Now().Unix(),
	}); err != nil {
		a.muError(w, r, err)
		return
	}
	writePHPJSON(w, retData(1, "ok"))
}

// muGetInfo 对应 Mod_Mu\NodeController::get_info：节点读取自身配置。
func (a *App) muGetInfo(w http.ResponseWriter, r *http.Request) {
	n, err := a.requestNode(r, chi.URLParam(r, "id"))
	if err != nil {
		a.muError(w, r, err)
		return
	}
	if n == nil {
		writePHPJSON(w, sub.NewOMap().Set("ret", 0))
		return
	}
	writePHPJSON(w, retData(1, sub.NewOMap().
		Set("node_group", n.NodeGroup).
		Set("node_class", n.NodeClass).
		Set("node_speedlimit", sub.PHPFloat(n.NodeSpeedlimit.Float())).
		Set("traffic_rate", sub.PHPFloat(n.TrafficRate)).
		Set("mu_only", n.MuOnly).
		Set("sort", n.Sort).
		Set("server", sub.RealServer(n.Server)). // 节点后端使用真实地址，不含外部中转后缀
		Set("type", "ss-panel-v3-mod_Uim")))
}

// nodeJSON 对应 Node 模型的 JSON（全部字段，原版字段顺序与类型）。
func nodeJSON(n store.SsNode) *sub.OMap {
	return sub.NewOMap().
		Set("id", n.ID).Set("name", n.Name).Set("type", n.Type).Set("server", sub.RealServer(n.Server)).Set("method", n.Method).
		Set("info", n.Info).Set("status", n.Status).Set("sort", n.Sort).Set("custom_method", n.CustomMethod).
		Set("traffic_rate", sub.PHPFloat(n.TrafficRate)).Set("node_class", n.NodeClass).
		Set("node_speedlimit", sub.PHPFloat(n.NodeSpeedlimit.Float())).Set("node_connector", n.NodeConnector).
		Set("node_bandwidth", n.NodeBandwidth).Set("node_bandwidth_limit", n.NodeBandwidthLimit).
		Set("bandwidthlimit_resetday", n.BandwidthlimitResetday).Set("node_heartbeat", n.NodeHeartbeat).
		Set("node_ip", n.NodeIp).Set("node_group", n.NodeGroup).Set("custom_rss", n.CustomRss).Set("mu_only", n.MuOnly)
}

// muAllNodes 对应 Mod_Mu\NodeController::get_all_info。
func (a *App) muAllNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.Q.ListAPINodes(r.Context())
	if err != nil {
		a.muError(w, r, err)
		return
	}
	list := make([]any, 0, len(nodes))
	for _, n := range nodes {
		list = append(list, nodeJSON(n))
	}
	writePHPJSON(w, retData(1, list))
}

// muGetConfig 对应 Mod_Mu\NodeController::getConfig。原版会把面板数据库的地址、账号和密码返回给任何持有节点密钥的请求（K36），
// 新版停用此接口；节点请使用 WebAPI 模式。
func (a *App) muGetConfig(w http.ResponseWriter, _ *http.Request) {
	writePHPJSON(w, retData(0, "database mode is not supported, please use webapi"))
}

// muDetectRules 对应 Mod_Mu\FuncController::get_detect_logs：审计规则。
func (a *App) muDetectRules(w http.ResponseWriter, r *http.Request) {
	rules, err := a.Q.ListAllDetectRules(r.Context())
	if err != nil {
		a.muError(w, r, err)
		return
	}
	list := make([]any, 0, len(rules))
	for _, x := range rules {
		list = append(list, sub.NewOMap().Set("id", x.ID).Set("name", x.Name).Set("text", x.Text).Set("regex", x.Regex).Set("type", x.Type))
	}
	writePHPJSON(w, retData(1, list))
}

// muRelayRules 对应 Mod_Mu\FuncController::get_relay_rules：以该节点为起点的中转规则。
func (a *App) muRelayRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, err := a.requestNode(r, r.URL.Query().Get("node_id"))
	if err != nil {
		a.muError(w, r, err)
		return
	}
	var sourceSort any
	var id int32
	if n != nil {
		sourceSort, id = n.Sort, int32(n.ID) //nolint:gosec // 节点 ID 范围很小
	}
	rules, err := a.Q.ListRelayBySource(ctx, id)
	if err != nil {
		a.muError(w, r, err)
		return
	}
	nodes, err := a.nodeMap(r)
	if err != nil {
		a.muError(w, r, err)
		return
	}
	list := make([]any, 0, len(rules))
	for _, x := range rules {
		m := sub.NewOMap().Set("id", x.ID).Set("user_id", x.UserID).Set("source_node_id", x.SourceNodeID).
			Set("dist_node_id", x.DistNodeID).Set("dist_ip", x.DistIp).Set("port", x.Port).Set("priority", x.Priority).
			Set("source_node_sort", sourceSort)
		if dn, ok := nodes[uint32(x.DistNodeID)]; ok && x.DistNodeID > 0 { //nolint:gosec // 已判断为正数
			m.Set("dist_node_sort", dn.Sort).Set("dist_node_server", sub.RealServer(dn.Server))
		} else {
			m.Set("dist_node_sort", nil).Set("dist_node_server", nil)
		}
		list = append(list, m)
	}
	writePHPJSON(w, retData(1, list))
}

// muBlockIPs 对应 Mod_Mu\FuncController::get_blockip：最近一分钟内新增的封禁 IP。
func (a *App) muBlockIPs(w http.ResponseWriter, r *http.Request) {
	list, err := a.Q.ListBlockIPsSince(r.Context(), time.Now().Unix()-60)
	if err != nil {
		a.muError(w, r, err)
		return
	}
	out := make([]any, 0, len(list))
	for _, x := range list {
		out = append(out, sub.NewOMap().Set("id", x.ID).Set("nodeid", x.Nodeid).Set("ip", x.Ip).Set("datetime", x.Datetime))
	}
	writePHPJSON(w, retData(1, out))
}

// muUnblockIPs 对应 Mod_Mu\FuncController::get_unblockip：最近一分钟内解封的 IP。
func (a *App) muUnblockIPs(w http.ResponseWriter, r *http.Request) {
	list, err := a.Q.ListUnblockIPsSince(r.Context(), time.Now().Unix()-60)
	if err != nil {
		a.muError(w, r, err)
		return
	}
	out := make([]any, 0, len(list))
	for _, x := range list {
		out = append(out, sub.NewOMap().Set("id", x.ID).Set("ip", x.Ip).Set("datetime", x.Datetime).Set("userid", x.Userid))
	}
	writePHPJSON(w, retData(1, out))
}

// muAddBlockIP 对应 Mod_Mu\FuncController::addBlockIp：节点上报需要封禁的 IP（已存在的跳过）。
func (a *App) muAddBlockIP(w http.ResponseWriter, r *http.Request) {
	a.muInsertEach(w, r, func(ctx context.Context, q *store.Queries, n store.SsNode, log map[string]any, now int64) error {
		ip := limitLen(anyString(log["ip"]), 255)
		if c, err := q.CountBlockIP(ctx, ip); err != nil || c > 0 {
			return err
		}
		return q.InsertBlockIP(ctx, store.InsertBlockIPParams{Nodeid: n.ID, Ip: ip, Datetime: now})
	})
}

// muAddSpeedtest 对应 Mod_Mu\FuncController::addSpeedtest：节点上报测速结果。
func (a *App) muAddSpeedtest(w http.ResponseWriter, r *http.Request) {
	a.muInsertEach(w, r, func(ctx context.Context, q *store.Queries, n store.SsNode, log map[string]any, now int64) error {
		s := func(k string) string { return limitLen(anyString(log[k]), 255) }
		return q.InsertSpeedtest(ctx, store.InsertSpeedtestParams{
			Nodeid: n.ID, Datetime: now,
			Telecomping: s("telecomping"), Telecomeupload: s("telecomeupload"), Telecomedownload: s("telecomedownload"),
			Unicomping: s("unicomping"), Unicomupload: s("unicomupload"), Unicomdownload: s("unicomdownload"),
			Cmccping: s("cmccping"), Cmccupload: s("cmccupload"), Cmccdownload: s("cmccdownload"),
		})
	})
}

// muGetAutoexec 对应 Mod_Mu\FuncController::get_autoexec：最近一分钟内下发、本节点尚未执行的命令。
func (a *App) muGetAutoexec(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, err := a.requestNode(r, r.URL.Query().Get("node_id"))
	if err != nil {
		a.muError(w, r, err)
		return
	}
	if n == nil {
		writePHPJSON(w, sub.NewOMap().Set("ret", 0))
		return
	}
	autos, err := a.Q.ListAutoCommandsSince(ctx, time.Now().Unix()-60)
	if err != nil {
		a.muError(w, r, err)
		return
	}
	done := map[string]bool{}
	if len(autos) > 0 {
		signs := make([]string, 0, len(autos))
		for _, x := range autos {
			signs = append(signs, fmt.Sprintf("%d-%d", n.ID, x.ID))
		}
		got, err := a.Q.ListAutoReceipts(ctx, signs)
		if err != nil {
			a.muError(w, r, err)
			return
		}
		for _, s := range got {
			done[s] = true
		}
	}
	list := make([]any, 0, len(autos))
	for _, x := range autos {
		if done[fmt.Sprintf("%d-%d", n.ID, x.ID)] {
			continue
		}
		list = append(list, sub.NewOMap().Set("id", x.ID).Set("type", x.Type).Set("value", x.Value).Set("sign", x.Sign).Set("datetime", x.Datetime))
	}
	writePHPJSON(w, retData(1, list))
}

// muAddAutoexec 对应 Mod_Mu\FuncController::addAutoexec：节点上报命令执行结果。
// 原版允许节点写入任意类型的记录，任何一台节点（或泄露的节点密钥）都能向所有节点下发命令（K37）；
// 这里只接受执行回执（type = 2），下发命令只能由管理员在后台操作。
func (a *App) muAddAutoexec(w http.ResponseWriter, r *http.Request) {
	a.muInsertEach(w, r, func(ctx context.Context, q *store.Queries, _ store.SsNode, log map[string]any, now int64) error {
		if anyInt(log["type"]) != 2 {
			return nil
		}
		return q.InsertAuto(ctx, store.InsertAutoParams{Type: 2, Value: anyString(log["value"]), Sign: anyString(log["sign"]), Datetime: now})
	})
}

// muPing 对应 Mod_Mu\FuncController::ping。
func (a *App) muPing(w http.ResponseWriter, _ *http.Request) {
	writePHPJSON(w, retData(1, "pong"))
}

// legacyMuUsers 对应旧版 Mu\UserController::index。
func (a *App) legacyMuUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := server.ClientIP(r)
	nodes, err := a.Q.ListAPINodes(ctx)
	if err != nil {
		a.muError(w, r, err)
		return
	}
	var n *store.SsNode
	for i := range nodes {
		if nodes[i].NodeIp == ip {
			n = &nodes[i]
			break
		}
	}
	if n == nil {
		writePHPJSON(w, sub.NewOMap().Set("ret", 0).Set("msg", "node not found"))
		return
	}
	users, ok, err := a.nodeUsers(ctx, *n, time.Now())
	if err != nil {
		a.muError(w, r, err)
		return
	}
	if !ok {
		writePHPJSON(w, retData(1, nil))
		return
	}
	list := make([]any, 0, len(users))
	for _, u := range users {
		list = append(list, sub.NewOMap().
			Set("id", u.ID).Set("email", u.Email).Set("passwd", u.Passwd).Set("t", sub.PHPFloat(u.T)).
			Set("u", sub.PHPFloat(u.U)).Set("d", sub.PHPFloat(u.D)).Set("transfer_enable", sub.PHPFloat(u.TransferEnable)).
			Set("port", u.Port).Set("switch", u.Switch).Set("enable", u.Enable).Set("method", u.Method))
	}
	writePHPJSON(w, sub.NewOMap().Set("ret", 1).Set("msg", "ok").Set("data", list))
}

// legacyMuAddTraffic 对应旧版 Mu\UserController::addTraffic：逐个用户上报流量。
func (a *App) legacyMuAddTraffic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nr := parseNodeRequest(r)
	uid, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 32)
	if err != nil {
		writePHPJSON(w, sub.NewOMap().Set("ret", 0).Set("msg", "update failed"))
		return
	}
	n, err := a.requestNode(r, nr.param("node_id"))
	if err != nil || n == nil {
		writePHPJSON(w, sub.NewOMap().Set("ret", 0).Set("msg", "update failed"))
		return
	}
	u, d := anyInt(nr.param("u")), anyInt(nr.param("d"))
	id32 := uint32(uid) //nolint:gosec // ParseUint 已限制在 32 位范围内
	now := time.Now().Unix()
	err = a.tx(ctx, func(q *store.Queries) error {
		if err := q.AddNodeBandwidth(ctx, store.AddNodeBandwidthParams{NodeBandwidth: u + d, ID: n.ID}); err != nil {
			return err
		}
		rows, err := q.AddUserTrafficRows(ctx, store.AddUserTrafficRowsParams{
			U: int64(math.Round(float64(u) * n.TrafficRate)), D: int64(math.Round(float64(d) * n.TrafficRate)), T: now, ID: id32,
		})
		if err != nil || rows == 0 {
			return err
		}
		return q.InsertTrafficLog(ctx, store.InsertTrafficLogParams{
			UserID: id32, U: u, D: d, NodeID: n.ID, Rate: n.TrafficRate,
			Traffic: util.FlowAutoShow(float64(u+d) * n.TrafficRate), LogTime: now,
		})
	})
	if err != nil {
		a.Log.Error("节点上报流量失败", "err", err)
		writePHPJSON(w, sub.NewOMap().Set("ret", 0).Set("msg", "update failed"))
		return
	}
	writePHPJSON(w, sub.NewOMap().Set("ret", 1).Set("msg", "ok"))
}

// legacyMuOnlineCount 对应旧版 Mu\NodeController::onlineUserLog。
func (a *App) legacyMuOnlineCount(w http.ResponseWriter, r *http.Request) {
	nr := parseNodeRequest(r)
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 32)
	if err == nil {
		err = a.Q.InsertNodeOnlineLog(r.Context(), store.InsertNodeOnlineLogParams{NodeID: uint32(id), OnlineUser: int32(anyInt(nr.param("count"))), LogTime: time.Now().Unix()}) //nolint:gosec // 在线人数
	}
	if err != nil {
		writePHPJSON(w, sub.NewOMap().Set("ret", 0).Set("msg", "update failed"))
		return
	}
	writePHPJSON(w, sub.NewOMap().Set("ret", 1).Set("msg", "ok"))
}

// legacyMuNodeInfo 对应旧版 Mu\NodeController::info。
func (a *App) legacyMuNodeInfo(w http.ResponseWriter, r *http.Request) {
	nr := parseNodeRequest(r)
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 32)
	if err == nil {
		err = a.Q.InsertNodeInfo(r.Context(), store.InsertNodeInfoParams{
			NodeID: uint32(id), Uptime: anyFloat(nr.param("uptime")), Load: limitLen(nr.param("load"), 32), LogTime: time.Now().Unix(),
		})
	}
	if err != nil {
		writePHPJSON(w, sub.NewOMap().Set("ret", 0).Set("msg", "update failed"))
		return
	}
	writePHPJSON(w, sub.NewOMap().Set("ret", 1).Set("msg", "ok"))
}
