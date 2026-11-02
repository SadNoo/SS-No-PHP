package sub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/testdb"
)

type fixture struct {
	Users  []map[string]any `json:"users"`
	Nodes  []map[string]any `json:"nodes"`
	Relays []map[string]any `json:"relays"`
}

// TestGoldenFullSubscription 用与原版相同的数据生成完整订阅内容，与 testdata/golden_full.json
// （原版 URL.php 在 PHP 8.4 下的实际输出）逐字节比较。
func TestGoldenFullSubscription(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	var f fixture
	b, _ := os.ReadFile("testdata/fixture.json")
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	toUTC := func(v any) string {
		s := v.(string)
		if s == "1989-06-04 00:05:00" || s == "2099-06-04 00:05:00" {
			return s
		}
		tm, err := time.ParseInLocation("2006-01-02 15:04:05", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return tm.UTC().Format("2006-01-02 15:04:05")
	}
	for _, u := range f.Users {
		_, err := pool.ExecContext(ctx, `INSERT INTO user (id,user_name,email,pass,passwd,port,method,protocol,protocol_param,obfs,obfs_param,is_multi_user,is_admin,class,node_group,u,d,transfer_enable,class_expire,expire_in,reg_date,im_value,pac,remark,forbidden_ip,forbidden_port,disconnect_ip)
			VALUES (?,?,?,'x',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,UTC_TIMESTAMP(),'','','','','','')`,
			u["id"], u["user_name"], u["email"], u["passwd"], u["port"], u["method"], u["protocol"], u["protocol_param"], u["obfs"], u["obfs_param"],
			u["is_multi_user"], u["is_admin"], u["class"], u["node_group"], u["u"], u["d"], u["transfer_enable"], toUTC(u["class_expire"]), toUTC(u["expire_in"]))
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range f.Nodes {
		_, err := pool.ExecContext(ctx, `INSERT INTO ss_node (id,name,type,sort,server,method,node_class,node_group,custom_rss,mu_only,traffic_rate,node_ip) VALUES (?,?,?,?,?,'m',?,?,?,?,?,'')`,
			n["id"], n["name"], n["type"], n["sort"], n["server"], n["node_class"], n["node_group"], n["custom_rss"], n["mu_only"], n["traffic_rate"])
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range f.Relays {
		_, err := pool.ExecContext(ctx, `INSERT INTO relay (id,user_id,source_node_id,dist_node_id,dist_ip,port,priority) VALUES (?,?,?,?,?,?,?)`,
			r["id"], r["user_id"], r["source_node_id"], r["dist_node_id"], r["dist_ip"], r["port"], r["priority"])
		if err != nil {
			t.Fatal(err)
		}
	}

	var want map[string]string
	gb, _ := os.ReadFile("testdata/golden_full.json")
	if err := json.Unmarshal(gb, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != 48 {
		t.Fatalf("对拍数据应有 48 条，实际 %d 条", len(want))
	}
	compared := 0
	q := store.New(pool)
	now := time.Now()
	for _, merge := range []bool{false, true} {
		s := &Service{Q: q, AppName: "牛牛面板", SubURL: "https://p.example/link/", MergeSub: merge, MuRegex: "%5m%id.%suffix", MuSuffix: "microsoft.com", Loc: loc,
			TokenFunc: func(context.Context, uint32, int32) (string, error) { return "TESTTOKEN", nil }}
		for _, uid := range []uint32{1, 2, 5} {
			u, err := q.GetUser(ctx, uid)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}, {0, 2}, {10010, 0}, {10010, 1}} {
				key := fmt.Sprintf("merge=%v/user=%d/mu=%d/ss=%d", merge, uid, c[0], c[1])
				got, err := s.AllURL(ctx, u, int32(c[0]), c[1], now)
				if err != nil {
					t.Fatal(err)
				}
				compare(t, key, got, want[key])
				compared++
			}
			if !merge {
				vm, err := s.AllVMessURL(ctx, u)
				if err != nil {
					t.Fatal(err)
				}
				compare(t, fmt.Sprintf("user=%d/vmess", uid), vm, want[fmt.Sprintf("user=%d/vmess", uid)])
				ssd, err := s.AllSSDURL(ctx, u)
				if err != nil {
					t.Fatal(err)
				}
				compare(t, fmt.Sprintf("user=%d/ssd", uid), ssd, want[fmt.Sprintf("user=%d/ssd", uid)])
				compared += 2
			}
		}
	}
	if compared != len(want) {
		t.Fatalf("只比较了 %d 条", compared)
	}
}

func compare(t *testing.T, key, got, want string) {
	t.Helper()
	if got == want {
		return
	}
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gl) || i < len(wl); i++ {
		var g, w string
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if g != w {
			t.Errorf("%s 第 %d 行不同\n  得到 %s\n  期望 %s", key, i+1, decodeForDiff(g), decodeForDiff(w))
			return
		}
	}
}

func decodeForDiff(line string) string {
	for _, p := range []string{"ssr://", "ssd://"} {
		if strings.HasPrefix(line, p) {
			if b, err := base64Decode(strings.TrimPrefix(line, p)); err == nil {
				return p + string(b)
			}
		}
	}
	return line
}

// TestClashRender 检查 Clash 配置的结构：节点、代理组与规则段都存在，动态部分已替换。
func TestClashRender(t *testing.T) {
	c := NewOMap().Set("name", "香港 01").Set("type", "ss").Set("server", "a.com").Set("port", 443)
	out := renderClash([]*OMap{c}, []string{"香港 01"}, time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC))
	for _, want := range []string{
		"## 最后更新时间：2026-01-02 03:04:05",
		`    - {"name":"香港 01","type":"ss","server":"a.com","port":443}`,
		`- { name: "Auto", type: fallback, proxies: ["香港 01"], url:`,
		`- { name: "Proxy", type: select, proxies: ["Auto","香港 01"] }`,
		"Rule:\n", "- MATCH,Others",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Clash 配置缺少：%s", want)
		}
	}
	if strings.Contains(out, "{PROXIES") || strings.Contains(out, "{UPDATED}") {
		t.Error("仍有未替换的占位符")
	}
}
