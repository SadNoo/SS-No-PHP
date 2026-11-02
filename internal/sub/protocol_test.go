package sub

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// Public, deterministic fixture material only; these keys have never been used by a server.
const testServerKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
const testPublicKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

func TestProtocolCredentials(t *testing.T) {
	u := store.User{ID: 7, Passwd: "test-only-password"}
	if got := SS2022UserKey(u, 14, testServerKey); got != "cftaKeSZSIyd/E/szs0krokXEZAcyEZJ0UnAfbtLjgw=" {
		t.Fatalf("SS2022 vector: %s", got)
	}
	if got := AnyTLSPassword(u); got != "qHV4wiNqrx_r-y0xbPiBMzwe4DrKaMCSZnP6z7DWuWI" {
		t.Fatalf("AnyTLS vector: %s", got)
	}
	if SS2022UserKey(u, 15, testServerKey) == SS2022UserKey(u, 14, testServerKey) {
		t.Fatal("SS2022 must be node specific")
	}
	for _, n := range []store.SsNode{{ID: 14, Sort: SortSS2022, Server: "example.com;443;" + testServerKey}, {ID: 15, Sort: SortVLESS, Server: "e.com;443;0;tcp;reality;sni=s.com|pbk=" + testPublicKey + "|sid=ab"}, {ID: 16, Sort: SortAnyTLS, Server: "e.com;443;0;tcp;anytls;sni=s.com"}} {
		p, err := ProtocolProxy(u, n)
		if err != nil {
			t.Fatal(err)
		}
		p.Name = "Test 名字"
		uri, err := url.Parse(p.URI())
		if err != nil || uri.Host != "example.com:443" && uri.Host != "e.com:443" {
			t.Fatalf("bad URI %s: %v", p.URI(), err)
		}
		if p.Kind == "ss" {
			if uri.User.String() == "" || !strings.Contains(p.URI(), "%3A") {
				t.Fatal("SS2022 compound credential must be percent encoded")
			}
		}
		if _, err := p.SingBoxOutbound(); err != nil {
			t.Fatal(err)
		}
		if p.Kind == "vless" && p.TextLine("surge") != "" {
			t.Fatal("Surge must not receive VLESS")
		}
		if p.Kind == "anytls" && !strings.HasPrefix(p.TextLine("quantumultx"), "anytls=") {
			t.Fatal("QX must receive AnyTLS")
		}
	}
	for _, c := range []struct {
		sort   int32
		server string
	}{{14, "a;443;bad"}, {15, "a;443;0;tcp;reality;sni=x|pbk=" + testPublicKey}, {15, "a;443;0;tcp;reality;sni=x|pbk=" + testPublicKey + "|sid=a"}, {16, "a;80;0;tcp;anytls;sni=x"}, {16, "a;443;0;tcp;anytls;sni=x|bad=y"}, {16, "a;443;0;tcp;anytls;sni=1.1.1.1"}} {
		if _, err := ParseProtocol(c.sort, c.server); err == nil {
			t.Errorf("accepted invalid format %d", c.sort)
		}
	}
}

func TestRequestValidation(t *testing.T) {
	for _, q := range []string{"sing-box=1&sb_version=1.15&quic=1&tailscale=1", "sing-box=1", "surge=4", "surge=5", "list=surge", "anytls=1", "sub=3", "mu=4", "v2ray=1"} {
		v, _ := url.ParseQuery(q)
		if _, err := ParseRequest(v); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
	for _, q := range []string{"sing-box=1&sb_version=1.16", "sing-box=1&sb_version=1.14&sb_version=1.15", "sing-box=1&sb_version[]=1.15", "sing-box=1&quic=true", "surge=6", "mu=4&clash=1", "anytls=1&sb_version=1.15", "list=unknown"} {
		v, _ := url.ParseQuery(q)
		if _, err := ParseRequest(v); err == nil {
			t.Errorf("accepted invalid query %s", q)
		}
	}
}

func TestApprovedSingBoxPolicies(t *testing.T) {
	b, err := os.ReadFile("testdata/singbox-approved.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]any
	if err := json.Unmarshal(b, &fixtures); err != nil {
		t.Fatal(err)
	}
	p := Proxy{Name: "audit-node", Kind: "vmess", Server: "node.example", Port: 443, UUID: "00000000-0000-4000-8000-000000000001", Transport: "tcp"}
	for key, want := range fixtures {
		t.Run(key, func(t *testing.T) {
			v := strings.SplitN(key, "-", 2)[0]
			got, err := SingBox([]Proxy{p}, v, strings.Contains(key, "tailscale"), strings.Contains(key, "quic"), testProxyDomains)
			if err != nil {
				t.Fatal(err)
			}
			var actual any
			if err := json.Unmarshal([]byte(got), &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, want) {
				a, _ := json.MarshalIndent(actual, "", " ")
				w, _ := json.MarshalIndent(want, "", " ")
				t.Errorf("approved policy changed\ngot %s\nwant %s", a, w)
			}
		})
	}
}

func TestClientExports(t *testing.T) {
	proxies := []Proxy{{Name: "SS2022", Kind: "ss", Server: "e.com", Port: 443, Method: SS2022Method, Password: testServerKey + ":" + testServerKey}, {Name: "AnyTLS", Kind: "anytls", Server: "e.com", Port: 443, Password: "test-only", SNI: "s.com"}, {Name: "VLESS", Kind: "vless", Server: "e.com", Port: 443, UUID: "00000000-0000-4000-8000-000000000001", SNI: "s.com", PublicKey: testPublicKey, ShortID: "ab"}}
	surge, err := TextClient(proxies, "surge5", false, testProxyDomains)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"[Proxy]", "[Proxy Group]", "[Rule]", "encrypt-method=" + SS2022Method, "anytls,", "FINAL,☁️ Others", "127.0.0.0/8", "::1/128"} {
		if !strings.Contains(surge, s) {
			t.Errorf("Surge missing %s", s)
		}
	}
	if strings.Contains(surge, "VLESS =") {
		t.Fatal("Surge contains VLESS")
	}
	clash, err := ModernClash(proxies, false, testProxyDomains)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(clash), &parsed); err != nil || parsed["proxies"] == nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	for _, r := range []string{"surge", "loon", "quantumultx"} {
		list, err := TextClient(proxies, r, true, testProxyDomains)
		if err != nil || strings.Contains(list, "[General]") {
			t.Fatalf("list %s: %v", r, err)
		}
	}

	vmess := Proxy{Name: "VMess", Kind: "vmess", Server: "e.com", Port: 443, UUID: "00000000-0000-4000-8000-000000000001", Transport: "tcp"}
	all := append(proxies, vmess, Proxy{Name: "LegacySS", Kind: "ss", Server: "e.com", Port: 443, Method: "aes-128-gcm", Password: "test-only"})
	for _, version := range []string{"surge4", "surge5"} {
		body, err := TextClient(all, version, false, testProxyDomains)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body, "VMess = vmess") || strings.Contains(body, "VLESS =") || strings.Contains(body, "LegacySS =") {
			t.Fatalf("%s protocol boundary", version)
		}
		// Surge 4、5 都包含 SS2022；只有 Surge 5 包含 AnyTLS。
		if !strings.Contains(body, "SS2022 = ss") || strings.Contains(body, "AnyTLS =") != (version == "surge5") {
			t.Fatalf("%s protocol inclusion", version)
		}
	}

	u := store.User{Enable: 1, ExpireIn: time.Now().Add(time.Hour), TransferEnable: 100}
	if !activeUser(u, time.Now()) {
		t.Fatal("active user rejected")
	}
	u.U = 100
	if activeUser(u, time.Now()) {
		t.Fatal("exhausted user accepted")
	}
	n := store.SsNode{Type: 1, NodeClass: 1}
	if NodeAllowed(u, n) {
		t.Fatal("class bypass")
	}
	u.IsAdmin = 1
	if !NodeAllowed(u, n) {
		t.Fatal("admin rejected")
	}
	if _, err := base64.StdEncoding.DecodeString(testServerKey); err != nil {
		t.Fatal(err)
	}
}

// Set explicit local core paths to validate every mode without bundling binaries in the repository.
func TestSingBoxCoreCheck(t *testing.T) {
	for _, entry := range []struct{ version, env string }{{"1.14", "SINGBOX_CHECK_114"}, {"1.15", "SINGBOX_CHECK_115"}} {
		core := os.Getenv(entry.env)
		if core == "" {
			continue
		}
		for _, mode := range []struct {
			name       string
			tail, quic bool
		}{{"ordinary", false, false}, {"quic", false, true}, {"tailscale", true, false}, {"tailscale-quic", true, true}} {
			t.Run(entry.version+"-"+mode.name, func(t *testing.T) {
				nodes := []Proxy{{Name: "VMess", Kind: "vmess", Server: "node.example", Port: 443, UUID: "00000000-0000-4000-8000-000000000001", Transport: "ws", Path: "/test", TLS: true, SNI: "s.example"}, {Name: "SS2022", Kind: "ss", Server: "192.0.2.1", Port: 443, Method: SS2022Method, Password: testServerKey + ":" + testServerKey}, {Name: "VLESS", Kind: "vless", Server: "192.0.2.1", Port: 443, UUID: "00000000-0000-4000-8000-000000000001", SNI: "s.example", PublicKey: testPublicKey, ShortID: "ab"}, {Name: "AnyTLS", Kind: "anytls", Server: "192.0.2.1", Port: 443, Password: "test-only", SNI: "s.example"}}
				content, err := SingBox(nodes, entry.version, mode.tail, mode.quic, testProxyDomains)
				if err != nil {
					t.Fatal(err)
				}
				dir := t.TempDir()
				filename := filepath.Join(dir, "config.json")
				if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(core, "check", "-c", filename)
				cmd.Dir = dir
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("core check: %v\n%s", err, output)
				}
			})
		}
	}
}

func TestModernVMessTLS(t *testing.T) {
	for _, server := range []string{"e.com;443;0;tls;ws;host=s.example|path=/test", "e.com;443;0;ws;tls;host=s.example|path=/test"} {
		v := VMessConfig(server)
		if v.Str("net") != "ws" || v.Str("tls") != "tls" || v.Str("path") != "/test" {
			t.Fatalf("TLS transport lost: %s", server)
		}
	}
}
