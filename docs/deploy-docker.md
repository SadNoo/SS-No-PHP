# 一键部署（Docker + Cloudflare）

适合：面板和数据库在同一台 Debian 12 / 13 服务器上，域名托管在 Cloudflare 并开启代理（橙色云朵），使用 Cloudflare 源服务器证书。
所有步骤都由 `deploy/prod/install.sh` 完成，交给 AI 助手执行也只需要这一条命令。

不用 Docker、自己配置 systemd 与 nginx 的方式见 [deploy.md](deploy.md)。

## 1. 服务器建议

| 规模 | 配置 | 说明 |
| --- | --- | --- |
| 几百到约 3000 用户 | 2 核 CPU、2 GB 内存、40 GB SSD | 面板与 MySQL 同机足够；建议加 1～2 GB swap |
| 上万用户或节点很多 | 4 核 CPU、4～8 GB 内存 | 在 `deploy/prod/.env` 中把 `DB_BUFFER_POOL` 调到 `1G`～`2G` |

- 系统：Debian 12 / 13，x86_64 或 arm64 均可
- 带宽：面板本身流量很小（页面和节点接口），有 Cloudflare 在前面时 5 Mbps 以上即可
- **面板不要和节点装在同一台服务器上**：节点 IP 被封锁或被攻击时面板不受影响，面板的真实 IP 也不会因节点暴露
- 机房选与节点之间网络稳定的地区即可，用户访问走 Cloudflare

## 2. Cloudflare 设置

1. **DNS**：把每个面板域名的 A 记录指向服务器 IP，开启代理（橙色云朵）
2. **SSL/TLS → 概述**：加密模式选 **完全（严格）/ Full (strict)**
3. **SSL/TLS → 源服务器 → 创建证书**：每个根域名（Cloudflare 中的每个站点）创建一张，主机名填 `example.com, *.example.com`，
   密钥格式选 PEM。把“源证书”保存为 `deploy/prod/certs/example.com.crt`，“私钥”保存为 `deploy/prod/certs/example.com.key`
   （文件名随意，`.crt` 与 `.key` 同名即可）。私钥只显示一次，请同时自行保存
4. **节点访问面板**：节点后端的请求不是浏览器，可能被 Cloudflare 的“自动程序攻击模式 / Bot Fight Mode”或 WAF 拦截。二选一：
   - 在 **安全性 → WAF → 自定义规则** 中添加规则：URI 路径以 `/mod_mu` 开头时“跳过”（全部托管规则与超级自动程序攻击模式）；或
   - 另外准备一个**不开代理**（灰色云朵）的域名给节点使用，并把它也加入 `--domains`（需要同样有源服务器证书；
     此时该域名会暴露服务器 IP）

面板只采信来自 Cloudflare IP 段的 `CF-Connecting-IP`，所以无论节点经由 Cloudflare 还是直连，面板看到的都是节点真实 IP，
来源 IP 校验和动态 IP 宽限期都正常工作；其他来源伪造这个头不会生效。

## 3. 安装

以 root 登录服务器：

```sh
apt-get update && apt-get install -y git
git clone https://github.com/SadNoo/SS-No-PHP.git /opt/niuniupanel
cd /opt/niuniupanel/deploy/prod
# 把第 2 步的证书放进 certs/ 目录，然后：
./install.sh --domains "panel.example.com panel.example.net" --name "我的面板" --admin-email admin@example.com
```

- `--domains`：第一个是主域名（邮件、订阅链接使用），其余域名同样可以访问面板
- `--admin-email`：会提示输入管理员密码；从旧面板迁移时不要加（老管理员帐号会随数据导入），改用 `--skip-admin`
- `--no-auto-backup`：关闭每日自动备份，需要时手动运行 `./backup.sh`；选择记在 `.env`，之后重新运行 `install.sh` 沿用
- `--auto-backup`：开启每日自动备份（首次安装默认开启）

脚本会：安装 Docker（未安装时）、生成随机的数据库密码与站点密钥、检查证书是否覆盖每个域名、生成 Caddy 配置、
构建并启动面板、（默认）设置每天台北时间（UTC+8）04:30 自动备份数据库（与服务器时区无关）。可以重复运行：已生成的密钥不会改变；增减域名时带上新的 `--domains` 重新运行即可。

防火墙只需开放 22（SSH）、80、443。

## 4. 修改配置

面板配置在 `deploy/prod/.env.panel`（环境变量形式，键名与 `config.example.toml` 对应：`NIUNIU_<段名>_<键名>`），
文件里已列出常用项（邮件、购买说明、旧版密码盐等）。修改后执行：

```sh
cd /opt/niuniupanel/deploy/prod && docker compose up -d
```

备用订阅地址（用户中心订阅区出现「订阅线路」下拉框）：`NIUNIU_APP_SUB_URL_BACKUP=https://sub2.example.com/link/,https://sub3.example.net/link/`，
逗号分隔，每个都以 `/link/` 结尾；这些域名要指向本服务器并加入 `install.sh` 的 `--domains`。

订阅完整配置中强制走「🍃 Proxy」的域名后缀（默认为空，例如填站点自己的域名）：`NIUNIU_APP_SUB_PROXY_DOMAINS=example.com,example.net`，逗号分隔；
设为空值则不输出这条规则。

节点密钥是其中的 `NIUNIU_SECURITY_MU_KEY`。纯真 IP 库放到 `deploy/prod/data/qqwry.dat` 即可显示 IP 归属地：
放入后运行一次 `./ensure-dirs.sh`（确保容器内的普通用户可以读取），再执行 `docker compose restart panel`。

无法写成环境变量的设置（目前是用户中心首页的「群组」入口 `[[contact.group_links]]`）写在
`deploy/prod/config/config.toml`，键名与仓库根目录的 `config.example.toml` 相同。该文件由 `install.sh` / `upgrade.sh`
从 `config.toml.example` 复制生成（已有的不覆盖），里面有注释掉的示例，去掉行首的 `#` 改成自己的链接即可。修改后执行：

```sh
cd /opt/niuniupanel/deploy/prod && docker compose restart panel
```

- 同一项在 `.env.panel` 与 `config.toml` 中都有时，以 `.env.panel` 为准；
- 文件不存在时面板只使用 `.env.panel`，照常启动；文件写错（例如链接不是 https / http / tg / mailto）时面板拒绝启动，
  `docker compose logs panel` 会写明第几项有误；
- `config/` 与 `.env.panel` 一样不进 Git、不在数据库备份中，请自行保存一份；
- 面板容器以普通用户运行，`config/`、`data/` 需可进入，`config.toml`、`qqwry.dat` 需可读（`install.sh` / `upgrade.sh` 通过 `ensure-dirs.sh` 自动处理，
  放入新文件或手动改过权限后可以单独运行 `./ensure-dirs.sh`）。因此该文件对本机其他用户可读，**密码、密钥类设置请写在 `.env.panel`**。

## 5. 日常运维

```sh
cd /opt/niuniupanel/deploy/prod
docker compose ps                    # 查看状态
docker compose logs -f panel         # 查看面板日志
./backup.sh                          # 立即备份数据库到 backups/（保留最近 14 份，文件名是台北时间）
./restore.sh backups/xxx.sql.gz      # 从备份恢复（会先再备份一次当前数据）
./upgrade.sh                         # 升级到最新版本（先自动备份，表结构变更自动执行，清理旧镜像与构建缓存）
docker compose exec panel panel xcat # 运维命令，如 createAdmin、resetTraffic
```

### 数据库备份

- **自动备份**：每天台北时间 04:30（`backup-timer.sh` 设置的系统定时任务，服务器关机错过时开机后补做）。
  查看下次时间：`systemctl list-timers niuniupanel-backup.timer`；查看执行结果：`journalctl -u niuniupanel-backup -n 20`。
  旧版本安装的定时任务按服务器时区 04:30（AWS 等默认 UTC，即台北时间 12:30）：从旧版本升级后，单独运行一次 `./backup-timer.sh update` 改为台北时间（旧版 `upgrade.sh` 不会执行这一步；之后的升级会自动处理）。
  `update` 只更新时间，**不改变启用 / 运行状态**：已关闭的自动备份保持关闭。升级（`upgrade.sh`）也一样，不会重新打开用户关闭的自动备份。
  开启：`./backup-timer.sh install`（或 `install.sh --auto-backup`）；关闭：`systemctl disable --now niuniupanel-backup.timer`（或 `install.sh --no-auto-backup`）。
- **位置**：`/opt/niuniupanel/deploy/prod/backups/niuniupanel-年月日-时分秒.sql.gz`（gzip 压缩的 SQL，只有 root 可读）。
  保留最近 14 份：手动备份、升级（`upgrade.sh`）、恢复（`restore.sh`）前的自动备份也算在内。
- **只备份数据库**。`.env.panel`、`config/`、`certs/`、`data/` 不在其中，请自行另存（这些文件含秘密，保存在只有自己能打开的地方）。
- **手动备份**：在 1Panel「终端」或 SSH 中执行 `cd /opt/niuniupanel/deploy/prod && ./backup.sh`，最后一行显示文件名、大小与磁盘剩余空间。
- **下载到本地**（移动硬盘、自己的云端）：
  - 1Panel：左侧「文件」→ 进入 `/opt/niuniupanel/deploy/prod/backups` → 勾选要下载的 `.sql.gz` 文件 → 「下载」（或在文件右侧的「更多 / …」中选择下载）；
  - 电脑命令行：`scp root@服务器IP:/opt/niuniupanel/deploy/prod/backups/niuniupanel-*.sql.gz 本地目录/`（AWS Lightsail 用 `-i 密钥文件`、用户 `admin` 时先用 sudo 复制出来，或直接用 1Panel 下载）；
  - 下载后可在电脑上用 7-Zip 等工具解压查看，里面是普通的 SQL 文本；恢复时把 `.sql.gz` 放回 `backups/` 后执行 `./restore.sh backups/文件名.sql.gz`。
  - 备份包含全部用户资料（邮箱、密码哈希、订阅码等），云端请使用自己的私人空间，不要分享链接。
- **空间不足时不备份**：剩余空间少于上一份备份的两倍（至少 200 MB）时 `backup.sh` 直接失败并提示，不会把磁盘写满；磁盘使用超过 85% 时会提示清理。

### 磁盘空间

面板本身占用很小，会持续增长的只有下面几项，都已有上限或会自动清理：

| 项 | 处理 |
| --- | --- |
| 数据库中的流量、在线、节点状态等记录 | 面板每日任务只保留 3 天（与原版相同） |
| MySQL 二进制日志 | 只保留 3 天（`--binlog-expire-logs-seconds`，原默认 30 天） |
| 容器日志（`docker compose logs`） | 每个容器最多 3 个 10 MB 文件 |
| 数据库备份 | 最近 14 份 |
| 升级留下的旧镜像与构建缓存 | `upgrade.sh` 升级成功后自动清理旧镜像与 7 天前的构建缓存 |

查看占用：`df -h /`（磁盘）、`du -sh /opt/niuniupanel/deploy/prod/backups`（备份）、`docker system df`（镜像、容器、数据卷、构建缓存）。
需要立刻腾出空间时可以执行 `docker builder prune -af`（只删除构建缓存，下次升级多花几分钟重新编译，不影响运行中的面板与数据）。

### 构建上下文

面板镜像在服务器本机从源码构建。`.dockerignore` 采用白名单，构建时只读取 `go.mod`、`go.sum`、`cmd/`、`db/`、`internal/`、`web/`，
`.env`、`.env.panel`、`certs/`、`config/`、`data/`、`backups/` 等部署文件不会进入构建过程（CI 用 `deploy/check-build-context.sh` 检查）。
早于此版本构建时这些文件曾进入本机的构建缓存（最终镜像中没有）：升级后执行一次 `docker builder prune -af` 即可清除；不要导出或推送旧的构建缓存。

升级后有问题需要回退：`git -C /opt/niuniupanel checkout <上一个版本>`，`./restore.sh` 恢复升级前自动生成的备份，再 `docker compose up -d --build`。

## 6. 从 SSPanel-UIM 迁移

步骤与注意事项见 [migration.md](migration.md)。在这种部署方式下导入旧数据：

```sh
# 1. 旧服务器上导出（停止旧面板写入后）
mysqldump --single-transaction --default-character-set=utf8mb4 -u用户 -p 旧库名 > old.sql
# 2. 复制 old.sql 到新服务器，导入到临时库 legacy
cd /opt/niuniupanel/deploy/prod
docker compose exec -T db sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e "CREATE DATABASE legacy"'
docker compose exec -T db sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" --default-character-set=utf8mb4 legacy' < old.sql
# 3. 导入新面板（新库必须为空：安装时用 --skip-admin）
docker compose run --rm -e NIUNIU_IMPORT_DSN="root:$(grep ^DB_ROOT_PASSWORD .env | cut -d= -f2)@tcp(db:3306)/legacy" panel import-legacy
# 4. 确认无误后删除临时库
docker compose exec -T db sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e "DROP DATABASE legacy"'
```

导入前在 `.env.panel` 中填好原版的 `NIUNIU_SECURITY_LEGACY_SALT`、`NIUNIU_SECURITY_LEGACY_PWD_METHOD`，
节点密钥沿用原版 muKey 时改 `NIUNIU_SECURITY_MU_KEY`，然后 `docker compose up -d`。
