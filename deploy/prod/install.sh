#!/usr/bin/env bash
# niuniupanel 一键安装（Debian 12 / 13，Docker）。可以重复运行：已生成的密钥会保留。
#
#   ./install.sh --domains "panel.example.com panel.example.net" --name "我的面板"
#
# 第一个域名是主域名（邮件、订阅链接使用），其余域名同样可以访问面板。
# 运行前把每个域名所属的 Cloudflare 源服务器证书放到 certs/ 目录：
#   certs/example.com.crt  certs/example.com.key   （文件名任意，.crt 与 .key 同名即可）
# 详细说明见 docs/deploy-docker.md。
set -euo pipefail
cd "$(dirname "$0")"

DOMAINS=""
NAME=""
ADMIN_EMAIL=""
SKIP_ADMIN=0
AUTO_BACKUP=""   # 1 / 0：命令行明确指定；空：沿用上次的选择（见第 6 步）

usage() {
  sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'
  echo "参数："
  echo "  --domains \"域名1 域名2 ...\"   面板域名，第一个为主域名（必填，首次安装）"
  echo "  --name \"站点名称\"              默认 SS-No-PHP"
  echo "  --admin-email 邮箱             创建管理员（会提示输入密码）"
  echo "  --skip-admin                   不创建管理员"
  echo "  --no-auto-backup               关闭每日自动备份（需要时手动运行 ./backup.sh）；选择会记住"
  echo "  --auto-backup                  开启每日自动备份（首次安装默认开启；之后重新运行沿用上次的选择）"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --domains) DOMAINS="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --admin-email) ADMIN_EMAIL="$2"; shift 2 ;;
    --skip-admin) SKIP_ADMIN=1; shift ;;
    --no-auto-backup) AUTO_BACKUP=0; shift ;;
    --auto-backup) AUTO_BACKUP=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "未知参数: $1"; usage; exit 1 ;;
  esac
done

say() { printf '\n\033[1;34m==> %s\033[0m\n' "$*"; }
die() { printf '\n\033[1;31m错误：%s\033[0m\n' "$*" >&2; exit 1; }

# ---- 1. Docker ----
say "检查 Docker"
if ! command -v docker >/dev/null 2>&1; then
  [ "$(id -u)" = 0 ] || die "未安装 Docker，请用 root 运行本脚本以自动安装，或先自行安装 Docker"
  echo "未安装 Docker，正在用 apt 安装 docker.io 与 docker-compose ..."
  apt-get update -qq
  apt-get install -y -qq docker.io docker-compose >/dev/null || die "自动安装失败，请按 https://docs.docker.com/engine/install/debian/ 安装 Docker"
  systemctl enable --now docker
fi
if docker compose version >/dev/null 2>&1; then
  COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1 && docker-compose version 2>/dev/null | grep -q 'v\?2\.'; then
  COMPOSE=(docker-compose)
else
  die "需要 Docker Compose v2（docker compose 或 docker-compose 2.x）"
fi
command -v openssl >/dev/null 2>&1 || die "需要 openssl（apt install openssl）"

# ---- 2. 配置与密钥 ----
rand() { openssl rand -hex "$1"; }
get_var() { [ -f "$2" ] && grep -E "^$1=" "$2" | tail -n1 | cut -d= -f2- || true; }

if [ -z "$DOMAINS" ]; then
  DOMAINS="$(get_var PANEL_DOMAINS .env)"
  [ -n "$DOMAINS" ] || die "首次安装需要 --domains \"域名1 域名2\""
fi
read -r -a DOMAIN_LIST <<<"$DOMAINS"
MAIN="${DOMAIN_LIST[0]}"
NAME="${NAME:-$(get_var NIUNIU_APP_NAME .env.panel)}"
NAME="${NAME:-SS-No-PHP}"

say "生成配置（主域名 $MAIN）"
umask 077
DB_ROOT_PASSWORD="$(get_var DB_ROOT_PASSWORD .env)"; DB_ROOT_PASSWORD="${DB_ROOT_PASSWORD:-$(rand 24)}"
DB_PASSWORD="$(get_var DB_PASSWORD .env)";           DB_PASSWORD="${DB_PASSWORD:-$(rand 24)}"
DB_BUFFER_POOL="$(get_var DB_BUFFER_POOL .env)";     DB_BUFFER_POOL="${DB_BUFFER_POOL:-256M}"
PREV_AUTO_BACKUP="$(get_var AUTO_BACKUP .env)"
cat > .env <<EOF
# docker compose 使用的变量（由 install.sh 生成，请妥善保管，不要提交到 Git）
PANEL_DOMAINS=$DOMAINS
DB_ROOT_PASSWORD=$DB_ROOT_PASSWORD
DB_PASSWORD=$DB_PASSWORD
DB_BUFFER_POOL=$DB_BUFFER_POOL
EOF
# 记住自动备份的选择：命令行明确指定的写入；没有指定时保留原有记录（重新运行 install.sh 不会改变）
SAVED_AUTO_BACKUP="$PREV_AUTO_BACKUP"
[ -n "$AUTO_BACKUP" ] && SAVED_AUTO_BACKUP=$AUTO_BACKUP
[ -n "$SAVED_AUTO_BACKUP" ] && echo "AUTO_BACKUP=$SAVED_AUTO_BACKUP" >> .env

if [ ! -f .env.panel ]; then
  cat > .env.panel <<EOF
# 面板配置（环境变量形式，键名对应 config.example.toml：NIUNIU_<段名>_<键名>）。
# 修改后执行 docker compose up -d 生效。由 install.sh 首次生成，之后重复运行不会覆盖本文件（站点名、主域名除外）。
NIUNIU_APP_NAME=$NAME
NIUNIU_APP_BASE_URL=https://$MAIN
NIUNIU_APP_KEY=$(rand 32)
NIUNIU_SECURITY_MU_KEY=$(rand 24)
NIUNIU_SECURITY_COOKIE_SECURE=true

# 从 SSPanel-UIM 迁移时必须与原版一致（原版 salt / pwdMethod），否则老用户无法登录：
# NIUNIU_SECURITY_LEGACY_SALT=
# NIUNIU_SECURITY_LEGACY_PWD_METHOD=md5
# 节点密钥沿用原版 muKey 时，把上面的 NIUNIU_SECURITY_MU_KEY 改成原值。

# 购买说明（充值、套餐页面顶部）：
# NIUNIU_PAYMENT_NOTICE=添加微信 xxx 好友购买套餐

# 邮件（注册验证、找回密码需要）：
# NIUNIU_MAIL_DRIVER=smtp
# NIUNIU_MAIL_SMTP_HOST=smtp.example.com
# NIUNIU_MAIL_SMTP_PORT=465
# NIUNIU_MAIL_SMTP_USERNAME=
# NIUNIU_MAIL_SMTP_PASSWORD=
# NIUNIU_MAIL_SMTP_SENDER=
# NIUNIU_MAIL_SMTP_NAME=
EOF
else
  sed -i -e "s|^NIUNIU_APP_NAME=.*|NIUNIU_APP_NAME=$NAME|" -e "s|^NIUNIU_APP_BASE_URL=.*|NIUNIU_APP_BASE_URL=https://$MAIN|" .env.panel
fi
mkdir -p data certs backups
# 配置文件（群组入口等）首次从模板复制、之后不覆盖；并确保容器中的普通用户可以读取 config/ 与 data/
# （上面的 umask 077 会让新目录只有 root 可进入）。
bash ./ensure-dirs.sh

# ---- 3. 证书 ----
say "检查证书"
mkdir -p certs/loaded
rm -f certs/loaded/*.pem
shopt -s nullglob
for crt in certs/*.crt certs/*.pem; do
  key="${crt%.*}.key"
  [ -f "$key" ] || die "证书 $crt 缺少同名私钥 $key"
  openssl x509 -in "$crt" -noout >/dev/null 2>&1 || die "$crt 不是有效的证书"
  cat "$crt" "$key" > "certs/loaded/$(basename "${crt%.*}").pem"
done
LOADED=(certs/loaded/*.pem)
[ ${#LOADED[@]} -gt 0 ] || die "certs/ 中没有证书。请在 Cloudflare 的 SSL/TLS → 源服务器 中创建源服务器证书，保存为 certs/<名称>.crt 与 certs/<名称>.key"
for d in "${DOMAIN_LIST[@]}"; do
  ok=0
  for c in "${LOADED[@]}"; do
    if openssl x509 -in "$c" -noout -checkhost "$d" 2>/dev/null | grep -q "does match"; then ok=1; break; fi
  done
  [ $ok = 1 ] || die "没有覆盖域名 $d 的证书（Cloudflare 源服务器证书需包含 $d 或 *.${d#*.}）"
  echo "  $d ✓"
done
chmod 644 certs/loaded/*.pem   # Caddy 容器以普通用户读取（目录仍为 700）

# ---- 4. Caddy 配置 ----
say "生成 Caddyfile（只采信来自 Cloudflare 的真实 IP 头）"
CF_V4="$(curl -fsS --max-time 10 https://www.cloudflare.com/ips-v4 2>/dev/null || true)"
CF_V6="$(curl -fsS --max-time 10 https://www.cloudflare.com/ips-v6 2>/dev/null || true)"
if ! echo "$CF_V4" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+$'; then
  echo "  无法获取 Cloudflare 最新 IP 段，使用内置列表"
  CF_V4="173.245.48.0/20 103.21.244.0/22 103.22.200.0/22 103.31.4.0/22 141.101.64.0/18 108.162.192.0/18 190.93.240.0/20 188.114.96.0/20 197.234.240.0/22 198.41.128.0/17 162.158.0.0/15 104.16.0.0/13 104.24.0.0/14 172.64.0.0/13 131.0.72.0/22"
  CF_V6="2400:cb00::/32 2606:4700::/32 2803:f800::/32 2405:b500::/32 2405:8100::/32 2a06:98c0::/29 2c0f:f248::/32"
fi
CF_RANGES="$(echo $CF_V4 $CF_V6)"
SITES=""
for d in "${DOMAIN_LIST[@]}"; do SITES="${SITES:+$SITES, }https://$d"; done
cat > Caddyfile <<EOF
# 由 install.sh 生成，修改域名请重新运行 install.sh
{
	auto_https disable_certs
	servers {
		trusted_proxies static $CF_RANGES
		trusted_proxies_strict
		client_ip_headers CF-Connecting-IP
	}
}

$SITES {
	tls {
		load /certs/loaded
	}
	encode zstd gzip
	reverse_proxy panel:8080 {
		header_up X-Forwarded-For {client_ip}
	}
}
EOF

# ---- 5. 启动 ----
say "构建并启动（首次需要几分钟）"
"${COMPOSE[@]}" up -d --build
echo -n "等待面板就绪"
for _ in $(seq 1 90); do
  if "${COMPOSE[@]}" exec -T panel wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1; then echo " ✓"; break; fi
  echo -n "."; sleep 2
done
"${COMPOSE[@]}" exec -T panel wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1 || die "面板未能启动，查看日志：${COMPOSE[*]} logs panel"

# ---- 6. 每日备份 ----
# 只有明确开启（--auto-backup，或首次安装的默认）才启用定时任务；已存在的定时任务只更新内容、保持原来的启用 / 运行状态，
# 用户关闭后（--no-auto-backup 或 systemctl disable）重新运行 install.sh 不会重新打开。
TIMER=${NIUNIU_SYSTEMD_DIR:-/etc/systemd/system}/niuniupanel-backup.timer
if [ "$SAVED_AUTO_BACKUP" = 0 ]; then
  if [ -f "$TIMER" ]; then
    systemctl disable --now niuniupanel-backup.timer >/dev/null 2>&1 || true
    [ "$(id -u)" = 0 ] && bash ./backup-timer.sh update >/dev/null 2>&1 || true
  fi
  say "已关闭每日自动备份（选择已记住）：需要时运行 $(pwd)/backup.sh，重新开启加 --auto-backup"
elif [ "$(id -u)" = 0 ] && [ -d /run/systemd/system ]; then
  if [ "$AUTO_BACKUP" = 1 ] || [ ! -f "$TIMER" ]; then
    say "设置每日数据库备份（台北时间 04:30，保留最近 14 份，目录 $(pwd)/backups）"
    bash ./backup-timer.sh install
  else
    say "更新每日备份定时任务（保持原来的启用状态）"
    bash ./backup-timer.sh update
  fi
fi

# ---- 7. 管理员 ----
if [ $SKIP_ADMIN = 0 ] && [ -n "$ADMIN_EMAIL" ]; then
  say "创建管理员 $ADMIN_EMAIL"
  read -r -s -p "管理员密码（至少 8 位）：" ADMIN_PW; echo
  "${COMPOSE[@]}" exec -T panel panel xcat createAdmin "$ADMIN_EMAIL" "$ADMIN_PW"
  unset ADMIN_PW
fi

say "完成"
echo "面板地址：https://$MAIN"
for d in "${DOMAIN_LIST[@]:1}"; do echo "          https://$d"; done
echo "节点密钥：见 $(pwd)/.env.panel 中的 NIUNIU_SECURITY_MU_KEY"
echo "修改配置：编辑 .env.panel 后执行 ${COMPOSE[*]} up -d；群组入口写在 config/config.toml，修改后执行 ${COMPOSE[*]} restart panel"
[ $SKIP_ADMIN = 0 ] && [ -z "$ADMIN_EMAIL" ] && echo "创建管理员：${COMPOSE[*]} exec panel panel xcat createAdmin 邮箱 密码"
exit 0
