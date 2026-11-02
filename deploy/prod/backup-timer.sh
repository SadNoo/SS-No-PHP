#!/usr/bin/env bash
# 每日数据库备份的系统定时任务：每天台北时间（UTC+8）04:30 运行 backup.sh，与服务器自身的时区无关。需要 root 与 systemd。
#
#   ./backup-timer.sh install   写入定时任务并启用、启动（install.sh 在选择自动备份时调用）
#   ./backup-timer.sh update    只更新定时任务的内容（默认）：不启用、不启动、不停用。原来已启用的保持启用，
#                               已停用的保持停用；原来正在运行的重启一次以使用新的时间，没有运行的保持不运行。
#                               upgrade.sh 调用；从旧版本升级后可单独运行一次，把旧的“服务器时区 04:30”改为台北时间。
set -euo pipefail
cd "$(dirname "$0")"
UNIT_DIR=${NIUNIU_SYSTEMD_DIR:-/etc/systemd/system}   # 测试时指向临时目录
mode=${1:-update}
case "$mode" in install|update) ;; *) echo "用法: $0 [install|update]" >&2; exit 2 ;; esac

WHEN='*-*-* 04:30:00 Asia/Taipei'
cat > "$UNIT_DIR/niuniupanel-backup.service" <<EOF
[Unit]
Description=niuniupanel 数据库备份
[Service]
Type=oneshot
ExecStart=$(pwd)/backup.sh
EOF
cat > "$UNIT_DIR/niuniupanel-backup.timer" <<EOF
[Unit]
Description=niuniupanel 每日数据库备份（台北时间 04:30）
[Timer]
OnCalendar=$WHEN
Persistent=true
[Install]
WantedBy=timers.target
EOF
systemctl daemon-reload
if [ "$mode" = install ]; then
  systemctl enable niuniupanel-backup.timer >/dev/null 2>&1
  systemctl restart niuniupanel-backup.timer
else
  # try-restart 只重启正在运行的单元，停止或停用的保持原状；启用状态（开机自启）不变。
  systemctl try-restart niuniupanel-backup.timer
fi
enabled=$(systemctl is-enabled niuniupanel-backup.timer 2>/dev/null || true)
active=$(systemctl is-active niuniupanel-backup.timer 2>/dev/null || true)
if [ "$active" = active ]; then
  next="$(systemctl show niuniupanel-backup.timer -p NextElapseUSecRealtime --value 2>/dev/null || true)"
  echo "每日备份：台北时间 04:30（${enabled:-unknown}，运行中${next:+，下次：$(TZ=Asia/Taipei date -d "$next" '+%F %T' 2>/dev/null || echo "$next") 台北时间}）"
else
  echo "每日备份定时任务已更新为台北时间 04:30，当前未运行（${enabled:-unknown} / ${active:-unknown}），保持原状；需要时运行 ./backup-timer.sh install"
fi
