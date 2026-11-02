// Package logx 根据配置创建结构化日志。
package logx

import (
	"io"
	"log/slog"
	"strings"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

// New 创建日志器。
func New(c config.Log, w io.Writer) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(c.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if c.Format == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
