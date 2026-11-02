package app

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

// Pager 对应原版 Eloquent 的 paginate()：模板中用 .Items 遍历当前页，用 .Render 输出分页链接。
type Pager struct {
	Items   any
	Page    int
	PerPage int
	Total   int64
	Path    string
}

// pageParam 读取 ?page=，非法值按第 1 页处理。
func pageParam(r *http.Request) int {
	p, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || p < 1 {
		return 1
	}
	return min(p, 1_000_000)
}

// newPager 根据总数和当前页计算偏移量。
func newPager(r *http.Request, path string, perPage int, total int64) *Pager {
	return &Pager{Page: pageParam(r), PerPage: perPage, Total: total, Path: path}
}

// Offset 当前页第一条记录的偏移量（供 SQL 使用）。
func (p *Pager) Offset() int32 { return int32((p.Page - 1) * p.PerPage) } //nolint:gosec // 页码已限制在 100 万以内

// Limit 每页条数（供 SQL 使用）。
func (p *Pager) Limit() int32 { return int32(p.PerPage) } //nolint:gosec // 每页条数为程序常量

// LastPage 最后一页的页码（至少为 1）。
func (p *Pager) LastPage() int {
	if p.Total == 0 {
		return 1
	}
	return int((p.Total + int64(p.PerPage) - 1) / int64(p.PerPage))
}

func (p *Pager) url(page int) string {
	return p.Path + "?page=" + strconv.Itoa(page)
}

// Render 输出与 illuminate/pagination 5.2 BootstrapThreePresenter 相同的 HTML；只有一页时输出空字符串。
func (p *Pager) Render() template.HTML {
	last := p.LastPage()
	if last <= 1 {
		return ""
	}
	cur := p.Page
	var links strings.Builder
	page := func(n int) {
		if n == cur {
			fmt.Fprintf(&links, `<li class="active"><span>%d</span></li>`, n)
		} else {
			fmt.Fprintf(&links, `<li><a href="%s">%d</a></li>`, template.HTMLEscapeString(p.url(n)), n)
		}
	}
	rng := func(from, to int) {
		for n := from; n <= to; n++ {
			page(n)
		}
	}
	dots := func() { links.WriteString(`<li class="disabled"><span>...</span></li>`) }

	// UrlWindow：onEachSide = 3，window = 6。
	const onEachSide, window = 3, 6
	switch {
	case last < window+6:
		rng(1, last)
	case cur <= window:
		rng(1, window+2)
		dots()
		rng(last-1, last)
	case cur > last-window:
		rng(1, 2)
		dots()
		rng(last-(window+2), last)
	default:
		rng(1, 2)
		dots()
		rng(cur-onEachSide, cur+onEachSide)
		dots()
		rng(last-1, last)
	}

	var prev, next string
	if cur <= 1 {
		prev = `<li class="disabled"><span>&laquo;</span></li>`
	} else {
		prev = fmt.Sprintf(`<li><a href="%s" rel="prev">&laquo;</a></li>`, template.HTMLEscapeString(p.url(cur-1)))
	}
	if cur >= last {
		next = `<li class="disabled"><span>&raquo;</span></li>`
	} else {
		next = fmt.Sprintf(`<li><a href="%s" rel="next">&raquo;</a></li>`, template.HTMLEscapeString(p.url(cur+1)))
	}
	return template.HTML(`<ul class="pagination">` + prev + " " + links.String() + " " + next + `</ul>`) //nolint:gosec // 内容全部由程序生成
}
