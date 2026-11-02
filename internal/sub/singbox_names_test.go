package sub

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/testdb"
)

// TestSingBoxRenderNames 走真实订阅入口（ParseRequest + Service.Render）：节点重名、与分组名或 direct / tailscale 同名、
// 名称为空时由通用名称处理改名，sing-box 输出中没有重复 tag（sing-box 遇到重复 tag 拒绝整份配置）。
func TestSingBoxRenderNames(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := pool.ExecContext(ctx, `INSERT INTO user (id,user_name,email,pass,passwd,port,method,protocol,protocol_param,obfs,obfs_param,is_multi_user,is_admin,class,node_group,u,d,transfer_enable,class_expire,expire_in,reg_date,im_value,pac,remark,forbidden_ip,forbidden_port,disconnect_ip)
		VALUES (1,'alice','a@x.com','x','pa55',10001,'chacha20-ietf','origin','','plain','',0,0,1,1,0,0,10737418240,'2099-06-04 00:05:00','2099-06-04 00:05:00',UTC_TIMESTAMP(),'','','','','','')`); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"香港", "香港", "direct", "🍃 Proxy", "tailscale", " "} {
		if _, err := pool.ExecContext(ctx, `INSERT INTO ss_node (id,name,type,sort,server,method,node_class,node_group,custom_rss,mu_only,traffic_rate,node_ip) VALUES (?,?,1,11,'vm.example.com;443;0;tcp','m',0,0,0,0,1,'')`, i+1, name); err != nil {
			t.Fatal(err)
		}
	}
	q := store.New(pool)
	u, err := q.GetUser(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Q: q, AppName: "牛牛面板", SubURL: "https://p.example/link/", Loc: time.UTC,
		TokenFunc: func(context.Context, uint32, int32) (string, error) { return "TESTTOKEN", nil }}
	want := []string{"Proxy", "direct (2)", "tailscale (2)", "香港", "香港 (2)", "🍃 Proxy (2)"}
	for _, query := range []string{"sing-box=1&tailscale=1", "sing-box=1&tailscale=0", "sing-box=1&sb_version=1.15&tailscale=1", "sing-box=1&sb_version=1.15&tailscale=0"} {
		out, err := s.Render(ctx, u, requestFor(t, query), time.Now())
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		var c struct {
			Outbounds []struct {
				Tag       string   `json:"tag"`
				Outbounds []string `json:"outbounds"`
			} `json:"outbounds"`
			Endpoints []struct {
				Tag string `json:"tag"`
			} `json:"endpoints"`
		}
		if err := json.Unmarshal([]byte(out), &c); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		seen := map[string]bool{}
		for _, e := range c.Endpoints {
			seen[e.Tag] = true
		}
		for _, o := range c.Outbounds {
			if seen[o.Tag] {
				t.Fatalf("%s: duplicate tag %q", query, o.Tag)
			}
			seen[o.Tag] = true
		}
		if got := c.Outbounds[0].Outbounds; c.Outbounds[0].Tag != "🍃 Proxy" || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: proxy group = %q, want %q", query, got, want)
		}
	}
}
