package view_test

import (
	"bytes"
	"html/template"
	"os"
	"strings"
	"testing"

	"github.com/SadNoo/SS-No-PHP/web"
)

func TestSubscriptionCenterRender(t *testing.T) {
	tmpl, err := template.New("root").ParseFS(web.Templates, "templates/partials/user/subscription-center.html")
	if err != nil {
		t.Fatal(err)
	}
	clients := []map[string]any{}
	for _, c := range []struct{ name, format, note, copyLabel, importLabel string }{{"Surge v4", "surge4", "VMess + SS2022", "复制订阅", "一键导入"}, {"Surge v5", "surge5", "SS2022 / VMess / AnyTLS", "复制订阅", "一键导入"}, {"Shadowrocket", "shadowrocket", "全部协议", "", "一键导入"}, {"Kitsunebi", "kitsunebi", "VMess", "复制订阅", ""}} {
		clients = append(clients, map[string]any{"Name": c.name, "Format": c.format, "Note": c.note, "CopyLabel": c.copyLabel, "ImportLabel": c.importLabel})
	}
	protocols := []map[string]any{{"Name": "SS / SS2022", "Format": "ss2022", "CopyLabel": "SS2022 订阅", "AllURL": "ss_url_all"}, {"Name": "AnyTLS", "Format": "anytls", "CopyLabel": "独立订阅", "AllURL": ""}}
	var b bytes.Buffer
	err = tmpl.ExecuteTemplate(&b, "partials/user/subscription-center", map[string]any{"Data": map[string]any{
		"subscription_base": "https://panel.example.com/link/test-only-token", "subscription_clients": clients, "protocol_subs": protocols, "ss_url_all": "ss://a\nss://<b>\n",
	}})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, s := range []string{`data-sub-import="surge4"`, `data-sub-import="surge5"`, `data-sub-import="shadowrocket"`, `data-sub-copy="kitsunebi"`, `data-sub-copy="ss2022"`, `data-sub-copy="anytls"`,
		"data-copy-text=\"ss://a\nss://&lt;b&gt;\n\"", "按协议订阅", `data-sb-mode="tailscale-quic"`, `id="sb-mode" value="ordinary"`, "target=\"_blank\"", "rel=\"noopener noreferrer\""} {
		if !strings.Contains(out, s) {
			t.Errorf("render missing %s", s)
		}
	}
	for _, s := range []string{`data-sub-copy="shadowrocket"`, `data-sub-import="kitsunebi"`, "data-sub-download"} {
		if strings.Contains(out, s) {
			t.Errorf("render should not contain %s", s)
		}
	}
	if strings.Count(out, "全部 URL") != 1 {
		t.Errorf("only SS2022 has 全部 URL here: %d", strings.Count(out, "全部 URL"))
	}
	if filename := os.Getenv("SUBSCRIPTION_PREVIEW_PATH"); filename != "" {
		page := `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>订阅中心测试预览</title><style>body{font-family:system-ui;max-width:760px;margin:24px auto;padding:0 16px;color:#555d72}a{color:#2698f6}button,select{font-size:16px;padding:10px}button{cursor:pointer}.btn{display:inline-block;margin:8px}</style>` + b.String() + `</html>`
		if err := os.WriteFile(filename, []byte(page), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
