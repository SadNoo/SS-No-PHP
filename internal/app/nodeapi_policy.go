package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

const nodePolicyRetention = int64(3600)
const nodePolicyRows = 50000
const nodePolicyResponseBytes = 32 << 20

type nodePolicyIP struct {
	IP        string `json:"ip"`
	BlockedAt int64  `json:"blocked_at"`
}
type nodePolicyRule struct {
	ID    int64  `json:"id"`
	Regex string `json:"regex"`
}
type nodePolicySnapshot struct {
	APIVersion     int              `json:"api_version"`
	NodeID         uint32           `json:"node_id"`
	GeneratedAt    int64            `json:"generated_at"`
	BlockRetention int64            `json:"block_retention_sec"`
	BlockedIPs     []nodePolicyIP   `json:"blocked_ips"`
	DetectRules    []nodePolicyRule `json:"detect_rules"`
}

func canonicalPolicyIP(raw string) (string, error) {
	ip, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(raw), "[]"))
	if err != nil {
		return "", errors.New("invalid source IP in policy data")
	}
	return ip.WithZone("").Unmap().String(), nil
}

// deleteBlocksForIP 删除一个 IP 在所有节点上的全部封禁记录，包括同一 IP 的其他写法（如 ::ffff:1.2.3.4、
// IPv6 的不同书写形式），供用户中心和管理后台解封共用，须在写入解封记录的同一事务中调用。
// 只按原文删除时，其他写法的封禁会留下：解封记录 5 分钟后被清理，完整策略快照（/policy，读取 1 小时内的封禁）
// 会让这个 IP 重新被封。不是有效 IP 的输入只按原文删除。
func deleteBlocksForIP(ctx context.Context, q *store.Queries, ip string) error {
	if err := q.DeleteBlockIP(ctx, ip); err != nil {
		return err
	}
	want, err := canonicalPolicyIP(ip)
	if err != nil {
		return nil
	}
	rows, err := q.ListBlockIPIDs(ctx)
	if err != nil {
		return err
	}
	for _, b := range rows {
		if got, err := canonicalPolicyIP(b.Ip); err == nil && got == want {
			if err := q.DeleteBlockIPByID(ctx, b.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Unblock only wins when strictly newer, matching v2lite 9.4 (ties stay blocked).
func effectiveNodePolicy(blocks, unblocks map[string]int64, now int64) []nodePolicyIP {
	result := make([]nodePolicyIP, 0, len(blocks))
	for ip, ts := range blocks {
		if ts < now-nodePolicyRetention || ts > now || unblocks[ip] > ts {
			continue
		}
		result = append(result, nodePolicyIP{ip, ts})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].IP < result[j].IP })
	return result
}

// readPolicyIPs 读取封禁 / 解封记录，按标准化 IP 取最新时间。不是有效 IP 的记录（旧接口与旧后端可能原样写入任意文本）
// 跳过并计入 invalid，不能让整个快照失败：快照失败时后端会保留旧状态，新的封禁、用户增删都不生效。
func readPolicyIPs(ctx context.Context, tx *sql.Tx, invalid *int, query string, args ...any) (map[string]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := map[string]int64{}
	count := 0
	for rows.Next() {
		count++
		if count > nodePolicyRows {
			return nil, errors.New("policy event limit exceeded; refusing a partial snapshot")
		}
		var raw string
		var ts int64
		if err := rows.Scan(&raw, &ts); err != nil {
			return nil, err
		}
		ip, err := canonicalPolicyIP(raw)
		if err != nil {
			*invalid++
			continue
		}
		if ts > result[ip] {
			result[ip] = ts
		}
	}
	return result, rows.Err()
}

// v2NodePolicy is a complete, node-bound snapshot, not the legacy 60s delta feed.
// A read-only repeatable-read transaction prevents mixing block/unblock revisions.
func (a *App) v2NodePolicy(w http.ResponseWriter, r *http.Request) {
	n, ok := a.v2Node(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	now := time.Now().Unix()
	tx, err := a.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		a.muError(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	invalid := 0
	blocks, err := readPolicyIPs(ctx, tx, &invalid, "SELECT ip, datetime FROM blockip WHERE nodeid = ? AND datetime >= ? AND datetime <= ? ORDER BY datetime DESC LIMIT ?", n.ID, now-nodePolicyRetention, now, nodePolicyRows+1)
	if err != nil {
		a.muError(w, r, err)
		return
	}
	unblocks, err := readPolicyIPs(ctx, tx, &invalid, "SELECT ip, datetime FROM unblockip WHERE datetime >= ? AND datetime <= ? ORDER BY datetime DESC LIMIT ?", now-nodePolicyRetention, now, nodePolicyRows+1)
	if err != nil {
		a.muError(w, r, err)
		return
	}
	if invalid > 0 {
		a.Log.Warn("封禁 / 解封记录中有无效的 IP，已跳过", "node", n.ID, "count", invalid)
	}
	snapshot := nodePolicySnapshot{2, n.ID, now, nodePolicyRetention, effectiveNodePolicy(blocks, unblocks, now), []nodePolicyRule{}}
	basePayload, err := json.Marshal(snapshot)
	if err != nil {
		a.muError(w, r, err)
		return
	}
	payloadBytes := len(basePayload) + 128 // Reserve envelope overhead; bound memory while reading rules.
	rows, err := tx.QueryContext(ctx, "SELECT id, COALESCE(regex, '') FROM detect_list ORDER BY id ASC LIMIT ?", nodePolicyRows+1)
	if err != nil {
		a.muError(w, r, err)
		return
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rule nodePolicyRule
		if err = rows.Scan(&rule.ID, &rule.Regex); err != nil {
			break
		}
		if len(snapshot.DetectRules) >= nodePolicyRows || len(rule.Regex) > 65536 {
			err = errors.New("policy rule limit exceeded; refusing a partial snapshot")
			break
		}
		encoded, marshalErr := json.Marshal(rule)
		if marshalErr != nil {
			err = marshalErr
			break
		}
		payloadBytes += len(encoded) + 1
		if payloadBytes > nodePolicyResponseBytes {
			err = errors.New("policy response limit exceeded; refusing a partial snapshot")
			break
		}
		snapshot.DetectRules = append(snapshot.DetectRules, rule)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		a.muError(w, r, err)
		return
	}
	payload, err := json.Marshal(map[string]any{"ret": 1, "data": snapshot})
	if err != nil || len(payload) > nodePolicyResponseBytes {
		http.Error(w, "策略快照过大，请检查规则数量", http.StatusServiceUnavailable)
		return
	}
	if err = tx.Commit(); err != nil {
		a.muError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(payload)
}
