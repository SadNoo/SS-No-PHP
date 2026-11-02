#!/usr/bin/env bash
# 检查 Docker 构建上下文（.dockerignore）：在 deploy/prod 等位置放入假的秘密、证书、备份文件，确认它们不会进入构建，
# 构建上下文只含编译需要的源码，并且用正式的 Dockerfile 能编译出 panel。需要 Docker；CI 会运行。
#
#   bash deploy/check-build-context.sh
set -euo pipefail
cd "$(dirname "$0")/.."
IMG=niuniupanel-ctxcheck-$$
BASE=${CTXCHECK_BASE_IMAGE:-alpine:3.22}

# 假文件：已存在的同名文件不覆盖、检查后不删除；新建的检查后删除。
fakes=(
  deploy/prod/.env deploy/prod/.env.panel deploy/prod/Caddyfile
  deploy/prod/certs/loaded/ctxcheck.pem deploy/prod/certs/ctxcheck.key
  deploy/prod/config/config.toml deploy/prod/data/qqwry.dat deploy/prod/backups/niuniupanel-ctxcheck.sql.gz
  config.toml .env internal/ctxcheck.key web/ctxcheck.pem cmd/.env.panel
)
created=()
cleanup() {
  for f in "${created[@]}"; do rm -f "$f"; rmdir "$(dirname "$f")" 2>/dev/null || true; done
  docker image rm -f "$IMG" "$IMG-panel" >/dev/null 2>&1 || true
}
trap cleanup EXIT
for f in "${fakes[@]}"; do
  if [ ! -e "$f" ]; then
    mkdir -p "$(dirname "$f")"
    echo "CTXCHECK-SECRET" > "$f"
    created+=("$f")
  fi
done

# 1. 列出构建上下文中的全部文件（与正式构建使用同一个 .dockerignore）。
docker build -q -t "$IMG" -f - . >/dev/null <<EOF
FROM $BASE
COPY . /ctx
RUN cd /ctx && find . -type f | sed 's|^\./||' | sort > /ctx-files.txt
EOF
files="$(docker run --rm "$IMG" cat /ctx-files.txt)"

fail=0
bad="$(grep -vE '^(go\.mod|go\.sum|(cmd|db|internal|web)/.+)$' <<<"$files" || true)"
if [ -n "$bad" ]; then
  echo "构建上下文包含不应有的文件：" >&2; echo "$bad" >&2; fail=1
fi
for f in "${fakes[@]}"; do
  if grep -qxF "$f" <<<"$files"; then echo "构建上下文包含 $f" >&2; fail=1; fi
done
if docker run --rm "$IMG" grep -rl CTXCHECK-SECRET /ctx >/dev/null 2>&1; then
  echo "构建上下文中发现假秘密内容" >&2; fail=1
fi
for f in go.mod go.sum cmd/panel/main.go db/embed.go web/embed.go; do
  grep -qxF "$f" <<<"$files" || { echo "构建上下文缺少 $f" >&2; fail=1; }
done
[ $fail = 0 ] || exit 1
echo "构建上下文：$(echo "$files" | wc -l) 个文件，只有 go.mod、go.sum、cmd/、db/、internal/、web/，没有部署文件与秘密"

# 2. 用正式的 Dockerfile 构建，确认白名单没有漏掉编译需要的文件。
if [ "${CTXCHECK_SKIP_BUILD:-}" != 1 ]; then
  docker build -q -t "$IMG-panel" . >/dev/null
  docker run --rm "$IMG-panel" version >/dev/null
  echo "正式 Dockerfile 构建成功"
fi
