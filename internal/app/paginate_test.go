package app

import (
	"strings"
	"testing"
)

// TestPagerRender 对照 illuminate/pagination 5.2 BootstrapThreePresenter 的输出。
func TestPagerRender(t *testing.T) {
	p := &Pager{Page: 1, PerPage: 15, Total: 15, Path: "/user/x"}
	if p.Render() != "" {
		t.Fatal("只有一页时不应输出分页")
	}
	p = &Pager{Page: 1, PerPage: 15, Total: 16, Path: "/user/x"}
	want := `<ul class="pagination"><li class="disabled"><span>&laquo;</span></li> <li class="active"><span>1</span></li><li><a href="/user/x?page=2">2</a></li> <li><a href="/user/x?page=2" rel="next">&raquo;</a></li></ul>`
	if got := string(p.Render()); got != want {
		t.Fatalf("两页输出不一致:\n%s\n%s", got, want)
	}
	// 共 20 页、当前第 10 页：1 2 ... 7-13 ... 19 20
	p = &Pager{Page: 10, PerPage: 1, Total: 20, Path: "/p"}
	got := string(p.Render())
	for _, s := range []string{`?page=2">2</a></li><li class="disabled"><span>...</span></li><li><a href="/p?page=7">7`, `<span>10</span>`, `?page=13">13</a></li><li class="disabled"><span>...</span></li><li><a href="/p?page=19">19`} {
		if !strings.Contains(got, s) {
			t.Fatalf("中间页窗口不正确，缺少 %q:\n%s", s, got)
		}
	}
	// 当前第 3 页（靠近开头）：1-8 ... 19 20
	got = string((&Pager{Page: 3, PerPage: 1, Total: 20, Path: "/p"}).Render())
	if !strings.Contains(got, `?page=8">8</a></li><li class="disabled"><span>...</span></li><li><a href="/p?page=19">19`) {
		t.Fatalf("靠近开头的窗口不正确:\n%s", got)
	}
}
