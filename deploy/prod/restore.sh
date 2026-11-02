#!/usr/bin/env bash
# 从备份恢复数据库：./restore.sh backups/niuniupanel-xxxx.sql.gz（会覆盖当前数据，执行前会再备份一次）
set -euo pipefail
cd "$(dirname "$0")"
[ -f "${1:-}" ] || { echo "用法: $0 backups/文件名.sql.gz"; exit 1; }
if docker compose version >/dev/null 2>&1; then COMPOSE=(docker compose); else COMPOSE=(docker-compose); fi
read -r -p "将用 $1 覆盖当前数据库，确认请输入 yes：" ans
[ "$ans" = yes ] || exit 1
./backup.sh
"${COMPOSE[@]}" stop panel
gunzip -c "$1" | "${COMPOSE[@]}" exec -T db sh -c 'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" niuniupanel'
"${COMPOSE[@]}" start panel
echo "已恢复"
