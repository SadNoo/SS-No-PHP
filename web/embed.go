// Package web 打包页面模板和静态资源。
//
// 静态资源（Material 主题样式、图片等）来自 SSPanel-UIM（MIT 许可证，见 static/LICENSE-SSPanel-UIM）。
package web

import "embed"

// Templates 是页面模板。
//
//go:embed templates
var Templates embed.FS

// Static 是静态资源，按原路径对外提供（/theme/...、/assets/...、/images/...）。
//
//go:embed static
var Static embed.FS
