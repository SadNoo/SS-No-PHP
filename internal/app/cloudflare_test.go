package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/cloudflare"
	"github.com/SadNoo/SS-No-PHP/internal/config"
	"github.com/SadNoo/SS-No-PHP/internal/store"
)

func TestCloudflareSyncFilter(t *testing.T) {
	hit := make(chan string, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit <- r.URL.String()
		_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.DNS.CloudflareEnable, cfg.DNS.CloudflareKey, cfg.DNS.CloudflareName = true, "k", "example.com"
	c := cloudflare.New("", "k", "example.com")
	c.BaseURL = srv.URL
	a := &App{Cfg: &cfg, CF: c, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	for _, n := range []store.SsNode{
		{Server: "hk.other.com", NodeIp: "1.1.1.1"},          // 不在该域名下
		{Server: "1.1.1.1", NodeIp: "1.1.1.1"},               // 直接使用 IP
		{Server: "hk.example.com", NodeIp: "hk.example.com"}, // 未解析出 IP
	} {
		a.cloudflareSync(n)
	}
	select {
	case u := <-hit:
		t.Fatalf("不应请求 Cloudflare: %s", u)
	case <-time.After(200 * time.Millisecond):
	}
	a.cloudflareSync(store.SsNode{Server: "HK.example.com;port=443", NodeIp: "1.1.1.1"})
	select {
	case <-hit:
	case <-time.After(3 * time.Second):
		t.Fatal("应同步 Cloudflare")
	}
}
