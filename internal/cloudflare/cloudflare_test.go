package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCF 模拟 Cloudflare API，保存 A 记录。
type fakeCF struct {
	mu      sync.Mutex
	records map[string]map[string]any // id -> 记录
	auth    []string
}

func (f *fakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Header.Get("Authorization")+"|"+r.Header.Get("X-Auth-Email")+"|"+r.Header.Get("X-Auth-Key"))
	write := func(result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	}
	switch {
	case r.URL.Path == "/zones":
		if r.URL.Query().Get("name") != "example.com" {
			write([]any{})
			return
		}
		write([]map[string]string{{"id": "z1"}})
	case r.URL.Path == "/zones/z1/dns_records" && r.Method == http.MethodGet:
		var out []map[string]string
		for id, rec := range f.records {
			if rec["name"] == r.URL.Query().Get("name") {
				out = append(out, map[string]string{"id": id})
			}
		}
		write(out)
	case r.URL.Path == "/zones/z1/dns_records" && r.Method == http.MethodPost:
		var rec map[string]any
		_ = json.NewDecoder(r.Body).Decode(&rec)
		f.records["r"+string(rune('0'+len(f.records)))] = rec
		write(rec)
	case strings.HasPrefix(r.URL.Path, "/zones/z1/dns_records/") && r.Method == http.MethodPatch:
		var rec map[string]any
		_ = json.NewDecoder(r.Body).Decode(&rec)
		f.records[strings.TrimPrefix(r.URL.Path, "/zones/z1/dns_records/")] = rec
		write(rec)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":7003,"message":"No route"}]}`))
	}
}

func TestUpdateA(t *testing.T) {
	f := &fakeCF{records: map[string]map[string]any{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := New("", "token", "example.com")
	c.BaseURL = srv.URL
	ctx := context.Background()

	if err := c.UpdateA(ctx, "hk1.example.com", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateA(ctx, "hk1.example.com", "2.2.2.2"); err != nil {
		t.Fatal(err)
	}
	if len(f.records) != 1 || f.records["r0"]["content"] != "2.2.2.2" {
		t.Fatalf("应添加一条后修改: %v", f.records)
	}
	if f.auth[0] != "Bearer token||" {
		t.Errorf("API Token 认证头错误: %q", f.auth[0])
	}

	g := New("a@b.c", "globalkey", "example.com")
	g.BaseURL = srv.URL
	f.auth = nil
	_ = g.UpdateA(ctx, "hk1.example.com", "3.3.3.3")
	if f.auth[0] != "|a@b.c|globalkey" {
		t.Errorf("Global API Key 认证头错误: %q", f.auth[0])
	}

	g.Zone = "other.com"
	if err := g.UpdateA(ctx, "x.other.com", "1.1.1.1"); err == nil || !strings.Contains(err.Error(), "没有找到域名") {
		t.Errorf("不存在的域名应报错: %v", err)
	}
	g.BaseURL = srv.URL + "/bad"
	if err := g.UpdateA(ctx, "x.example.com", "1.1.1.1"); err == nil || !strings.Contains(err.Error(), "7003") {
		t.Errorf("接口错误应返回错误信息: %v", err)
	}
}
