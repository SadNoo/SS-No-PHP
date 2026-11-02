package view_test

import (
	"io/fs"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/view"
	"github.com/SadNoo/SS-No-PHP/web"
)

// TestErrorPagesRender 确保错误页面都能正常渲染，并且对数据做了转义。
func TestErrorPagesRender(t *testing.T) {
	tfs, err := fs.Sub(web.Templates, "templates")
	if err != nil {
		t.Fatal(err)
	}
	funcs := view.PHPFuncs(time.UTC)
	funcs["year"] = func() int { return 2026 }
	funcs["cfg"] = func(string) string { return "" }
	r, err := view.New(tfs, funcs, false, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	type app struct{ Name string }
	type cfg struct{ App app }
	type site struct{ Cfg cfg }
	data := map[string]any{"Site": site{Cfg: cfg{App: app{Name: "<script>x</script>"}}}}
	for _, p := range []string{"error/404", "error/405", "error/500"} {
		w := httptest.NewRecorder()
		if err := r.Render(w, 200, "error", p, data); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		body := w.Body.String()
		if strings.Contains(body, "<script>x</script>") {
			t.Fatalf("%s: 站点名称未转义", p)
		}
	}
}
