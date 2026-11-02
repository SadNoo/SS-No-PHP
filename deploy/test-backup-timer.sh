#!/usr/bin/env bash
# 回归测试：upgrade.sh / backup-timer.sh 不会改变每日备份定时任务的启用与运行状态。
# 不需要 root、Docker 或 systemd：git、docker、systemctl、sleep 都是测试桩，定时任务文件写在临时目录。CI 会运行。
#
#   bash deploy/test-backup-timer.sh
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fail=0
check() { if eval "$2"; then echo "  ✓ $1"; else echo "  ✗ $1" >&2; fail=1; fi; }

mkdir -p "$T/bin"
# systemctl 桩：状态保存在 $STATE/<单元>.enabled / .active，调用记录在 $STATE/calls
cat > "$T/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
echo "$*" >> "$STATE/calls"
cmd=$1; shift
now=""; [ "${1:-}" = --now ] && { now=1; shift; }
[ "${1:-}" = --quiet ] && shift
u=${1:-}
en() { cat "$STATE/$u.enabled" 2>/dev/null || echo disabled; }
ac() { cat "$STATE/$u.active" 2>/dev/null || echo inactive; }
case "$cmd" in
  daemon-reload|show) ;;
  enable)  echo enabled > "$STATE/$u.enabled"; [ -n "$now" ] && echo active > "$STATE/$u.active" ;;
  disable) echo disabled > "$STATE/$u.enabled"; [ -n "$now" ] && echo inactive > "$STATE/$u.active" ;;
  start|restart) echo active > "$STATE/$u.active" ;;
  stop) echo inactive > "$STATE/$u.active" ;;
  try-restart) [ "$(ac)" = active ] && echo "restarted $u" >> "$STATE/restarts" ;;
  is-enabled) en; [ "$(en)" = enabled ] ;;
  is-active) ac; [ "$(ac)" = active ] ;;
  *) echo "unexpected systemctl $cmd" >&2; exit 1 ;;
esac
exit 0
EOF
cat > "$T/bin/docker" <<'EOF'
#!/usr/bin/env bash
echo "docker $*" >> "$STATE/calls"
case "$*" in
  "compose version") exit 0 ;;
  *healthz*) [ "${HEALTH_OK:-1}" = 1 ] ;;
  *) exit 0 ;;
esac
EOF
printf '#!/bin/sh\nexit 0\n' > "$T/bin/git"
printf '#!/bin/sh\nexit 0\n' > "$T/bin/sleep"
chmod +x "$T/bin/"*

OLD_TIMER='[Unit]
Description=niuniupanel 每日数据库备份
[Timer]
OnCalendar=*-*-* 04:30:00
Persistent=true
[Install]
WantedBy=timers.target'

# setup <名称> <是否有定时任务文件 yes|no> <enabled|disabled> <active|inactive>
setup() {
  D="$T/$1"; mkdir -p "$D/prod" "$D/units" "$D/state"
  cp "$ROOT/deploy/prod/upgrade.sh" "$ROOT/deploy/prod/backup-timer.sh" "$D/prod/"
  printf '#!/bin/sh\necho backup >> "$STATE/calls"\n' > "$D/prod/backup.sh"
  printf '#!/bin/sh\nexit 0\n' > "$D/prod/ensure-dirs.sh"
  chmod +x "$D/prod/"*.sh
  if [ "$2" = yes ]; then
    echo "$OLD_TIMER" > "$D/units/niuniupanel-backup.timer"
    echo "$3" > "$D/state/niuniupanel-backup.timer.enabled"
    echo "$4" > "$D/state/niuniupanel-backup.timer.active"
  fi
  : > "$D/state/calls"
}
run_upgrade() { (cd "$D/prod" && PATH="$T/bin:$PATH" STATE="$D/state" NIUNIU_SYSTEMD_DIR="$D/units" HEALTH_OK="${1:-1}" bash ./upgrade.sh >"$D/out" 2>&1); }
state() { echo "$(cat "$D/state/niuniupanel-backup.timer.enabled" 2>/dev/null)/$(cat "$D/state/niuniupanel-backup.timer.active" 2>/dev/null)"; }
taipei() { grep -q 'OnCalendar=\*-\*-\* 04:30:00 Asia/Taipei' "$D/units/niuniupanel-backup.timer"; }
no_enable() { ! grep -qE '^(enable|start|restart) ' "$D/state/calls"; }

echo "① 曾启用、之后关闭（disable --now 或 --no-auto-backup，定时任务文件仍在）"
setup c1 yes disabled inactive; run_upgrade; rc=$?
check "升级成功" "[ $rc = 0 ]"
check "保持 disabled/inactive（实际 $(state)）" '[ "$(state)" = disabled/inactive ]'
check "没有 enable / start / restart" no_enable
check "定时任务内容更新为台北时间" taipei

echo "② 从未启用（没有定时任务文件）"
setup c2 no; run_upgrade; rc=$?
check "升级成功" "[ $rc = 0 ]"
check "不创建定时任务文件" '[ ! -e "$D/units/niuniupanel-backup.timer" ]'
check "没有调用 systemctl" '! grep -qvE "^(docker|backup)" "$D/state/calls"'

echo "③ 已启用且正常运行"
setup c3 yes enabled active; run_upgrade; rc=$?
check "升级成功" "[ $rc = 0 ]"
check "保持 enabled/active（实际 $(state)）" '[ "$(state)" = enabled/active ]'
check "重启一次以使用新的时间（try-restart）" 'grep -q restarted "$D/state/restarts" 2>/dev/null'
check "没有 enable / start / restart" no_enable
check "定时任务内容更新为台北时间" taipei

echo "④ 已启用但暂时停止"
setup c4 yes enabled inactive; run_upgrade; rc=$?
check "升级成功" "[ $rc = 0 ]"
check "保持 enabled/inactive（实际 $(state)）" '[ "$(state)" = enabled/inactive ]'
check "没有重启" '[ ! -s "$D/state/restarts" ]'
check "没有 enable / start / restart" no_enable

echo "⑤ 升级健康检查失败"
setup c5 yes disabled inactive; run_upgrade 0; rc=$?
check "升级失败（退出码非 0）" "[ $rc != 0 ]"
check "定时任务文件不变" '[ "$(cat "$D/units/niuniupanel-backup.timer")" = "$OLD_TIMER" ]'
check "保持 disabled/inactive（实际 $(state)）" '[ "$(state)" = disabled/inactive ]'
check "没有调用 systemctl" '! grep -qvE "^(docker|backup)" "$D/state/calls"'
check "没有清理镜像与构建缓存" '! grep -q prune "$D/state/calls"'

echo "⑥ 从旧版本升级后手动运行 ./backup-timer.sh update（关闭状态）"
setup c6 yes disabled inactive
(cd "$D/prod" && PATH="$T/bin:$PATH" STATE="$D/state" NIUNIU_SYSTEMD_DIR="$D/units" bash ./backup-timer.sh update >"$D/out" 2>&1); rc=$?
check "运行成功" "[ $rc = 0 ]"
check "保持 disabled/inactive（实际 $(state)）" '[ "$(state)" = disabled/inactive ]'
check "定时任务内容更新为台北时间" taipei
check "提示保持原状" 'grep -q "保持原状" "$D/out"'

echo "⑦ ./backup-timer.sh install 明确开启"
setup c7 yes disabled inactive
(cd "$D/prod" && PATH="$T/bin:$PATH" STATE="$D/state" NIUNIU_SYSTEMD_DIR="$D/units" bash ./backup-timer.sh install >"$D/out" 2>&1); rc=$?
check "运行成功" "[ $rc = 0 ]"
check "变为 enabled/active（实际 $(state)）" '[ "$(state)" = enabled/active ]'

[ $fail = 0 ] && echo "全部通过" || { echo "有失败项" >&2; exit 1; }
