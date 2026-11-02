# 部署

niuniupanel 是一个独立的可执行文件，页面模板、静态资源、表结构变更都已打包在内。
部署只需要：可执行文件、配置文件、MySQL 数据库、一个提供 HTTPS 的反向代理（下面以 nginx 为例）。

**推荐**：面板与数据库在同一台服务器、域名使用 Cloudflare 时，用 [deploy-docker.md](deploy-docker.md) 的一键部署脚本。本文是不用 Docker 的手动部署方式。

只想先看看效果：用 `deploy/demo` 一条命令启动演示环境（见 README“快速试用”）。
也可以用仓库根目录的 `Dockerfile` 构建镜像、以容器方式部署（配置全部用环境变量提供）。

从 SSPanel-UIM 迁移的步骤见 [migration.md](migration.md)，请先读完本文再看迁移文档。

## 1. 环境要求

| 项目 | 要求 |
| --- | --- |
| 系统 | Linux x86_64 / arm64（其他平台可自行编译） |
| 数据库 | MySQL 8.0 及以上（推荐 8.4 LTS） |
| 反向代理 | nginx、Caddy 等，负责 HTTPS |
| 编译（可选） | Go，版本见 `go.mod`；在服务器上不需要安装 Go |

## 2. 编译

在任意装有 Go 的机器上：

```sh
git clone https://github.com/SadNoo/SS-No-PHP.git
cd SS-No-PHP
make build                                   # 生成 bin/panel
GOOS=linux GOARCH=arm64 make build           # 交叉编译 arm64
```

把 `bin/panel` 复制到服务器的 `/usr/local/bin/panel`。

## 3. 数据库

```sql
CREATE DATABASE niuniupanel CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'panel'@'127.0.0.1' IDENTIFIED BY '换成随机密码';
GRANT ALL PRIVILEGES ON niuniupanel.* TO 'panel'@'127.0.0.1';
```

- 表结构不需要手动导入：服务启动时自动执行（`migrate.auto_up = true`），多台服务器同时启动时由数据库锁保证只执行一次。
  以后升级版本带来的表结构变更也会自动执行。
- **数据库连接默认强制 TLS 并校验证书**（`database.tls_mode = "verify"`）：
  - 数据库在另一台服务器或云数据库：保持 `verify`，把数据库的 CA 证书路径填到 `tls_ca`（云数据库一般提供下载）。
  - 数据库与面板在同一台服务器、通过 `127.0.0.1` 连接：可以设为 `disabled`。
- 建议设置 `migrate.backup_command`，在执行表结构变更前自动备份，例如：

  ```toml
  [migrate]
  backup_command = "mysqldump --defaults-extra-file=/etc/niuniupanel/my.cnf --single-transaction niuniupanel | gzip > /var/lib/niuniupanel/backup/before-migrate-$(date +%Y%m%d%H%M%S).sql.gz"
  ```

  `my.cnf` 中写数据库帐号密码（`[client]` 段，权限 600），不要把密码写在命令行里。命令失败时不执行变更、服务不启动。

## 4. 配置

```sh
useradd --system --home /var/lib/niuniupanel --shell /usr/sbin/nologin niuniupanel
install -d -o niuniupanel -g niuniupanel -m 750 /var/lib/niuniupanel /var/lib/niuniupanel/data
install -d -m 750 -g niuniupanel /etc/niuniupanel
cp config.example.toml /etc/niuniupanel/config.toml      # 按注释修改
```

密码与密钥放在环境变量文件中，不写进配置文件：

```sh
cat > /etc/niuniupanel/env <<EOF
NIUNIU_APP_KEY=$(openssl rand -hex 32)
NIUNIU_DATABASE_PASSWORD=数据库密码
NIUNIU_SECURITY_MU_KEY=$(openssl rand -hex 24)
EOF
chmod 600 /etc/niuniupanel/env
```

任何配置项都能用 `NIUNIU_<段名>_<键名>` 覆盖（例如 `NIUNIU_MAIL_SMTP_PASSWORD`、`NIUNIU_TELEGRAM_TOKEN`、
`NIUNIU_PAYMENT_MERCHANT_PRIVATE_KEY`）。配置文件里出现无法识别的键时程序拒绝启动，可以及早发现拼写错误。

必须修改的配置：

- `app.base_url`：站点地址，例如 `https://panel.example.com`
- `app.key`：至少 16 位随机字符串（上面已放在环境变量中）
- `security.cookie_secure = true`（默认值，必须全站 HTTPS）
- `security.mu_key`：节点后端通信密钥
- `[mail]`：注册验证、找回密码、各类通知都需要邮件

## 5. 启动

```sh
cp deploy/systemd/niuniupanel.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now niuniupanel
journalctl -u niuniupanel -f                 # 查看日志
curl http://127.0.0.1:8080/healthz           # 返回 ok
```

创建管理员（全新安装时）：

```sh
sudo -u niuniupanel env $(cat /etc/niuniupanel/env | xargs) \
  panel -config /etc/niuniupanel/config.toml xcat createAdmin admin@example.com '一个强密码'
```

## 6. 反向代理（HTTPS）

```sh
cp deploy/nginx/niuniupanel.conf /etc/nginx/conf.d/   # 修改域名与证书路径
nginx -t && systemctl reload nginx
```

- 面板只采信 `http.trusted_proxies`（默认 `127.0.0.1`）传来的 `X-Forwarded-For`。nginx 与面板不在同一台机器，
  或前面还有 CDN 时，把这些代理的地址加入 `trusted_proxies`，否则登录记录、限流使用的 IP 都会是代理的 IP。
- 面板默认只监听 `127.0.0.1:8080`，不要直接暴露到公网。

## 7. 可选功能

| 功能 | 配置 | 说明 |
| --- | --- | --- |
| IP 归属地 | `display.qqwry_path` | 把纯真 IP 库 `qqwry.dat` 放到 `/var/lib/niuniupanel/data/qqwry.dat`（可从 GitHub 上的社区镜像下载）；没有该文件时只是不显示归属地 |
| 定时任务 | `[jobs]` | 程序内置调度，**不需要 crontab**。多台服务器部署时同一任务同一时刻只有一台执行；可以只在一台上 `jobs.enable = true` |
| Telegram | `[telegram]` | 设置 `token`、`bot`、`chat_id` 和随机的 `request_token` 后执行 `panel xcat setTelegram` 设置 webhook |
| 支付宝当面付 | `[payment]` | 开放平台“异步通知地址”填 `https://域名/payment/notify`，私钥用环境变量提供 |
| Radius | `[radius]`、`[radius.db]` | FreeRADIUS 使用 SQL 模块，面板写入 radcheck / radusergroup / nas，读取 radpostauth / radacct。NAS 变化后需要重启 radiusd（原版 `xcat syncnas` 的工作），可用 systemd path 单元或手动执行 |
| Cloudflare | `[dns]` | 推荐使用只授予该域名 DNS 编辑权限的 API Token（`cloudflare_email` 留空） |
| 验证码 | `[captcha]` | reCAPTCHA 或极验，启用后页面的内容安全策略会自动放行对应域名 |

## 8. 节点后端

节点使用 WebAPI 模式对接：

- API 地址：`app.base_url`（例如 `https://panel.example.com`）
- 密钥：`security.mu_key`（或 `mu_key_list` 中的任意一个）
- 节点 ID：后台“节点列表”中的 ID

原版的 `/mod_mu/nodes/config` 接口会把面板数据库的帐号密码发给节点，新版已停用（已知问题 K36），
因此**不支持节点直连数据库的模式**，请改为 WebAPI。

## 9. 升级

```sh
systemctl stop niuniupanel
cp panel-新版本 /usr/local/bin/panel
systemctl start niuniupanel          # 自动执行新版本的表结构变更（先运行 backup_command）
```

需要先确认变更内容时：设置 `migrate.auto_up = false`，用 `panel migrate status` 查看、`panel migrate up` 执行；
有未执行的变更时服务拒绝启动。`panel migrate down` 回退最近一次变更。

## 10. 备份

需要备份的只有数据库（以及 `/etc/niuniupanel`）。推荐数据库服务商的自动备份，或每天 `mysqldump --single-transaction`。
原版“把数据库和配置打包发邮件”的备份方式已取消（已知问题 K41）。
