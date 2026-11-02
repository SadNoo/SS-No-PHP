// Package view 负责页面渲染。
//
// 模板目录结构（web/templates）：
//
//	layouts/*.html     页面骨架，例如 layouts/error.html 定义模板 "layout/error"
//	partials/**.html   可复用片段，定义为 "partials/<路径>"（对应原版 {include file='...'}）
//	pages/**.html      具体页面：使用骨架时定义 "title"、"content" 等块；
//	                   原版那种自己 include 头尾的整页模板则定义 "page"
//
// 渲染时，每个页面都会和全部 layouts、partials 组合成独立的模板集，
// 再从指定的 layout 开始执行，因此不同页面定义的同名块不会互相覆盖。
package view

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
)

// Renderer 渲染页面。
type Renderer struct {
	fsys  fs.FS
	funcs template.FuncMap
	log   *slog.Logger

	mu    sync.RWMutex
	base  *template.Template
	pages map[string]*template.Template
	// reload 为 true 时每次渲染都重新解析模板（仅用于开发）。
	reload bool
}

// New 创建渲染器并预解析全部页面。funcs 为模板函数。
func New(fsys fs.FS, funcs template.FuncMap, reload bool, log *slog.Logger) (*Renderer, error) {
	r := &Renderer{fsys: fsys, funcs: funcs, reload: reload, log: log}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Renderer) load() error {
	base := template.New("").Funcs(r.funcs)
	for _, dir := range []string{"layouts", "partials"} {
		var files []string
		err := fs.WalkDir(r.fsys, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && path.Ext(p) == ".html" {
				files = append(files, p)
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if len(files) == 0 {
			continue
		}
		if base, err = base.ParseFS(r.fsys, files...); err != nil {
			return fmt.Errorf("解析模板 %s: %w", dir, err)
		}
	}
	pages := map[string]*template.Template{}
	err := fs.WalkDir(r.fsys, "pages", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Ext(p) != ".html" {
			return nil
		}
		t, err := base.Clone()
		if err != nil {
			return err
		}
		if _, err := t.ParseFS(r.fsys, p); err != nil {
			return fmt.Errorf("解析页面 %s: %w", p, err)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(p, "pages/"), ".html")
		pages[name] = t
		return nil
	})
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.base, r.pages = base, pages
	r.mu.Unlock()
	return nil
}

// Pages 返回全部页面名称，供渲染测试遍历。
func (r *Renderer) Pages() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.pages))
	for n := range r.pages {
		names = append(names, n)
	}
	return names
}

// Render 以 layout 为骨架渲染页面 page（layout 为空时执行页面自身定义的 "page" 模板），先写入缓冲区，成功后再输出，
// 避免模板执行到一半出错时返回半个页面。
func (r *Renderer) Render(w http.ResponseWriter, status int, layout, page string, data any) error {
	if r.reload {
		if err := r.load(); err != nil {
			return err
		}
	}
	r.mu.RLock()
	t, ok := r.pages[page]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("页面模板不存在: %s", page)
	}
	var buf bytes.Buffer
	entry := "page"
	if layout != "" {
		entry = "layout/" + layout
	}
	if err := t.ExecuteTemplate(&buf, entry, data); err != nil {
		return fmt.Errorf("渲染页面 %s: %w", page, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}
