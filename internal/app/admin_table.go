package app

import (
	"database/sql"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/sub"
)

// 后台列表页（原版 DataTables 服务端模式，Ozdemir\Datatables 与 Admin\UserController::ajax）。
//
// 列表的 SQL 由程序写定，请求中只有搜索词作为参数传入；排序字段只能是该列表已定义的列（K02：原版用户列表
// 把排序参数直接拼进 SQL）。原版把查询结果原样作为 HTML 输出，用户名、联络方式、工单标题等用户填写的内容
// 会在管理员浏览器中执行（K38），这里除操作按钮等程序生成的列外一律转义。

// tableCol 是列表的一列（原版 $table_config['total_column']）。
type tableCol struct {
	Key     string
	Title   string
	Default bool // 默认显示
	NoSort  bool // 不能排序（显示内容由程序计算、与数据库中的值顺序不同的列）
}

// tableConfig 对应原版 $table_config。
type tableConfig struct {
	Columns []tableCol
	AjaxURL string
}

// Keys 返回全部列名（模板脚本中使用）。
func (c tableConfig) Keys() []string {
	out := make([]string, 0, len(c.Columns))
	for _, col := range c.Columns {
		out = append(out, col.Key)
	}
	return out
}

// NoSortKeys 返回不能排序的列名（模板脚本中设为 orderable: false）。
func (c tableConfig) NoSortKeys() []string {
	out := []string{}
	for _, col := range c.Columns {
		if col.NoSort {
			out = append(out, col.Key)
		}
	}
	return out
}

// noSort 把指定的列设为不能排序。
func (c tableConfig) noSort(keys ...string) tableConfig {
	cols := make([]tableCol, len(c.Columns))
	copy(cols, c.Columns)
	for i := range cols {
		for _, k := range keys {
			if cols[i].Key == k {
				cols[i].NoSort = true
			}
		}
	}
	c.Columns = cols
	return c
}

// newTable 创建列表配置。cols 依次为 列名、标题；defaults 为空表示全部默认显示（与原版一致）。
func newTable(ajax string, defaults []string, cols ...string) tableConfig {
	cfg := tableConfig{AjaxURL: ajax}
	def := map[string]bool{}
	for _, d := range defaults {
		def[d] = true
	}
	for i := 0; i+1 < len(cols); i += 2 {
		cfg.Columns = append(cfg.Columns, tableCol{Key: cols[i], Title: cols[i+1], Default: len(defaults) == 0 || def[cols[i]]})
	}
	return cfg
}

// tableSpec 描述一个列表接口。
type tableSpec struct {
	// Query 是基础查询，列名即输出字段名；外层以子查询方式做搜索、排序与分页。
	Query string
	Args  []any
	// Columns 是可搜索、可排序的列（必须是 Query 中的列名）。
	Columns []string
	// NoOrder 中的列可以搜索但不按它排序（请求按这些列排序时按默认顺序，即 Columns[0]）。
	NoOrder []string
	// HTML 中的列输出原样 HTML（程序生成的按钮、管理员编写的公告等），其他列一律转义。
	HTML []string
	// Format 根据原始值改写输出（时间格式化、类型名称等）；返回的字段覆盖原值。
	Format func(row map[string]string) map[string]string
	// RowID 为 true 时输出 DT_RowId = row_1_{id}（原版删除行时使用）。
	RowID bool
}

func quoteIdent(s string) string { return "`" + strings.ReplaceAll(s, "`", "") + "`" }

// dataTable 处理 DataTables 服务端请求：draw、start、length、search[value]、order[0][column]、order[0][dir]、columns[i][data]。
func (a *App) dataTable(w http.ResponseWriter, r *http.Request, spec tableSpec) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		a.json(w, fail("非法请求"))
		return
	}
	draw, _ := strconv.Atoi(r.FormValue("draw"))
	start, _ := strconv.Atoi(r.FormValue("start"))
	// 未使用服务端分页的列表（如商品列表）不带 length，返回全部。
	length, err := strconv.Atoi(r.FormValue("length"))
	if err != nil || length == 0 {
		length = -1
	}
	start = max(start, 0)
	allowed := map[string]bool{}
	for _, c := range spec.Columns {
		allowed[c] = true
	}

	base := "SELECT * FROM (" + spec.Query + ") AS dt"
	where, whereArgs := "", []any{}
	if s := strings.TrimSpace(r.FormValue("search[value]")); s != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s) + "%"
		parts := make([]string, 0, len(spec.Columns))
		for _, c := range spec.Columns {
			parts = append(parts, "CAST("+quoteIdent(c)+" AS CHAR) LIKE ?")
			whereArgs = append(whereArgs, like)
		}
		where = " WHERE (" + strings.Join(parts, " OR ") + ")"
	}
	orderCol := ""
	if idx, err := strconv.Atoi(r.FormValue("order[0][column]")); err == nil {
		if c := r.FormValue("columns[" + strconv.Itoa(idx) + "][data]"); allowed[c] && !slices.Contains(spec.NoOrder, c) {
			orderCol = c
		}
	}
	if orderCol == "" && len(spec.Columns) > 0 {
		orderCol = spec.Columns[0]
	}
	dir := "ASC"
	if strings.EqualFold(r.FormValue("order[0][dir]"), "desc") {
		dir = "DESC"
	}

	var total, filtered int64
	if err := a.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM ("+spec.Query+") AS dt", spec.Args...).Scan(&total); err != nil {
		a.jsonError(w, r, err)
		return
	}
	filtered = total
	allArgs := append(append([]any{}, spec.Args...), whereArgs...)
	if where != "" {
		if err := a.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM ("+spec.Query+") AS dt"+where, allArgs...).Scan(&filtered); err != nil {
			a.jsonError(w, r, err)
			return
		}
	}
	q := base + where //nolint:gosec // 列名均来自程序定义的白名单，用户输入只作为参数
	if orderCol != "" {
		q += " ORDER BY " + quoteIdent(orderCol) + " " + dir
	}
	if length > 0 {
		q += " LIMIT " + strconv.Itoa(length) + " OFFSET " + strconv.Itoa(start)
	}
	rows, err := a.DB.QueryContext(ctx, q, allArgs...) //nolint:gosec // 同上
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	html := map[string]bool{}
	for _, c := range spec.HTML {
		html[c] = true
	}
	data := []any{}
	for rows.Next() {
		raw := make([]dbText, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			a.jsonError(w, r, err)
			return
		}
		row := make(map[string]string, len(cols))
		for i, c := range cols {
			row[c] = string(raw[i])
		}
		out := map[string]string{}
		for k, v := range row {
			out[k] = v
		}
		if spec.Format != nil {
			for k, v := range spec.Format(row) {
				out[k] = v
			}
		}
		m := sub.NewOMap()
		for _, c := range cols {
			m.Set(c, escapeUnless(out[c], html[c]))
		}
		for k, v := range out { // Format 新增的字段
			if _, ok := row[k]; !ok {
				m.Set(k, escapeUnless(v, html[k]))
			}
		}
		if spec.RowID {
			m.Set("DT_RowId", "row_1_"+row["id"])
		}
		data = append(data, m)
	}
	if err := rows.Err(); err != nil {
		a.jsonError(w, r, err)
		return
	}
	writePHPJSON(w, sub.NewOMap().Set("draw", draw).Set("recordsTotal", total).Set("recordsFiltered", filtered).Set("data", data))
}

func escapeUnless(v string, isHTML bool) string {
	if isHTML {
		return v
	}
	return template.HTMLEscapeString(v)
}

// opButtons 生成操作列的按钮（原版 onClick 改为 data-call）。
func editButton(href string) string {
	return `<a class="btn btn-brand" href="` + template.HTMLEscapeString(href) + `">编辑</a>`
}

// linkButton 是跳转到 href 的按钮（样式与“编辑”相同）。
func linkButton(href, text string) string {
	return `<a class="btn btn-brand" href="` + template.HTMLEscapeString(href) + `">` + text + `</a>`
}

func callButton(class, fn, id, text string, disabled bool) string {
	if disabled {
		return `<a class="btn ` + class + `" disabled>` + text + `</a>`
	}
	id = template.HTMLEscapeString(id)
	return `<a class="btn ` + class + `" href="javascript:void(0);" data-call="` + fn + `" data-args='["` + id + `"]'>` + text + `</a>`
}

// dbText 把查询结果的一列读成文本。连接开启了 parseTime，DATETIME / TIMESTAMP 列由驱动返回 time.Time，
// 直接读成字符串会变成 RFC 3339（2099-02-19T04:38:03Z）；这里统一为数据库原本的写法 "2006-01-02 15:04:05"（UTC），
// 与 fmtDBTime 等按数据库文本处理的代码一致。NULL 读成空字符串。
type dbText string

func (d *dbText) Scan(v any) error {
	switch t := v.(type) {
	case nil:
		*d = ""
	case time.Time:
		*d = dbText(t.UTC().Format(time.DateTime))
	default:
		var s sql.NullString
		if err := s.Scan(v); err != nil {
			return err
		}
		*d = dbText(s.String)
	}
	return nil
}
