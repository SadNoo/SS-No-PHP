#!/usr/bin/env bash
# 备份数据库到 backups/（gzip 压缩），保留最近 14 份。install.sh 会把它设为每天台北时间 04:30 自动执行（backup-timer.sh）。
# 文件名中的时间是台北时间（UTC+8）。可以用 1Panel「文件」打开本目录下的 backups/ 直接下载。
set -euo pipefail
cd "$(dirname "$0")"
if docker compose version >/dev/null 2>&1; then COMPOSE=(docker compose); else COMPOSE=(docker-compose); fi
mkdir -p backups
f="backups/niuniupanel-$(TZ=Asia/Taipei date +%Y%m%d-%H%M%S).sql.gz"
umask 077

# 空间不足时不备份，避免把磁盘写满（需要的空间按上一份备份的两倍估算，至少 200 MB）。
last="$(ls -1t backups/niuniupanel-*.sql.gz 2>/dev/null | head -n1 || true)"
need=204800
if [ -n "$last" ] && [ $(( $(stat -c %s "$last") * 2 / 1024 )) -gt $need ]; then need=$(( $(stat -c %s "$last") * 2 / 1024 )); fi
avail="$(df -Pk backups | awk 'NR==2 {print $4}')"
if [ "$avail" -lt "$need" ]; then
  echo "备份失败：磁盘剩余 $((avail / 1024)) MB，不足 $((need / 1024)) MB。请先清理空间（见 docs/deploy-docker.md「磁盘空间」）" >&2
  exit 1
fi

"${COMPOSE[@]}" exec -T db sh -c 'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction --routines --set-gtid-purged=OFF niuniupanel 2>/dev/null' | gzip > "$f.tmp"
[ -s "$f.tmp" ] && gzip -t "$f.tmp" || { rm -f "$f.tmp"; echo "备份失败" >&2; exit 1; }
mv "$f.tmp" "$f"
ls -1t backups/niuniupanel-*.sql.gz | tail -n +15 | xargs -r rm -f
echo "已备份：$f（$(du -h "$f" | cut -f1)；backups/ 共 $(du -sh backups | cut -f1)，磁盘剩余 $(df -Ph backups | awk 'NR==2 {print $4}')）"
pct="$(df -P backups | awk 'NR==2 {gsub("%","",$5); print $5}')"
if [ "$pct" -ge 85 ]; then
  echo "注意：磁盘已使用 ${pct}%，请清理空间（见 docs/deploy-docker.md「磁盘空间」）" >&2
fi
