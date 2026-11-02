package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

// TestV2TrafficConcurrentNodes 多个节点同时上报、用户互相重叠、跨越批量更新的 500 行分块：
// 不能出现死锁（关闭整笔重试，证明加锁顺序本身正确），并逐用户核对倍率取整后的累计流量、流量记录与节点已用流量。
// 场景来自第三方验收：1505 个用户，16 个节点，每批 1105 人、起点错开，逆序提交，同时放行。
func TestV2TrafficConcurrentNodes(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "conc-key" })
	ctx := context.Background()
	users := insertBulkUsers(t, e, "conc", 1505, "1")
	e.exec("ANALYZE TABLE user") // 让优化器按真实行数选择执行计划（与长期运行的库一致）
	attempts := v2TrafficAttempts
	v2TrafficAttempts = 1 // 不重做：任何死锁都会直接失败
	defer func() { v2TrafficAttempts = attempts }()
	const nodes, per, rounds = 16, 1105, 10
	rates := make([]float64, nodes)
	for i := range nodes {
		rates[i] = 0.5 + float64(i%2) // 0.5、1.5 交替
		e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip) VALUES
			(?, 'n', 1, ?, '2022-blake3-aes-256-gcm', '', '', 14, ?, 0, '127.0.0.1')`, 71+i, "n.example.com;8443;"+fixtureKey, rates[i])
	}
	type delta struct {
		UserID uint32 `json:"user_id"`
		U      int64  `json:"u"`
		D      int64  `json:"d"`
	}
	wantU, wantD := map[uint32]int64{}, map[uint32]int64{}
	logs := 0
	var last string
	for round := range rounds {
		bodies := make([]string, nodes)
		for i := range nodes {
			off := (i * 31) % 400
			var data []delta
			for k := off + per - 1; k >= off; k-- { // 逆序提交，面板按用户 ID 排序
				u := users[k].id
				data = append(data, delta{u, 1, 3})
				wantU[u] += int64(math.Round(1 * rates[i]))
				wantD[u] += int64(math.Round(3 * rates[i]))
			}
			logs += per
			b, _ := json.Marshal(map[string]any{"batch_id": fmt.Sprintf("conc-%d-%d", round, i), "reported_at": time.Now().Unix(), "data": data})
			bodies[i] = string(b)
		}
		last = bodies[nodes-1]
		start := make(chan struct{})
		results := make([]string, nodes)
		var wg sync.WaitGroup
		for i := range nodes {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				resp, err := http.Post(fmt.Sprintf("%s/mod_mu/v2/nodes/%d/traffic?key=conc-key", e.ts.URL, 71+i), "application/json", strings.NewReader(bodies[i]))
				if err != nil {
					results[i] = err.Error()
					return
				}
				b, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				results[i] = fmt.Sprintf("%d %s", resp.StatusCode, b)
			}(i)
		}
		close(start)
		wg.Wait()
		for i, r := range results {
			if !strings.HasPrefix(r, `200 {"data":"ok","duplicate":false`) {
				t.Fatalf("第 %d 轮节点 %d 上报失败: %s", round+1, 71+i, r)
			}
		}
	}
	// 重发最后一批：去重，不重复计费（下面的逐用户核对会发现任何重复）。
	if status, _, resp := e.response("POST", fmt.Sprintf("/mod_mu/v2/nodes/%d/traffic?key=conc-key", 71+nodes-1), last); status != 200 || !strings.Contains(resp, `"duplicate":true`) {
		t.Fatalf("重发应返回 duplicate: %d %s", status, resp)
	}
	rows, err := e.app.DB.QueryContext(ctx, "SELECT id, u, d FROM user WHERE email LIKE 'conc%@example.com'")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	checked := 0
	for rows.Next() {
		var id uint32
		var u, d int64
		if err := rows.Scan(&id, &u, &d); err != nil {
			t.Fatal(err)
		}
		if u != wantU[id] || d != wantD[id] {
			t.Fatalf("用户 %d 累计流量不正确: u=%d d=%d，应为 u=%d d=%d", id, u, d, wantU[id], wantD[id])
		}
		checked++
	}
	if err := rows.Err(); err != nil || checked != len(users) {
		t.Fatalf("核对用户数 %d: %v", checked, err)
	}
	var gotLogs int
	_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_traffic_log WHERE node_id BETWEEN 71 AND 86").Scan(&gotLogs)
	if gotLogs != logs {
		t.Fatalf("流量记录应为 %d 条: %d", logs, gotLogs)
	}
	for i := range nodes {
		if n, _ := e.app.Q.GetNode(ctx, uint32(71+i)); n.NodeBandwidth != int64(rounds*per*4) {
			t.Fatalf("节点 %d 已用流量应为 %d: %d", 71+i, rounds*per*4, n.NodeBandwidth)
		}
	}
}

// TestV2TrafficRetry 验证遇到死锁时整笔重做：第一次在计费之后、提交之前失败（整笔回滚），重做后只计费一次；
// 次数用尽时返回失败、什么都不写入，节点之后用同一批次重试会正常入账。
func TestV2TrafficRetry(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "retry-key" })
	ctx := context.Background()
	uid := e.login("retry@example.com")
	e.exec("UPDATE user SET class = 1 WHERE id = ?", uid)
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip) VALUES
		(40,'n',1,?,'2022-blake3-aes-256-gcm','','',14,1.5,1,'127.0.0.1')`, "n.example.com;8443;"+fixtureKey)
	deadlock := &mysql.MySQLError{Number: 1213, Message: "Deadlock found when trying to get lock; try restarting transaction"}
	var calls []int
	defer func() { v2TrafficTestHook = nil }()
	count := func(q string) (n int) { _ = e.app.DB.QueryRowContext(ctx, q).Scan(&n); return }
	used := func() (u, d int64) { x, _ := e.app.Q.GetUser(ctx, uid); return x.U, x.D }

	// 第一次失败，第二次成功。
	v2TrafficTestHook = func(attempt int) error {
		calls = append(calls, attempt)
		if attempt == 1 {
			return deadlock
		}
		return nil
	}
	body := fmt.Sprintf(`{"batch_id":"retry-1","reported_at":%d,"data":[{"user_id":%d,"u":10,"d":20}]}`, time.Now().Unix(), uid)
	if status, _, resp := e.response("POST", "/mod_mu/v2/nodes/40/traffic?key=retry-key", body); status != 200 || !strings.Contains(resp, `"duplicate":false`) {
		t.Fatalf("重做后应成功: %d %s", status, resp)
	}
	if u, d := used(); u != 15 || d != 30 || !slices.Equal(calls, []int{1, 2}) {
		t.Fatalf("应只计费一次: u=%d d=%d 调用 %v", u, d, calls)
	}
	if count("SELECT COUNT(*) FROM user_traffic_log WHERE node_id = 40") != 1 || count("SELECT COUNT(*) FROM node_traffic_batch WHERE node_id = 40") != 1 {
		t.Fatal("流量记录与去重记录应各一条")
	}
	if n, _ := e.app.Q.GetNode(ctx, 40); n.NodeBandwidth != 30 {
		t.Fatalf("节点已用流量应为 30: %d", n.NodeBandwidth)
	}

	// 次数用尽：返回失败，整笔回滚。
	calls = nil
	v2TrafficTestHook = func(attempt int) error { calls = append(calls, attempt); return deadlock }
	body2 := fmt.Sprintf(`{"batch_id":"retry-2","reported_at":%d,"data":[{"user_id":%d,"u":1,"d":1}]}`, time.Now().Unix(), uid)
	if _, _, resp := e.response("POST", "/mod_mu/v2/nodes/40/traffic?key=retry-key", body2); !strings.Contains(resp, `"ret":0`) {
		t.Fatalf("次数用尽应返回失败: %s", resp)
	}
	if u, d := used(); u != 15 || d != 30 || len(calls) != v2TrafficAttempts {
		t.Fatalf("失败的批次不应计费: u=%d d=%d 调用 %v", u, d, calls)
	}
	if count("SELECT COUNT(*) FROM node_traffic_batch WHERE node_id = 40") != 1 || count("SELECT COUNT(*) FROM user_traffic_log WHERE node_id = 40") != 1 {
		t.Fatal("失败的批次不应留下去重记录或流量记录")
	}
	// 节点用同一批次重试：正常入账。
	v2TrafficTestHook = nil
	if status, _, resp := e.response("POST", "/mod_mu/v2/nodes/40/traffic?key=retry-key", body2); status != 200 || !strings.Contains(resp, `"duplicate":false`) {
		t.Fatalf("之后重试应正常入账: %d %s", status, resp)
	}
	if u, d := used(); u != 17 || d != 32 {
		t.Fatalf("重试后计费: u=%d d=%d", u, d)
	}

	// 不是锁冲突的错误不重做。
	calls = nil
	v2TrafficTestHook = func(attempt int) error { calls = append(calls, attempt); return &mysql.MySQLError{Number: 1146} }
	body3 := fmt.Sprintf(`{"batch_id":"retry-3","reported_at":%d,"data":[{"user_id":%d,"u":1,"d":1}]}`, time.Now().Unix(), uid)
	if _, _, resp := e.response("POST", "/mod_mu/v2/nodes/40/traffic?key=retry-key", body3); !strings.Contains(resp, `"ret":0`) || len(calls) != 1 {
		t.Fatalf("其他错误不应重做: %s 调用 %v", resp, calls)
	}
}
