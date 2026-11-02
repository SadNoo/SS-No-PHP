#!/usr/bin/env bash
# 准备面板容器只读挂载的两个目录（install.sh、upgrade.sh 调用，也可单独运行）：
#   config/  配置文件 config.toml：不存在时从 config.toml.example 复制，已有的内容不改动；
#   data/    纯真 IP 库 qqwry.dat（放入后请运行一次本脚本）。
# 面板容器以普通用户 panel 运行：目录需可进入、文件需可读。install.sh 使用 umask 077，新建的目录会是 0700，
# 已有目录或复制进来的文件也可能只有 root 可读，因此这里只为上述目录与文件补上读取权限，
# 不收回任何已有权限，也不改动部署目录、.env、.env.panel、data/ 中的其他文件。
# 这些文件因此对本机其他用户可读：密码、密钥类设置请写在 .env.panel。
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p config data
[ -f config/config.toml ] || install -m 644 config.toml.example config/config.toml
chmod a+rx config data
chmod a+r config/config.toml
if [ -f data/qqwry.dat ]; then chmod a+r data/qqwry.dat; fi
