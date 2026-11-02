#!/usr/bin/env bash
# 升级到 Git 上的最新版本：先备份数据库，再重新构建并重启（表结构变更在启动时自动执行）。
set -euo pipefail
cd "$(dirname "$0")"
if docker compose version >/dev/null 2>&1; then COMPOSE=(docker compose); else COMPOSE=(docker-compose); fi
git -C ../.. pull --ff-only
# 拉取后改用新版本的本脚本继续（正在运行的 bash 仍读取旧文件，新增的升级步骤要到下次升级才会执行）。
if [ -z "${NIUNIU_UPGRADE_PULLED:-}" ]; then NIUNIU_UPGRADE_PULLED=1 exec bash "$(pwd)/upgrade.sh" "$@"; fi
# 旧版本安装时没有配置文件：从模板复制（已有的不覆盖）；并确保容器中的普通用户可以读取 config/ 与 data/。
bash ./ensure-dirs.sh
./backup.sh
"${COMPOSE[@]}" up -d --build
ok=0
for _ in $(seq 1 90); do
  if "${COMPOSE[@]}" exec -T panel wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1; then ok=1; break; fi
  sleep 2
done
if [ $ok = 0 ]; then
  echo "面板未能启动，查看日志：${COMPOSE[*]} logs panel；如需回退见 docs/deploy-docker.md" >&2
  exit 1
fi
# 已有每日备份定时任务时，只更新为台北时间 04:30（旧版按服务器时区）：不启用、不启动，保持原来的启用 / 运行状态，
# 用户关闭的自动备份不会因为升级而重新打开。
if [ -f "${NIUNIU_SYSTEMD_DIR:-/etc/systemd/system}/niuniupanel-backup.timer" ]; then
  bash ./backup-timer.sh update || echo "更新备份定时任务失败（需要 root），可稍后单独运行 ./backup-timer.sh update" >&2
fi
# 清理每次升级留下的旧镜像与 7 天前的构建缓存，避免磁盘越用越满（不影响运行中的容器、数据库与备份）。
docker image prune -f >/dev/null 2>&1 || true
docker builder prune -f --filter until=168h >/dev/null 2>&1 || true
echo "升级完成"
