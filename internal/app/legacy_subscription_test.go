package app

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func legacySubscriptionEnv(t *testing.T) (*testEnv, string) {
	t.Helper()
	e := newEnv(t)
	uid := e.login("legacy-subscriber@example.com")
	seedCustom(e)
	e.exec("UPDATE user SET enable=1,class=2,expire_in=?,class_expire=?,transfer_enable=1073741824,method='aes-128-cfb',protocol='origin',obfs='plain' WHERE id=?", time.Now().Add(time.Hour), time.Now().Add(time.Hour), uid)
	e.exec("UPDATE ss_node SET name='香港 SS2022 0' WHERE id=14")
	e.exec(`INSERT INTO ss_node (id,name,type,server,method,info,status,sort,node_class,node_ip,node_group) VALUES
 (20,'RetiredSS',1,'192.0.2.20','','','',0,0,'127.0.0.1',0),
 (21,'RetiredSS1',1,'192.0.2.21','','','',0,1,'127.0.0.1',0),
 (22,'DeniedClass',1,'192.0.2.22','','','',0,99,'127.0.0.1',0),
 (23,'DeniedGroup',1,'192.0.2.23','','','',0,0,'127.0.0.1',8),
 (24,'Kitsunebi VMess',1,'kit.example;443;0;ws;tls;path=/ws|host=kit.example','','','',11,0,'127.0.0.1',0),
 (26,'日本 SS2022 1',1,?,'','','',14,1,'127.0.0.1',0)`, "e1.example;443;"+fixtureKey)
	token, err := e.app.Sub.SubToken(context.Background(), uid, 0)
	if err != nil {
		t.Fatal(err)
	}
	return e, "/link/" + token
}

func TestLegacySubscriptionsFormats(t *testing.T) {
	e, base := legacySubscriptionEnv(t)
	for _, query := range []string{"surge=4", "surge=5", "quantumult=1", "quantumult=2", "quantumult=3", "list=quantumult", "kitsunebi=1", "list=kitsunebi", "surfboard=1", "list=ss", "sub=2", "sub=3", "mu=2", "mu=4", "clash=1", "sing-box=1&sb_version=1.15"} {
		status, h, body := e.response("GET", base+"?"+query, "")
		if status != http.StatusOK || body == "" || h.Get("Subscription-Userinfo") == "" {
			t.Fatalf("%s: %d %s", query, status, body)
		}
		if query == "kitsunebi=1" || query == "list=quantumult" || query == "sub=2" || query == "sub=3" || query == "mu=2" {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
			if err != nil && query == "mu=2" {
				decoded, err = base64.RawURLEncoding.DecodeString(strings.TrimSpace(body))
			}
			if err != nil {
				t.Fatal(err)
			}
			body = string(decoded)
		}
		for _, retired := range []string{"RetiredSS", "192.0.2.20", "192.0.2.21", "DeniedClass", "DeniedGroup", "ssr://", "aes-128-cfb"} {
			if strings.Contains(body, retired) {
				t.Fatalf("%s leaked retired/unauthorized node: %s", query, retired)
			}
		}
	}
}

func TestRetiredSubscriptionRoutes(t *testing.T) {
	e, base := legacySubscriptionEnv(t)
	for _, query := range []string{"", "?mu=0", "?mu=1", "?sub=1", "?list=ssr", "?list=clashr", "?clash=2", "?ssd=1", "?list=ssd", "?mu=3", "?surge=1", "?surge=2", "?surge=3", "?list=ssa", "?list=quantumult-ss"} {
		status, _, body := e.response("GET", base+query, "")
		if status != http.StatusGone || !strings.Contains(body, "已停用") {
			t.Fatalf("retirement %s: %d %s", query, status, body)
		}
	}
}

func TestSubscriptionFilterHTTPAndPages(t *testing.T) {
	// Independent fixtures retain the production subscription rate limiter.
	e, base := legacySubscriptionEnv(t)
	for _, query := range []string{"sub=2&class=0%2B1&noclass=1", "sub=2&class=0+1&noclass=1"} {
		_, _, encoded := e.response("GET", base+"?"+query, "")
		body, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil || !strings.Contains(string(body), "e.example") || !strings.Contains(string(body), "e1.example") {
			t.Fatalf("class precedence / plus decoding: %s %v", body, err)
		}
		for _, line := range strings.Fields(string(body)) {
			u, err := url.Parse(line)
			if err != nil || u.Scheme != "ss" || u.User.Username() != "2022-blake3-aes-256-gcm" {
				t.Fatalf("SS must exclusively use SS2022: %s", line)
			}
		}
	}
	_, _, denied := e.response("GET", base+"?sub=2&class=99", "")
	if denied != "" {
		t.Fatal("out-of-range class must not fall back to all nodes")
	}
	_, _, legacyClash := e.response("GET", base+"?mu=4&class=0", "")
	if !strings.Contains(legacyClash, "Proxy:\n") || strings.Contains(legacyClash, "AnyTLS") || strings.Contains(legacyClash, "SS2022") || strings.Contains(legacyClash, "VLESS") || strings.Contains(legacyClash, "RetiredSS") {
		t.Fatal("old Clash retains only VMess without switching formats")
	}
	_, _, vmessBase := e.response("GET", base+"?mu=2", "")
	_, _, vmessFiltered := e.response("GET", base+"?mu=2&class=0", "")
	if vmessBase != vmessFiltered {
		t.Fatal("filters changed VMess encoding or transport despite all nodes matching")
	}
	status, _, body := e.response("GET", base+"?list=ss&class=0&regex="+url.QueryEscape("香港")+"&emoji=1&extend=0", "")
	if status != 200 || !strings.Contains(body, "%F0%9F%87%AD%F0%9F%87%B0") || strings.Contains(body, "e1.example") {
		t.Fatalf("name / emoji filtering: %d %s", status, body)
	}
	_, _, ssAlias := e.response("GET", base+"?sub=2&type=ss", "")
	_, _, ss2022 := e.response("GET", base+"?ss2022=1", "")
	if ssAlias != ss2022 {
		t.Fatal("SS alias differs from SS2022-only subscription")
	}
	for _, query := range []string{"sub=2&class[]=1", "sub=2&regex=[", "sub=2&extend=2", "list=legacy", "sub=2&class=0&class=1"} {
		status, _, _ := e.response("GET", base+"?"+query, "")
		if status != 400 {
			t.Fatalf("invalid filter %s: %d", query, status)
		}
	}
	page := e.page("/user")
	for _, content := range []string{"Kitsunebi", "Surfboard", "sub-class", "sub-noclass", "sub-regex", "sub-extend", `data-sub-copy="quantumult"`, `data-sub-copy="quantumult-source"`, "SS（SS2022）"} {
		if !strings.Contains(page, content) {
			t.Fatalf("missing UI: %s", content)
		}
	}
	for _, retired := range []string{"id=\"all_ssr\"", "id=\"all_ss\"", "?mu=0", "?mu=1", "?mu=3", "ssr://", "SSD", "?surge=2", "?surge=3", "list=ssa"} {
		if strings.Contains(page, retired) {
			t.Fatalf("retired UI still exposed: %s", retired)
		}
	}
}
