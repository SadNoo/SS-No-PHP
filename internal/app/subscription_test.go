package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/config"
	"github.com/SadNoo/SS-No-PHP/internal/sub"
)

const fixtureKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
const fixturePublic = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

func seedCustom(e *testEnv) {
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_ip,node_group) VALUES
 (14,'SS2022',1,?,'2022-blake3-aes-256-gcm','','',14,1.5,0,'127.0.0.1',0),
 (15,'VLESS',1,?,'','','',15,1,0,'127.0.0.1',0),
 (16,'AnyTLS',1,?,'','','',16,1,0,'127.0.0.1',0),
 (17,'VMess',1,'v.example;443;0;ws;tls;path=/test|host=s.example','','','',11,1,0,'127.0.0.1',0),
 (18,'Locked',1,?,'','','',16,1,99,'127.0.0.1',0),
 (19,'Hidden',0,?,'','','',16,1,0,'127.0.0.1',0)`, "e.example;443;"+fixtureKey, "e.co;443;0;tcp;reality;sni=s.co|pbk="+fixturePublic+"|sid=ab", "a.example;443;0;tcp;anytls;sni=s.example", "a.example;443;0;tcp;anytls;sni=s.example", "a.example;443;0;tcp;anytls;sni=s.example")
}

func (e *testEnv) response(method, path, body string) (int, http.Header, string) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func TestMigratedSubscriptions(t *testing.T) {
	e := newEnv(t)
	uid := e.login("subscriber@example.com")
	seedCustom(e)
	e.exec("UPDATE user SET enable=1,expire_in=?,class_expire=?,transfer_enable=1073741824,method='aes-128-gcm',protocol='origin',obfs='plain' WHERE id=?", time.Now().Add(time.Hour), time.Now().Add(time.Hour), uid)
	token, err := e.app.Sub.SubToken(context.Background(), uid, 0)
	if err != nil {
		t.Fatal(err)
	}
	base := "/link/" + token
	for _, q := range []string{"surge=4", "surge=5", "list=surge", "clash=1", "loon=1", "quantumultx=1", "v2ray=1", "ss2022=1", "vless=1", "anytls=1", "shadowrocket=1"} {
		status, h, body := e.response("GET", base+"?"+q, "")
		if status != 200 || body == "" {
			t.Fatalf("%s: %d %s", q, status, body)
		}
		if h.Get("Subscription-Userinfo") == "" || !strings.Contains(h.Get("Cache-Control"), "no-store") {
			t.Fatalf("missing subscription headers %s", q)
		}
		if strings.Contains(body, "Locked") || strings.Contains(body, "Hidden") {
			t.Fatalf("unauthorized node %s", q)
		}

		if q == "surge=4" || q == "surge=5" || q == "loon=1" {
			for _, part := range []string{"[General]", "[Host]", "dns-server = 223.5.5.5", "127.0.0.0/8", "::1/128", "ChinaMax", "/Apple/", "/Netflix/", "/Global/"} {
				if !strings.Contains(body, part) {
					t.Fatalf("incomplete %s profile: %s", q, part)
				}
			}
			if q == "loon=1" {
				if !strings.Contains(body, "[Remote Rule]") || !strings.Contains(body, "Apple_Domain.list") || !strings.Contains(body, "Global_Domain.list") || !strings.Contains(body, "ChinaMax_Domain.list") {
					t.Fatal("missing Loon domain sets")
				}
			}
		}

		if q == "anytls=1" {
			b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
			if err != nil || !strings.Contains(string(b), "anytls://") {
				t.Fatalf("AnyTLS %v", err)
			}
		}
		if q == "surge=5" && (!strings.Contains(body, "AnyTLS = anytls") || strings.Contains(body, "VLESS =")) {
			t.Fatal("Surge protocol filtering")
		}
	}
	_, _, surge4 := e.response("GET", base+"?surge=4", "")
	if !strings.Contains(surge4, "VMess = vmess") || !strings.Contains(surge4, "SS2022 = ss") || strings.Contains(surge4, "AnyTLS =") || strings.Contains(surge4, "VLESS =") {
		t.Fatal("Surge 4 must contain VMess and SS2022 only")
	}
	// Eight variants exercise the actual route, not just the pure configuration builder.
	for _, version := range []string{"1.14", "1.15"} {
		for _, mode := range []string{"", "&quic=1", "&tailscale=1", "&quic=1&tailscale=1"} {
			status, _, body := e.response("GET", base+"?sing-box=1&sb_version="+version+mode, "")
			if status != 200 || !json.Valid([]byte(body)) {
				t.Fatalf("singbox %s%s: %d %s", version, mode, status, body)
			}
		}
	}
	for _, q := range []string{"sing-box=1&sb_version[]=1.15", "sing-box=1&sb_version=1.15&sb_version=1.14"} {
		status, _, _ := e.response("GET", base+"?"+q, "")
		if status != 400 {
			t.Fatalf("invalid query status %d", status)
		}
	}
	page := e.page("/user")
	for _, s := range []string{"subscription-center", "Surge", "AnyTLS", "sb-version", "/client-subscription-guide.html"} {
		if !strings.Contains(page, s) {
			t.Errorf("UI missing %s", s)
		}
	}
	guide := e.page("/client-subscription-guide.html")
	if !strings.Contains(guide, "遇到问题请在看公告联系") {
		t.Fatal("guide footer")
	}
	for _, id := range []int{14, 15, 16} {
		page := e.page(fmt.Sprintf("/user/node/%d", id))
		if !strings.Contains(page, "节点链接") {
			t.Fatalf("node detail %d", id)
		}
	}
	e.exec("UPDATE user SET enable=0 WHERE id=?", uid)
	_, _, body := e.response("GET", base+"?anytls=1", "")
	if body != "" {
		t.Fatal("disabled user must not receive credentials")
	}
}

func TestProtocolWebAPI(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Security.MuKey = "test-node-key" })
	uid := e.login("backend@example.com")
	seedCustom(e)
	e.exec("UPDATE user SET enable=1,expire_in=?,transfer_enable=1073741824,u=0,d=0 WHERE id=?", time.Now().Add(time.Hour), uid)
	key := "?key=test-node-key"
	for _, id := range []int{14, 15, 16, 17} {
		for _, endpoint := range []string{"config", "users"} {
			status, _, body := e.response("GET", fmt.Sprintf("/mod_mu/v2/nodes/%d/%s%s", id, endpoint, key), "")
			if status != 200 || !strings.Contains(body, `"ret":1`) {
				t.Fatalf("v2 %d %s: %d %s", id, endpoint, status, body)
			}
		}
	}
	_, _, vmessConfig := e.response("GET", "/mod_mu/v2/nodes/17/config"+key, "")
	if !strings.Contains(vmessConfig, `"tls":true`) {
		t.Fatal("VMess ws;tls must preserve TLS")
	}
	u, _ := e.app.Q.GetUser(context.Background(), uid)
	_, _, body := e.response("GET", "/mod_mu/v2/nodes/14/users"+key, "")
	if !strings.Contains(body, sub.SS2022UserKey(u, 14, fixtureKey)) || strings.Contains(body, u.Passwd) {
		t.Fatal("SS2022 credential must be derived")
	}
	_, _, body = e.response("GET", "/mod_mu/v2/nodes/16/users"+key, "")
	if !strings.Contains(body, sub.AnyTLSPassword(u)) {
		t.Fatal("AnyTLS credential mismatch")
	}
	for _, path := range []string{"/mod_mu/v2/nodes/14/config", "/mod_mu/v2/nodes/14/config?key=bad"} {
		status, _, body := e.response("GET", path, "")
		if status == 200 && strings.Contains(body, fixtureKey) {
			t.Fatal("credential disclosure")
		}
	}
	path := "/mod_mu/v2/nodes/14/traffic" + key
	payload := fmt.Sprintf(`{"batch_id":"batch-1","reported_at":%d,"data":[{"user_id":%d,"u":10,"d":20}]}`, time.Now().Unix(), uid)
	for range 2 {
		status, _, body := e.response("POST", path, payload)
		if status != 200 || !strings.Contains(body, `"ret":1`) {
			t.Fatalf("traffic %d %s", status, body)
		}
	}
	u, _ = e.app.Q.GetUser(context.Background(), uid)
	n, _ := e.app.Q.GetNode(context.Background(), 14)
	if u.U != 15 || u.D != 30 || n.NodeBandwidth != 30 {
		t.Fatalf("idempotence/rate: %d %d %d", u.U, u.D, n.NodeBandwidth)
	}
	status, _, _ := e.response("POST", path, strings.Replace(payload, `"u":10`, `"u":11`, 1))
	if status != 409 {
		t.Fatal("batch content conflict not rejected")
	}
	for _, p := range []string{fmt.Sprintf(`{"batch_id":"negative","reported_at":%d,"data":[{"user_id":1,"u":-1,"d":0}]}`, time.Now().Unix()), fmt.Sprintf(`{"batch_id":"float","reported_at":%d,"data":[{"user_id":1,"u":1.5,"d":0}]}`, time.Now().Unix()), `{"batch_id":"x","data":[]} {}`, `{broken`} {
		status, _, _ := e.response("POST", path, p)
		if status != 400 {
			t.Fatalf("invalid traffic accepted %d", status)
		}
	}
	// Multiple nodes concurrently reporting one user must add, rather than overwrite, totals.
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for i := range 8 {
		wg.Go(func() {
			url := e.ts.URL + fmt.Sprintf("/mod_mu/v2/nodes/16/traffic%s", key)
			data := fmt.Sprintf(`{"batch_id":"parallel-%d","reported_at":%d,"data":[{"user_id":%d,"u":1,"d":1}]}`, i, time.Now().Unix(), uid)
			resp, err := e.c.Post(url, "application/json", strings.NewReader(data))
			if err != nil {
				errs <- err.Error()
				return
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != 200 || !strings.Contains(string(b), `"ret":1`) {
				errs <- string(b)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	u, _ = e.app.Q.GetUser(context.Background(), uid)
	if u.U != 23 || u.D != 38 {
		t.Fatalf("concurrent totals %d %d", u.U, u.D)
	}
	e.exec("UPDATE ss_node SET server='invalid' WHERE id=14")
	status, _, _ = e.response("GET", "/mod_mu/v2/nodes/14/users"+key, "")
	if status != 422 {
		t.Fatal("bad protocol must not masquerade as empty user list")
	}
	// Legacy contract is still the original shape, with no v2 credential fields injected.
	old := e.raw("GET", "/mod_mu/users?node_id=16&key=test-node-key", "", "")
	if !strings.Contains(old, `"passwd":`) || strings.Contains(old, `"api_version":`) {
		t.Fatal("legacy API changed")
	}
}

// TestQuantumultXEmptyShortIDRoute 通过订阅接口验证两个 Quantumult X 入口（?quantumultx=1、?list=quantumultx）：
// short ID 为空的 VLESS REALITY 节点保留；只剩这一个节点时也返回 200，不出现“没有兼容该客户端的节点”。（第三方 P2）
func TestQuantumultXEmptyShortIDRoute(t *testing.T) {
	e := newEnv(t)
	uid := e.login("qx-sid@example.com")
	seedCustom(e)
	e.exec("UPDATE user SET enable=1,expire_in=?,class_expire=?,transfer_enable=1073741824 WHERE id=?", time.Now().Add(time.Hour), time.Now().Add(time.Hour), uid)
	e.exec("UPDATE ss_node SET server = ? WHERE id = 15", "e.co;443;0;tcp;reality;sni=s.co|pbk="+fixturePublic+"|sid=")
	token, err := e.app.Sub.SubToken(context.Background(), uid, 0)
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, wantOthers bool) {
		t.Helper()
		for _, q := range []string{"quantumultx=1", "list=quantumultx"} {
			status, _, body := e.response("GET", "/link/"+token+"?"+q, "")
			if status != 200 || !strings.Contains(body, ", reality-hex-shortid=, ") || !strings.Contains(body, "tag=VLESS\n") {
				t.Fatalf("%s %s: 空 short ID 的 VLESS 节点应保留: %d\n%s", name, q, status, body)
			}
			if wantOthers != (strings.Contains(body, "tag=SS2022\n") && strings.Contains(body, "tag=VMess\n")) {
				t.Fatalf("%s %s: 其他节点不正确:\n%s", name, q, body)
			}
		}
	}
	check("混合", true)
	e.exec("UPDATE ss_node SET type = 0 WHERE id <> 15")
	check("只有空 short ID 节点", false)
}

// TestSubscriptionFileNames 订阅下载的文件名按客户端软件命名、扩展名与 Content-Type 对应内容格式；附件下载并禁止类型猜测。
func TestSubscriptionFileNames(t *testing.T) {
	e := newEnv(t)
	uid := e.login("filename@example.com")
	seedCustom(e)
	e.exec("UPDATE user SET enable=1,expire_in=?,class_expire=?,transfer_enable=1073741824 WHERE id=?", time.Now().Add(time.Hour), time.Now().Add(time.Hour), uid)
	token, err := e.app.Sub.SubToken(context.Background(), uid, 0)
	if err != nil {
		t.Fatal(err)
	}
	const yamlType, textType, jsonType = "text/yaml; charset=utf-8", "text/plain; charset=utf-8", "application/json; charset=utf-8"
	for q, want := range map[string][2]string{
		"clash=1": {"Clash.yaml", yamlType}, "list=clash": {"Clash.yaml", yamlType}, "mu=4": {"Clash.yaml", yamlType},
		"surge=5": {"Surge5.conf", textType}, "surge=4": {"Surge4.conf", textType}, "list=surge": {"Surge.txt", textType},
		"loon=1": {"Loon.conf", textType}, "list=loon": {"Loon.txt", textType}, "surfboard=1": {"Surfboard.conf", textType},
		"quantumult=3": {"Quantumult.conf", textType}, "quantumult=2": {"Quantumult.conf", textType}, "quantumult=1": {"Quantumult.txt", textType},
		"sing-box=1": {"sing-box.json", jsonType}, "quantumultx=1": {"QuantumultX.txt", textType}, "list=quantumultx": {"QuantumultX.txt", textType},
		"shadowrocket=1": {"Shadowrocket.txt", textType}, "v2ray=1": {"v2rayN.txt", textType}, "list=v2rayn": {"v2rayN.txt", textType},
		"kitsunebi=1": {"Kitsunebi.txt", textType}, "ss2022=1": {"SS2022.txt", textType}, "sub=2": {"SS2022.txt", textType},
		"sub=3": {"VMess.txt", textType}, "mu=2": {"VMess.txt", textType}, "vless=1": {"VLESS.txt", textType}, "anytls=1": {"AnyTLS.txt", textType},
	} {
		status, h, body := e.response("GET", "/link/"+token+"?"+q, "")
		if status != 200 || body == "" {
			t.Errorf("%s: %d %s", q, status, body)
			continue
		}
		if got := h.Get("Content-Disposition"); got != `attachment; filename="`+want[0]+`"` || h.Get("Content-Type") != want[1] || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: Content-Disposition=%q Content-Type=%q nosniff=%q，应为 %s / %s", q, got, h.Get("Content-Type"), h.Get("X-Content-Type-Options"), want[0], want[1])
		}
	}
	_, _, clash := e.response("GET", "/link/"+token+"?clash=1", "")
	if !strings.HasPrefix(clash, "mixed-port: 7890\n") || !strings.Contains(clash, "\ndns:\n  enable: true\n") {
		t.Fatalf("Clash 订阅应为块状 YAML 并包含 DNS 设置:\n%.300s", clash)
	}
}
