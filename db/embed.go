// Package db 打包表结构变更文件和 sqlc 查询文件。
package db

import "embed"

// Migrations 是 goose 表结构变更文件。
//
//go:embed migrations/*.sql
var Migrations embed.FS
