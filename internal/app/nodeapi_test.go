package app

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

func (e *testEnv) raw(method, path, contentType, body string) string {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestNodeAPI(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "nodekey" })
	ctx := context.Background()
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_speedlimit,node_ip,node_group,custom_rss,mu_only,custom_method)
		VALUES (1,'香港',1,'hk.example.com','aes-256-cfb','','可用',0,1.5,0,0,'127.0.0.1',0,1,0,0)`)
	e.exec(`INSERT INTO user (id,user_name,email,pass,passwd,port,reg_date,im_value,pac,remark,forbidden_ip,forbidden_port,disconnect_ip,transfer_enable,u,d,method,node_speedlimit)
		VALUES (5,'a','a@example.com','x','pw',10005,UTC_TIMESTAMP(),'','','','127.0.0.0/8','','',1073741824,100,200,'chacha20',12.5),
		       (6,'b','b@example.com','x','pw2',10006,UTC_TIMESTAMP(),'','','','','','',100,100,0,'rc4-md5',0)`)

	if got := e.raw("GET", "/mod_mu/users?node_id=1", "", ""); got != `{"ret":0,"data":"null"}` {
		t.Fatalf("缺少密钥: %s", got)
	}
	if got := e.raw("GET", "/mod_mu/users?node_id=1&key=", "", ""); got != `{"ret":0,"data":"token or source is invalid"}` {
		t.Fatalf("空密钥不应通过: %s", got)
	}
	got := e.raw("GET", "/mod_mu/users?node_id=1&key=nodekey", "", "")
	want := `{"ret":1,"data":[{"id":5,"email":"a@example.com","passwd":"pw","u":100,"d":200,"port":10005,"method":"chacha20","node_speedlimit":12.5,"protocol":"origin","protocol_param":"","obfs":"plain","obfs_param":"","forbidden_ip":"127.0.0.0\/8","forbidden_port":"","disconnect_ip":"","is_multi_user":0,"uuid":"`
	if !strings.HasPrefix(got, want) || strings.Contains(got, `"id":6`) {
		t.Fatalf("用户列表不正确:\n%s", got)
	}
	if got := e.raw("GET", "/mod_mu/nodes/1/info?key=nodekey", "", ""); got != `{"ret":1,"data":{"node_group":0,"node_class":0,"node_speedlimit":0,"traffic_rate":1.5,"mu_only":0,"sort":0,"server":"hk.example.com","type":"ss-panel-v3-mod_Uim"}}` {
		t.Fatalf("节点信息不正确: %s", got)
	}

	// 流量上报：JSON 与表单两种格式，按倍率计费。
	if got := e.raw("POST", "/mod_mu/users/traffic?node_id=1&key=nodekey", "application/json", `{"data":[{"u":10,"d":20,"user_id":5},{"u":1,"d":1,"user_id":999}]}`); got != `{"ret":1,"data":"ok"}` {
		t.Fatalf("上报流量失败: %s", got)
	}
	if got := e.raw("POST", "/mod_mu/users/traffic?node_id=0&key=nodekey", "application/x-www-form-urlencoded", "data[0][u]=2&data[0][d]=2&data[0][user_id]=5"); got != `{"ret":1,"data":"ok"}` {
		t.Fatalf("表单上报流量失败: %s", got)
	}
	u, _ := e.app.Q.GetUser(ctx, 5)
	if u.U != 100+15+3 || u.D != 200+30+3 {
		t.Fatalf("流量计算不正确: u=%d d=%d", u.U, u.D)
	}
	n, _ := e.app.Q.GetNode(ctx, 1)
	if n.NodeBandwidth != 34 || n.NodeHeartbeat == 0 {
		t.Fatalf("节点流量或心跳不正确: %d %d", n.NodeBandwidth, n.NodeHeartbeat)
	}

	for path, body := range map[string]string{
		"/mod_mu/users/aliveip?node_id=1&key=nodekey":   `{"data":[{"ip":"1.2.3.4","user_id":5}]}`,
		"/mod_mu/users/detectlog?node_id=1&key=nodekey": `{"data":[{"list_id":1,"user_id":5}]}`,
		"/mod_mu/nodes/1/info?key=nodekey":              `{"load":"0.1 0.2 0.3","uptime":"123.4"}`,
		"/mod_mu/func/block_ip?node_id=1&key=nodekey":   `{"data":[{"ip":"9.9.9.9"}]}`,
		"/mod_mu/func/speedtest?node_id=1&key=nodekey":  `{"data":[{"telecomping":"10"}]}`,
		"/mod_mu/func/autoexec?node_id=1&key=nodekey":   `{"data":[{"type":1,"value":"rm -rf /","sign":"x"},{"type":2,"value":"ok","sign":"1-1"}]}`,
	} {
		if got := e.raw("POST", path, "application/json", body); got != `{"ret":1,"data":"ok"}` {
			t.Fatalf("%s: %s", path, got)
		}
	}
	var cmds int
	_ = e.app.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM auto WHERE type = 1").Scan(&cmds)
	if cmds != 0 {
		t.Fatal("节点不应能下发命令（K37）")
	}
	if got := e.raw("GET", "/mod_mu/func/block_ip?key=nodekey", "", ""); !strings.Contains(got, `"ip":"9.9.9.9"`) {
		t.Fatalf("封禁 IP 列表不正确: %s", got)
	}
	if got := e.raw("POST", "/mod_mu/nodes/config?key=nodekey", "application/x-www-form-urlencoded", url.Values{"type": {"database"}}.Encode()); strings.Contains(got, "password") || !strings.HasPrefix(got, `{"ret":0`) {
		t.Fatalf("数据库配置接口应停用（K36）: %s", got)
	}
	if got := e.raw("GET", "/mod_mu/func/ping?key=nodekey", "", ""); got != `{"ret":1,"data":"pong"}` {
		t.Fatalf("ping: %s", got)
	}
	if got := e.raw("GET", "/mod_mu/nodes?key=nodekey", "", ""); !strings.Contains(got, `"traffic_rate":1.5`) || !strings.Contains(got, `"node_ip":"127.0.0.1"`) {
		t.Fatalf("节点列表不正确: %s", got)
	}
	if got := e.raw("GET", "/mu/users?key=nodekey", "", ""); !strings.HasPrefix(got, `{"ret":1,"msg":"ok","data":[{"id":5,`) {
		t.Fatalf("旧版用户列表不正确: %s", got)
	}
}

func TestNodeIPMatches(t *testing.T) {
	for _, c := range []struct {
		nodeIP, ip string
		want       bool
	}{
		{"1.2.3.4", "1.2.3.4", true},
		{"1.2.3.45", "1.2.3.4", false},
		{"5.5.5.5,1.2.3.4#3|4", "1.2.3.4", true},
		{"", "", false},
	} {
		if got := nodeIPMatches(c.nodeIP, c.ip); got != c.want {
			t.Errorf("nodeIPMatches(%q, %q) = %v", c.nodeIP, c.ip, got)
		}
	}
}
