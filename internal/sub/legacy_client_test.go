package sub

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

func requestFor(t *testing.T, query string) Request {
	t.Helper()
	q, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseRequest(q)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return r
}

func TestLegacyFormatParametersAndRetirement(t *testing.T) {
	for _, q := range []string{"surge=4", "surge=5", "quantumult=1", "quantumult=2", "quantumult=3", "list=quantumult", "kitsunebi=1", "list=kitsunebi", "surfboard=1", "list=ss"} {
		requestFor(t, q)
	}
	for _, query := range []string{"", "sub=1", "mu=0", "mu=1", "clash=2", "list=clashr", "list=ssr", "ssd=1", "list=ssd", "mu=3", "surge=1", "surge=2", "surge=3", "list=ssa", "list=quantumult-ss"} {
		q, _ := url.ParseQuery(query)
		if _, err := ParseRequest(q); !errors.Is(err, ErrSSRRetired) && !errors.Is(err, ErrLegacySSRetired) {
			t.Errorf("SSR request not retired: %q: %v", query, err)
		}
	}
	for _, query := range []string{"list=legacy", "quantumult=4", "quantumult[]=1", "kitsunebi=1&kitsunebi=1", "sub=2&type=ssr", "sub=2&class[]=1", "sub=2&class=1&class=2", "sub=2&class=-1", "sub=2&class=x", "sub=2&class=2147483648", "sub=2&class=", "sub=2&regex=(?=x)", "sub=2&regex=%2Ffoo%2Fg", "sub=2&regex=[", "sub=2&emoji=yes", "sub=2&extend[]=1", "sub=2&extend=2", "surge=3&source=https://example.com/x", "surge=3&profiles=123456"} {
		q, _ := url.ParseQuery(query)
		if _, err := ParseRequest(q); err == nil {
			t.Errorf("invalid options accepted: %q", query)
		}
	}
	if _, err := ParseRequest(url.Values{"sub": {"2"}, "class": {}}); err == nil {
		t.Error("empty option slice must not panic or pass")
	}
}

func TestFilterIntersectionNamesAndClassPrecedence(t *testing.T) {
	available := []Proxy{{Name: "香港 HK 01", Kind: "ss", Class: 0, Method: "aes-128-gcm"}, {Name: "日本 JP 02", Kind: "vmess", Class: 1}, {Name: "香港 HK SS2022", Kind: "ss", Class: 1, Method: SS2022Method}}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"sub=2&class=0+1&noclass=1&regex=%2Fhk%2Fi&emoji=1", []string{"🇭🇰 香港 HK 01", "🇭🇰 香港 HK SS2022"}},
		{"sub=2&noclass=1", []string{"香港 HK 01"}},
		{"sub=2&class=99", nil},
		{"sub=2&type=ss2022", []string{"香港 HK SS2022"}},
		{"sub=2&type=vmess&emoji=0", []string{"日本 JP 02"}},
		{"sub=2&regex=~%E6%97%A5%E6%9C%AC~u&emoji=1", []string{"🇯🇵 日本 JP 02"}},
	} {
		r := requestFor(t, tc.query)
		got := r.Filter.proxies(available)
		var names []string
		for _, p := range got {
			names = append(names, p.Name)
		}
		if !reflect.DeepEqual(names, tc.want) {
			t.Errorf("%s got %v want %v", tc.query, names, tc.want)
		}
	}
	if available[0].Name != "香港 HK 01" {
		t.Fatal("filter mutated its input")
	}
	f := requestFor(t, "sub=2&emoji=1").Filter
	if got := f.name("🇭🇰 香港 HK"); got != "🇭🇰 香港 HK" {
		t.Fatalf("duplicate country flag: %s", got)
	}
}

func TestLegacyURIEncodingsAndCapabilities(t *testing.T) {
	p := Proxy{Name: "香港 & 测试", Kind: "vmess", Server: "node.example", Port: 443, UUID: "00000000-0000-4000-8000-000000000001", AlterID: 0, TLS: true, Transport: "ws", Host: "node.example", Path: "/ws?ed=1&v=2"}
	kit := kitsunebiURI(p)
	u, err := url.Parse(kit)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := base64.StdEncoding.DecodeString(u.Host)
	if err != nil || string(credential) != "auto:"+p.UUID+"@node.example:443" {
		t.Fatalf("wrong Kitsunebi credential encoding: %s", kit)
	}
	if u.Query().Get("remark") != p.Name || u.Query().Get("wspath") != p.Path || u.Query().Get("wsHost") != p.Host || u.Query().Get("tls") != "1" || u.Query().Get("allowInsecure") != "0" {
		t.Fatalf("lost transport / TLS options: %s", kit)
	}
	for _, r := range []Request{requestFor(t, "kitsunebi=1"), requestFor(t, "list=quantumult")} {
		body, err := renderLegacyClient([]Proxy{p}, r, testProxyDomains)
		if err != nil {
			t.Fatal(err)
		}
		outer, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
		if err != nil || !strings.HasPrefix(string(outer), "vmess://") {
			t.Fatal("missing subscription outer Base64")
		}
		if r.Format == "quantumult" {
			inner, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(strings.TrimSpace(string(outer)), "vmess://"))
			if err != nil || !strings.Contains(string(inner), " = vmess, node.example, 443,") || !strings.Contains(string(inner), "certificate=1") || !strings.Contains(string(inner), p.UUID) {
				t.Fatalf("Quantumult must carry native text, not JSON: %s", outer)
			}
		}
	}
	for _, kind := range []string{"anytls", "vless"} {
		p.Kind = kind
		if kitsunebiURI(p) != "" || quantumultLine(p) != "" {
			t.Fatal("unsupported modern protocol leaked to old client")
		}
	}
	p.Kind, p.SNI = "vmess", "different.example"
	if kitsunebiURI(p) != "" {
		t.Fatal("Kitsunebi must not silently disable verification for unsupported custom SNI")
	}
}

func TestLegacyCompleteProfileSnapshots(t *testing.T) {
	nodes := append(textProfileNodes(), Proxy{Name: "SS Stream", Kind: "ss", Server: "192.0.2.4", Port: 8388, Method: "aes-256-cfb", Password: "test-only-password"})
	for _, client := range []string{"surfboard", "quantumult"} {
		var body string
		var err error
		if client == "quantumult" {
			body, err = renderLegacyClient(nodes, requestFor(t, "quantumult=3"), testProxyDomains)
		} else {
			body, err = legacyTextProfile(nodes, client, false, testProxyDomains)
		}
		if err != nil {
			t.Fatal(err)
		}
		if (client == "quantumult" && strings.Contains(body, "SS2022 =")) || strings.Contains(body, "LegacySS =") || strings.Contains(body, "SS Stream =") || strings.Contains(body, "VLESS =") || strings.Contains(body, "AnyTLS =") || strings.Contains(body, "ssr://") {
			t.Fatalf("unsupported protocol in %s", client)
		}
		for _, s := range []string{"127.0.0.0/8", "::1/128", "FINAL,☁️ Others", "site1.example", "site2.example", "🫥SiriAI"} {
			if !strings.Contains(body, s) {
				t.Fatalf("%s missing %s", client, s)
			}
		}
		if client != "quantumult" {
			sections := profileSections(t, body)
			proxies := profileSettings(t, sections["Proxy"])
			groups := profileSettings(t, sections["Proxy Group"])
			known := map[string]bool{"DIRECT": true, "REJECT": true}
			for name := range proxies {
				known[name] = true
			}
			for name := range groups {
				known[name] = true
			}
			for _, value := range groups {
				for _, member := range strings.Split(value, ",")[1:] {
					if !known[strings.TrimSpace(member)] {
						t.Fatalf("dangling legacy group member: %s", member)
					}
				}
			}
			for _, rule := range sections["Rule"] {
				parts := strings.Split(rule, ",")
				policy := parts[len(parts)-1]
				if policy == "no-resolve" {
					policy = parts[len(parts)-2]
				}
				if !known[policy] {
					t.Fatalf("dangling legacy rule policy: %s", rule)
				}
			}
		}
		if client == "quantumult" {
			sections := profileSections(t, body)
			if len(sections["POLICY"]) != 6 {
				t.Fatal("missing native Quantumult groups")
			}
			for _, encoded := range sections["POLICY"] {
				group, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil || !strings.Contains(string(group), " : static, ") {
					t.Fatal("invalid native policy group")
				}
			}
		}
		path := filepath.Join("testdata", "text-profiles", client+"-legacy.conf")
		if os.Getenv("UPDATE_LEGACY_PROFILES") == "1" {
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil || string(want) != body {
			t.Fatalf("%s snapshot mismatch: %v", client, err)
		}
	}
}

func TestQuantumultSourcesKeepFiltersWithoutSSR(t *testing.T) {
	s := &Service{ProxyDomains: testProxyDomains, AppName: "Test Panel", SubURL: "https://panel.example/link/", TokenFunc: func(context.Context, uint32, int32) (string, error) { return "test-only-token", nil }}
	r := requestFor(t, "quantumult=2&class=0%2B1&regex=%2Fhk%2Fi&emoji=1&extend=0")
	body, err := s.quantumultSources(context.Background(), store.User{ID: 7}, r, textProfileNodes())
	if err != nil {
		t.Fatal(err)
	}
	sections := profileSections(t, body)
	if len(sections["POLICY"]) != 6 || len(sections["SOURCE"]) != 1 {
		t.Fatal("incomplete source profile")
	}
	for _, line := range sections["SOURCE"] {
		parts := strings.Split(line, ",")
		u, err := url.Parse(strings.TrimSpace(parts[2]))
		if err != nil || u.Query().Get("class") != "0+1" || u.Query().Get("regex") != "/hk/i" || u.Query().Get("extend") != "0" || strings.Contains(line, "ssr") {
			t.Fatalf("source filters/format changed: %s", line)
		}
		if _, err := ParseRequest(u.Query()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExtendedMetadata(t *testing.T) {
	u := store.User{Passwd: "private-account-value", TransferEnable: 1 << 30, U: 10, D: 20, ClassExpire: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}
	for _, query := range []string{"sub=2", "sub=3", "v2ray=1", "kitsunebi=1", "list=quantumult", "shadowrocket=1", "ss2022=1"} {
		r := requestFor(t, query+"&extend=1")
		base := base64.StdEncoding.EncodeToString([]byte("real-node-first\n"))
		ext := withSubscriptionInfo(base, r, u, time.Now(), time.UTC)
		if ext != withSubscriptionInfo(base, r, u, time.Now(), time.UTC) {
			t.Fatal("non-deterministic metadata encoding")
		}
		plain, err := base64.StdEncoding.DecodeString(ext)
		if err != nil || !strings.HasPrefix(string(plain), "real-node-first\n") || strings.Contains(string(plain), u.Passwd) || ext == base {
			t.Fatalf("invalid optional metadata %s: %v", query, err)
		}
	}
	for _, query := range []string{"sing-box=1", "clash=1", "vless=1", "anytls=1"} {
		r := requestFor(t, query+"&extend=1")
		if withSubscriptionInfo("{\"native\":true}", r, u, time.Now(), time.UTC) != "{\"native\":true}" {
			t.Fatal("native config must retain metadata in its header")
		}
	}
}
