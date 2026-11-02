package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/server"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/sub"
	"github.com/SadNoo/SS-No-PHP/internal/util"
	"github.com/go-chi/chi/v5"
	"github.com/go-sql-driver/mysql"
)

// v2Node 完成 WebAPI v2 的全部认证，把请求绑定到路径中的节点 ID：
// 密钥正确、节点存在，且来源 IP 属于该节点（登记 IP、当场重新解析域名或 DDNS 宽限期）。
// 节点 ID 只认路径；查询参数 node_id 与路径不一致时拒绝，不能覆盖路径 ID。
func (a *App) v2Node(w http.ResponseWriter, r *http.Request) (*store.SsNode, bool) {
	w.Header().Set("Cache-Control", "no-store")
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != idStr {
		http.Error(w, "必须在路径中指定有效的节点 ID", http.StatusBadRequest)
		return nil, false
	}
	if q, ok := r.URL.Query()["node_id"]; ok && (len(q) != 1 || q[0] != idStr) {
		http.Error(w, "查询参数 node_id 与路径中的节点 ID 不一致", http.StatusBadRequest)
		return nil, false
	}
	if keys := r.URL.Query()["key"]; len(keys) != 1 || !a.validMuKey(keys[0]) {
		http.Error(w, "节点认证失败", http.StatusForbidden)
		return nil, false
	}
	n, err := a.requestNode(r, idStr)
	if err != nil {
		a.muError(w, r, err)
		return nil, false
	}
	if n == nil { // 不存在的节点与密钥错误返回相同结果，不暴露节点是否存在
		http.Error(w, "节点认证失败", http.StatusForbidden)
		return nil, false
	}
	ip := server.ClientIP(r)
	if isLoopback(ip) {
		return n, true
	}
	// 来源 IP 属于该节点（登记 IP，或当场重新解析域名后属于该节点）：通过，并记录“从登记 IP 发来请求”的时间。
	verified := nodeIPMatches(n.NodeIp, ip)
	if !verified {
		fresh, err := a.nodeByFreshDNS(r.Context(), []store.SsNode{*n}, ip, time.Now())
		if err != nil {
			a.muError(w, r, err)
			return nil, false
		}
		verified = fresh != nil
	}
	if verified {
		a.nodeAuth.markVerified(n.ID, time.Now())
		return n, true
	}
	// DDNS 宽限期：该节点已停止从登记 IP 发来请求（nodeQuiet）且未超过宽限时间。
	last := a.nodeAuth.lastVerified(n.ID)
	age := time.Since(last)
	if !last.IsZero() && age >= nodeQuiet && age <= a.Cfg.Security.NodeIPGrace.D() {
		a.Log.Warn("节点来源 IP 与登记不符，宽限期内放行（等待 DDNS 更新）", "node", n.ID, "ip", ip)
		return n, true
	}
	http.Error(w, "节点来源不匹配", http.StatusForbidden)
	return nil, false
}

func backendProtocol(n store.SsNode) (any, error) {
	if n.Sort == sub.SortV2Ray || n.Sort == sub.SortV2RayRelay {
		v := sub.VMessConfig(sub.RealServer(n.Server)) // 节点后端使用真实节点地址，不含外部中转
		if v.Int("port") < 1 || v.Int("port") > 65535 || v.Str("add") == "" {
			return nil, errors.New("VMess 地址无效")
		}
		return map[string]any{"protocol": "vmess", "server": v.Str("add"), "server_port": v.Int("port"), "alter_id": v.Int("aid"), "transport": v.Str("net"), "tls": v.Str("tls") == "tls", "host": v.Str("host"), "path": v.Str("path"), "server_name": v.Str("sni")}, nil
	}
	return sub.ParseProtocol(n.Sort, n.Server)
}

func (a *App) v2NodeConfig(w http.ResponseWriter, r *http.Request) {
	n, ok := a.v2Node(w, r)
	if !ok {
		return
	}
	c, err := backendProtocol(*n)
	if err != nil {
		http.Error(w, "节点协议配置无效", http.StatusUnprocessableEntity)
		return
	}
	a.json(w, map[string]any{"ret": 1, "data": map[string]any{"api_version": 2, "node_id": n.ID, "sort": n.Sort, "enabled": n.Type == 1, "node_class": n.NodeClass, "node_group": n.NodeGroup, "node_speedlimit": n.NodeSpeedlimit.Float(), "traffic_rate": n.TrafficRate, "traffic_unit": "raw_bytes_delta", "protocol_config": c}})
}

func (a *App) v2NodeUsers(w http.ResponseWriter, r *http.Request) {
	n, ok := a.v2Node(w, r)
	if !ok {
		return
	}
	if _, err := backendProtocol(*n); err != nil {
		http.Error(w, "节点协议配置无效，请保留上次成功用户列表", http.StatusUnprocessableEntity)
		return
	}
	users, available, err := a.nodeUsers(r.Context(), *n, time.Now())
	if err != nil {
		a.muError(w, r, err)
		return
	}
	list := []map[string]any{}
	var skip blockedIPSkip
	if v2NodeServing(*n, available) {
		for _, u := range users {
			// ip_limit：同时在线 IP 数（0 不限），供后端参考；blocked_ips：面板判定超出限制、暂时禁止连接的 IP。
			entry := map[string]any{"id": u.ID, "node_speedlimit": u.NodeSpeedlimit.Float(), "forbidden_ip": u.ForbiddenIp, "forbidden_port": u.ForbiddenPort, "expire_at": u.ExpireIn.Unix(),
				"ip_limit": u.NodeConnector, "blocked_ips": nodeBlockedIPs(u, &skip)}
			switch n.Sort {
			case sub.SortSS2022:
				c, _ := sub.ParseProtocol(n.Sort, n.Server)
				entry["method"] = sub.SS2022Method
				entry["password"] = sub.SS2022UserKey(u, n.ID, c.ServerKey)
			case sub.SortAnyTLS:
				entry["password"] = sub.AnyTLSPassword(u)
			default:
				entry["uuid"] = model.NewUser(u, a.Loc).UUID()
				if n.Sort == sub.SortVLESS {
					entry["flow"] = "xtls-rprx-vision"
				}
			}
			list = append(list, entry)
		}
	}
	a.reportBlockedIPSkip(n.ID, "v2/users", &skip)
	a.json(w, map[string]any{"ret": 1, "data": list})
}

type trafficDelta struct {
	UserID uint32 `json:"user_id"`
	U      int64  `json:"u"`
	D      int64  `json:"d"`
}
type trafficBatch struct {
	BatchID    string         `json:"batch_id"`
	ReportedAt int64          `json:"reported_at"` // 这批流量的产生时间（Unix 秒），必填
	Data       []trafficDelta `json:"data"`
}

// 流量批次的去重约定：面板只接受 reported_at 在 v2BatchAcceptWindow 之内的批次，去重记录保留 v2BatchRetention。
// 接受窗口短于保留窗口，所以任何能被接受的批次，其去重记录一定还在，重复提交不会被重复计费；
// 超过接受窗口的批次返回 410，节点后端应丢弃并告警，不要再重试。
const (
	v2BatchAcceptWindow = 7 * 24 * time.Hour
	v2BatchFutureSkew   = 10 * time.Minute
	v2BatchRetention    = 30 * 24 * time.Hour
)

var batchIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// v2Traffic atomically deduplicates retries and adds raw byte deltas with the panel's rate once.
func (a *App) v2Traffic(w http.ResponseWriter, r *http.Request) {
	n, ok := a.v2Node(w, r)
	if !ok {
		return
	}
	if _, err := backendProtocol(*n); err != nil {
		http.Error(w, "节点协议配置无效", http.StatusUnprocessableEntity)
		return
	}
	var batch trafficBatch
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&batch); err != nil {
		http.Error(w, "流量请求无效", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "流量请求包含多余内容", http.StatusBadRequest)
		return
	}
	now := time.Now()
	if batch.ReportedAt <= 0 || time.Unix(batch.ReportedAt, 0).After(now.Add(v2BatchFutureSkew)) {
		http.Error(w, "reported_at 缺失或晚于当前时间（请检查节点时钟）", http.StatusBadRequest)
		return
	}
	if now.Sub(time.Unix(batch.ReportedAt, 0)) > v2BatchAcceptWindow {
		http.Error(w, "批次已过期：超过 7 天的流量不再计入，请丢弃该批次", http.StatusGone)
		return
	}
	if !batchIDRE.MatchString(batch.BatchID) || len(batch.Data) == 0 || len(batch.Data) > 10000 || n.TrafficRate < 0 || math.IsNaN(n.TrafficRate) || math.IsInf(n.TrafficRate, 0) {
		http.Error(w, "批次或倍率无效", http.StatusBadRequest)
		return
	}
	var rawTotal int64
	seen := map[uint32]bool{}
	for _, d := range batch.Data {
		// Bound float multiplication below the exact integer range, rejecting overflow before writes.
		if d.UserID == 0 || seen[d.UserID] || d.U < 0 || d.D < 0 || d.U > 1<<50 || d.D > 1<<50 || float64(d.U)*n.TrafficRate > 1<<52 || float64(d.D)*n.TrafficRate > 1<<52 {
			http.Error(w, "用户或流量数值无效", http.StatusBadRequest)
			return
		}
		seen[d.UserID] = true
		if d.U > math.MaxInt64-rawTotal || d.D > math.MaxInt64-rawTotal-d.U {
			http.Error(w, "流量总量溢出", http.StatusBadRequest)
			return
		}
		rawTotal += d.U + d.D
	}
	sort.Slice(batch.Data, func(i, j int) bool { return batch.Data[i].UserID < batch.Data[j].UserID })
	canonical, _ := json.Marshal(struct {
		ReportedAt int64          `json:"reported_at"`
		Data       []trafficDelta `json:"data"`
	}{batch.ReportedAt, batch.Data})
	sum := sha256.Sum256(canonical)
	digest := hex.EncodeToString(sum[:])
	ctx := r.Context()
	// 多个节点同时上报、用户重叠时，数据库仍可能因死锁或锁等待超时回滚本批（加锁顺序见 store.AddUsersTraffic）。
	// 这时整笔重做：重新开启事务，重新写去重记录、计费、流量记录和节点已用流量；只有提交成功才返回 ret=1。
	// 去重语义不变：上一次实际已提交（只是没收到结果）时，重做会命中去重记录，返回 duplicate=true，不会重复计费。
	var res trafficResult
	var err error
	for attempt := 1; ; attempt++ {
		res, err = a.applyTrafficBatch(ctx, n, batch, digest, rawTotal, now, attempt)
		if err == nil || !retryableTxError(err) || attempt >= v2TrafficAttempts {
			break
		}
		a.Log.Warn("流量批次遇到死锁或锁等待超时，整批重做", "node", n.ID, "batch", batch.BatchID, "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(time.Duration(attempt) * (50 + time.Duration(rand.IntN(50))) * time.Millisecond): //nolint:gosec // 退避抖动，与安全无关
			continue
		}
		break
	}
	switch {
	case err != nil:
		a.muError(w, r, err)
	case res.conflict:
		http.Error(w, "批次 ID 已用于不同内容", http.StatusConflict)
	case res.duplicate:
		a.json(w, map[string]any{"ret": 1, "data": "ok", "duplicate": true})
	default:
		if len(res.ignored) > 0 {
			a.Log.Warn("流量批次中有不计费的用户（已删除或已不属于本节点）", "node", n.ID, "batch", batch.BatchID, "ignored", len(res.ignored), "users", len(batch.Data))
		}
		a.json(w, map[string]any{"ret": 1, "data": "ok", "duplicate": false, "ignored": res.ignored})
	}
}

// v2TrafficAttempts 是流量批次遇到死锁或锁等待超时时最多执行的次数（含第一次）。
var v2TrafficAttempts = 3

// v2TrafficTestHook 仅供测试：在本批计费之后、提交之前调用，返回错误时按该错误处理（用于模拟死锁）。
var v2TrafficTestHook func(attempt int) error

// retryableTxError 判断错误是否为可以整笔重做的锁冲突：死锁（1213）或锁等待超时（1205）。
func retryableTxError(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && (me.Number == 1213 || me.Number == 1205)
}

type trafficResult struct {
	duplicate, conflict bool
	ignored             []map[string]any
}

// applyTrafficBatch 在一个事务中写入去重记录并为本批计费；任何错误都整笔回滚，不会部分提交。
// batch.Data 已按用户 ID 升序排列。
func (a *App) applyTrafficBatch(ctx context.Context, n *store.SsNode, batch trafficBatch, digest string, rawTotal int64, now time.Time, attempt int) (trafficResult, error) {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return trafficResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, "INSERT INTO node_traffic_batch (node_id,batch_id,payload_hash,created_at) VALUES (?,?,?,?)", n.ID, batch.BatchID, digest, now.Unix())
	if err != nil {
		var me *mysql.MySQLError
		if !errors.As(err, &me) || me.Number != 1062 {
			return trafficResult{}, err
		}
		_ = tx.Rollback()
		var old string
		if err := a.DB.QueryRowContext(ctx, "SELECT payload_hash FROM node_traffic_batch WHERE node_id=? AND batch_id=?", n.ID, batch.BatchID).Scan(&old); err != nil {
			return trafficResult{}, err
		}
		return trafficResult{duplicate: old == digest, conflict: old != digest}, nil
	}
	q := a.Q.WithTx(tx)
	logTime := now.Unix()
	// 不存在的用户（已删除）、等级 / 分组已不符合本节点的用户（如套餐到期降为 0 级）：只跳过这几条、不计费，其余照常入账。
	// 不能整批拒绝：节点后端按顺序重试，一批永久失败会挡住该节点之后所有用户的流量，7 天后被丢弃。
	// 跳过的条目也不能为其他等级 / 分组的用户计费，节点只能给自己可服务的用户记账。
	// 相关用户一次读出，计费与流量记录批量写入（每条语句最多 500 行），避免每个用户多次往返、长时间持有事务。
	ids := make([]uint32, len(batch.Data))
	for i, d := range batch.Data {
		ids[i] = d.UserID
	}
	access := make(map[uint32]store.ListUserNodeAccessRow, len(ids))
	for start := 0; start < len(ids); start += 1000 {
		rows, err := q.ListUserNodeAccess(ctx, ids[start:min(start+1000, len(ids))])
		if err != nil {
			return trafficResult{}, err
		}
		for _, u := range rows {
			access[u.ID] = u
		}
	}
	res := trafficResult{ignored: []map[string]any{}}
	deltas := make([]store.UserTrafficDelta, 0, len(batch.Data))
	logs := make([]store.InsertTrafficLogParams, 0, len(batch.Data))
	for _, d := range batch.Data {
		u, ok := access[d.UserID]
		if !ok {
			res.ignored = append(res.ignored, map[string]any{"user_id": d.UserID, "reason": "user_not_found"})
			continue
		}
		// 已禁用、帐号过期、流量用尽的用户照常计入最后的流量。
		if u.IsAdmin == 0 && (u.Class < n.NodeClass || (n.NodeGroup != 0 && u.NodeGroup != n.NodeGroup)) {
			res.ignored = append(res.ignored, map[string]any{"user_id": d.UserID, "reason": "user_not_on_node"})
			continue
		}
		deltas = append(deltas, store.UserTrafficDelta{ID: u.ID, U: int64(math.Round(float64(d.U) * n.TrafficRate)), D: int64(math.Round(float64(d.D) * n.TrafficRate))})
		logs = append(logs, store.InsertTrafficLogParams{UserID: u.ID, NodeID: n.ID, U: d.U, D: d.D, Rate: n.TrafficRate, Traffic: util.FlowAutoShow(float64(d.U+d.D) * n.TrafficRate), LogTime: logTime})
	}
	if err := q.AddUsersTraffic(ctx, logTime, deltas); err != nil {
		return trafficResult{}, err
	}
	if err := q.InsertTrafficLogs(ctx, logs); err != nil {
		return trafficResult{}, err
	}
	// 节点已用流量按实际转发计算，包括跳过的条目。
	if err := q.AddNodeBandwidth(ctx, store.AddNodeBandwidthParams{ID: n.ID, NodeBandwidth: rawTotal}); err != nil {
		return trafficResult{}, err
	}
	if v2TrafficTestHook != nil {
		if err := v2TrafficTestHook(attempt); err != nil {
			return trafficResult{}, err
		}
	}
	return res, tx.Commit()
}

// trafficBatchCleanPause 是分批清理去重记录时每批之间的间隔，给数据库留出处理其他请求的时间。
var trafficBatchCleanPause = 200 * time.Millisecond

// cleanTrafficBatches 分批删除超过保留期（30 天）的流量批次去重记录，每批最多 5000 条。
func (a *App) cleanTrafficBatches(ctx context.Context, now time.Time) error {
	cut := now.Add(-v2BatchRetention).Unix()
	var total int64
	for {
		n, err := a.Q.DeleteOldTrafficBatches(ctx, cut)
		if err != nil {
			return err
		}
		total += n
		if n < 5000 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(trafficBatchCleanPause):
		}
	}
	if total > 0 {
		a.Log.Info("已清理过期的流量批次去重记录", "rows", total)
	}
	return nil
}

// v2NodeServing 判断节点当前是否为用户提供服务（users 与 alive_ip 接口共用）：节点启用且未超过流量上限。
func v2NodeServing(n store.SsNode, available bool) bool {
	return available && n.Type == 1 && (n.NodeBandwidthLimit == 0 || n.NodeBandwidth < n.NodeBandwidthLimit)
}

type aliveIPEntry struct {
	UserID uint32 `json:"user_id"`
	IP     string `json:"ip"`
}

// v2AliveIP 接收节点上报的用户在线 IP（最近一分钟内有连接的 IP），面板据此执行同时在线 IP 数限制（checkConnector）。
// 只记录可以使用本节点的用户；其他用户的条目忽略并计入 ignored。空数组表示当前没有在线用户。
func (a *App) v2AliveIP(w http.ResponseWriter, r *http.Request) {
	n, ok := a.v2Node(w, r)
	if !ok {
		return
	}
	var body struct {
		Data []aliveIPEntry `json:"data"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Data == nil {
		http.Error(w, "在线 IP 请求无效", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "在线 IP 请求包含多余内容", http.StatusBadRequest)
		return
	}
	if len(body.Data) > 10000 {
		http.Error(w, "单次最多 10000 条", http.StatusBadRequest)
		return
	}
	type key struct {
		user uint32
		ip   string
	}
	seen := map[key]bool{}
	var entries []key
	for _, e := range body.Data {
		ip, ok := normalizeIP(e.IP) // 统一写法：IPv6 压缩形式，IPv4 映射地址转为 IPv4
		if e.UserID == 0 || !ok {
			http.Error(w, "用户 ID 或 IP 无效", http.StatusBadRequest)
			return
		}
		k := key{e.UserID, ip}
		if !seen[k] {
			seen[k] = true
			entries = append(entries, k)
		}
	}
	ctx := r.Context()
	now := time.Now()
	users, available, err := a.nodeUsers(ctx, *n, now)
	if err != nil {
		a.muError(w, r, err)
		return
	}
	// 与 users 接口相同：节点停用或流量超限时不服务任何用户，上报全部忽略。
	allowed := make(map[uint32]bool, len(users))
	if v2NodeServing(*n, available) {
		for _, u := range users {
			allowed[u.ID] = true
		}
	}
	ignored := 0
	rows := make([]store.InsertAliveIPParams, 0, len(entries))
	for _, e := range entries {
		if !allowed[e.user] {
			ignored++
			continue
		}
		rows = append(rows, store.InsertAliveIPParams{Nodeid: n.ID, Userid: e.user, Ip: e.ip, Datetime: now.Unix()})
	}
	// 批量写入（每条语句最多 500 行），仍在一个事务内：要么全部记录，要么都不记录。
	if err := a.tx(ctx, func(q *store.Queries) error { return q.InsertAliveIPs(ctx, rows) }); err != nil {
		a.muError(w, r, err)
		return
	}
	a.json(w, map[string]any{"ret": 1, "data": "ok", "accepted": len(entries) - ignored, "ignored": ignored})
}
