# 从 SSPanel-UIM 迁移

适用于 SSPanel-UIM 2020-01 前后的版本（Anankke 分支，`config/.config.php` 配置）。
迁移不修改旧库，旧面板可以一直保留到确认新面板工作正常。

## 概要

1. 按 [deploy.md](deploy.md) 部署新面板，**先不要启动服务**
2. 把旧的 `.config.php` 改写为 `config.toml`（见下方对照表）
3. 停止旧面板写入（关闭网站、停止 crontab 与节点后端），导出一份旧库备份
4. 执行 `panel import-legacy` 导入数据
5. 启动新面板，检查后台数据、登录、订阅
6. 节点后端改为 WebAPI 连接新面板（见第 3 节）；切换域名解析或反向代理
7. 设置 Telegram webhook、支付回调地址等外部服务

## 1. 导入数据

新库必须是**全新的空库**（只执行过表结构变更也可以）。旧库的连接串用环境变量提供，避免密码出现在进程列表里：

```sh
export NIUNIU_IMPORT_DSN='旧库用户:旧库密码@tcp(旧库地址:3306)/旧库名'
# 旧库在远程时加上 TLS：...@tcp(host:3306)/sspanel?tls=true
sudo -u niuniupanel env $(cat /etc/niuniupanel/env | xargs) NIUNIU_IMPORT_DSN="$NIUNIU_IMPORT_DSN" \
  panel -config /etc/niuniupanel/config.toml import-legacy
```

参数：

| 参数 | 说明 |
| --- | --- |
| `-tz Asia/Shanghai` | 旧库时间字段使用的时区，默认取 `app.time_zone`（原版 `timeZone = 'PRC'` 即 `Asia/Shanghai`） |
| `-skip-logs` | 跳过流量记录、在线 IP、登录记录、测速记录等日志类表，数据量很大时可以加快迁移 |

导入时做的转换（详见 `db/migrations/00002_schema.sql` 文件头）：

- 时间：原版按北京时间存储，新版统一存 UTC，导入时自动换算；`1989-06-04 00:05:00`（无等级期限）、
  `2099-06-04 00:05:00`（永不过期）原样保留；`0000-00-00` 等无效日期改为默认值
- 空值：新版的列都不允许 NULL，导入时改为空字符串或 0（与原版 PHP 中的判断结果相同）
- 字符集：统一为 utf8mb4（旧库为 latin1 / utf8 时由 MySQL 自动转换）
- ID 全部保留：订阅链接、邀请链接、节点 ID、充值记录都不变
- 原版代码中未使用的字段（`last_get_gift_time` 等）不导入；旧库缺少的新字段使用默认值，导入结果中会列出

导入结束会逐表输出行数。如果中途出错（例如两个用户的邮箱只有大小写不同，新版的邮箱是唯一索引），
处理旧库数据后删除新库重新创建再导入即可。

导入前可以检查有没有乱码：在旧库中执行 `SELECT user_name, HEX(user_name) FROM user LIMIT 5;`，
中文名的十六进制应为 `E4`～`E9` 开头的三字节一组。若是 `C3A5C2...` 这样的形式，说明旧库本身已经是“双重编码”的乱码，
需要先在旧库中修复。

### 导入后：补齐已有用户的禁止访问网段

新注册用户的禁止访问 IP 默认包含回环、内网、运营商级 NAT、链路本地与云元数据地址（`reg_forbidden_ip`）。
旧库用户各自保存原来的值（通常只有 `127.0.0.0/8,::1/128`），在新库中执行下面的 SQL 补齐（phpMyAdmin / 1Panel 的 SQL 窗口均可）：

- 只追加缺少的网段，用户原有的自定义 IP 保留，不会重复；
- 可以重复执行，第二次执行不会改动任何行；
- 执行前建议先备份 `user` 表（例如 `CREATE TABLE user_bak_forbidden AS SELECT id, forbidden_ip FROM user;`，确认无误后再删除备份表）。

```sql
UPDATE user SET forbidden_ip = IF(TRIM(COALESCE(forbidden_ip, '')) = '', '127.0.0.0/8', CONCAT(TRIM(TRAILING ',' FROM forbidden_ip), ',127.0.0.0/8')) WHERE FIND_IN_SET('127.0.0.0/8', REPLACE(COALESCE(forbidden_ip, ''), ' ', '')) = 0;
UPDATE user SET forbidden_ip = IF(TRIM(COALESCE(forbidden_ip, '')) = '', '0.0.0.0/8', CONCAT(TRIM(TRAILING ',' FROM forbidden_ip), ',0.0.0.0/8')) WHERE FIND_IN_SET('0.0.0.0/8', REPLACE(COALESCE(forbidden_ip, ''), ' ', '')) = 0;
UPDATE user SET forbidden_ip = IF(TRIM(COALESCE(forbidden_ip, '')) = '', '10.0.0.0/8', CONCAT(TRIM(TRAILING ',' FROM forbidden_ip), ',10.0.0.0/8')) WHERE FIND_IN_SET('10.0.0.0/8', REPLACE(COALESCE(forbidden_ip, ''), ' ', '')) = 0;
UPDATE user SET forbidden_ip = IF(TRIM(COALESCE(forbidden_ip, '')) = '', '100.64.0.0/10', CONCAT(TRIM(TRAILING ',' FROM forbidden_ip), ',100.64.0.0/10')) WHERE FIND_IN_SET('100.64.0.0/10', REPLACE(COALESCE(forbidden_ip, ''), ' ', '')) = 0;
UPDATE user SET forbidden_ip = IF(TRIM(COALESCE(forbidden_ip, '')) = '', '169.254.0.0/16', CONCAT(TRIM(TRAILING ',' FROM forbidden_ip), ',169.254.0.0/16')) WHERE FIND_IN_SET('169.254.0.0/16', REPLACE(COALESCE(forbidden_ip, ''), ' ', '')) = 0;
UPDATE user SET forbidden_ip = IF(TRIM(COALESCE(forbidden_ip, '')) = '', '172.16.0.0/12', CONCAT(TRIM(TRAILING ',' FROM forbidden_ip), ',172.16.0.0/12')) WHERE FIND_IN_SET('172.16.0.0/12', REPLACE(COALESCE(forbidden_ip, ''), ' ', '')) = 0;
UPDATE user SET forbidden_ip = IF(TRIM(COALESCE(forbidden_ip, '')) = '', '192.168.0.0/16', CONCAT(TRIM(TRAILING ',' FROM forbidden_ip), ',192.168.0.0/16')) WHERE FIND_IN_SET('192.168.0.0/16', REPLACE(COALESCE(forbidden_ip, ''), ' ', '')) = 0;
UPDATE user SET forbidden_ip = IF(TRIM(COALESCE(forbidden_ip, '')) = '', '::1/128', CONCAT(TRIM(TRAILING ',' FROM forbidden_ip), ',::1/128')) WHERE FIND_IN_SET('::1/128', REPLACE(COALESCE(forbidden_ip, ''), ' ', '')) = 0;
UPDATE user SET forbidden_ip = IF(TRIM(COALESCE(forbidden_ip, '')) = '', 'fc00::/7', CONCAT(TRIM(TRAILING ',' FROM forbidden_ip), ',fc00::/7')) WHERE FIND_IN_SET('fc00::/7', REPLACE(COALESCE(forbidden_ip, ''), ' ', '')) = 0;
UPDATE user SET forbidden_ip = IF(TRIM(COALESCE(forbidden_ip, '')) = '', 'fe80::/10', CONCAT(TRIM(TRAILING ',' FROM forbidden_ip), ',fe80::/10')) WHERE FIND_IN_SET('fe80::/10', REPLACE(COALESCE(forbidden_ip, ''), ' ', '')) = 0;
```

执行后可以抽查：`SELECT id, forbidden_ip FROM user ORDER BY id LIMIT 5;`

### 导入后：检查封禁 IP（disconnect_ip）

`user.disconnect_ip` 是面板判定超出同时在线 IP 数、暂时禁止连接的来源 IP（逗号分隔），由每分钟任务写入并在 5 分钟后按
`disconnect_ip` 表的记录解除。旧库中可能有非法项（端口、网段、畸形文本等，来源不明）或没有对应记录、永远不会解除的残留。

- `import-legacy` 结束时会检查并报告（只报告用户 ID、项数、分类与长度，不输出 IP 原文，也不修改数据）；
- 节点接口只下发合法的单个 IPv4 / IPv6，非法项跳过并在面板日志中告警，后端不会因此拒绝用户列表；
- 修复（站长确认后执行，只改 `disconnect_ip`，不删除用户、不改变其他字段与连接凭据）：

```sh
cd /opt/niuniupanel/deploy/prod
./backup.sh                                                              # 先备份
docker compose exec -T panel panel xcat checkDisconnectIP                # 影响清单（只读）
docker compose exec -T panel panel xcat repairDisconnectIP               # 预览修复结果
# 修复：回滚文件直接写到本机 backups/（以 root 运行一次性容器，只为能写入这个目录）
docker compose run --rm --no-deps --user 0:0 -v "$PWD/backups:/rollback" panel \
  xcat repairDisconnectIP --apply --rollback-file /rollback/rollback-disconnect-ip-$(date +%Y%m%d-%H%M%S).sql
```

- `--apply` 必须指定 `--rollback-file`：先把全部回滚语句写入这个**新文件**（已存在则拒绝，权限 600），写入、刷盘、关闭都成功后
  才修改数据库；任何一步失败都不修改并报错。
- 回滚：`docker compose exec -T db sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" niuniupanel' < backups/rollback-disconnect-ip-时间.sql`。
  每次修复有一个编号，实际修改了的用户在同一个事务中记入 `disconnect_ip_repair` 表。回滚语句只恢复**本次确实修改了**、
  且字段（按字节）仍是修复后值的用户：
  - 全部跳过（检查后字段都有变化）：回滚文件中的语句都不生效，不改动任何用户；
  - 部分跳过：只恢复修改了的用户，跳过的用户不受影响（即使其他操作把它改成了与计划相同的值）；
  - 修复事务失败：没有任何修改，也没有修复记录，回滚文件不改动任何用户；
  - 修复后又有变化的用户不受影响。重复执行回滚文件不会重复改动。
- 默认只去掉非法项，没有对应记录的合法项保留；确认这些也是历史残留时再加 `--orphans`。
- 检查在同一个数据库快照中读取两张表；修复时逐个锁住用户行，按字节确认字段仍是检查时的原文，并重新读取该用户当前的
  `disconnect_ip` 表记录后重新计算，结果不同（期间新增了封禁或记录）就跳过，再运行一次即可。

## 2. 配置对照

新版配置是 TOML 文件，按段组织。下表列出名称或位置有变化的配置项；**表中没有列出的原版配置项，
在 `config.example.toml` 中使用相同的名字（改为小写下划线）**，例如 `register_mode` → `[register] mode`、
`code_payback` → `[users] code_payback`，可以在 `config.example.toml` 中搜索原版键名（注释中都标出了）。

| 原版 `.config.php` | 新版 | 说明 |
| --- | --- | --- |
| `key` | `app.key` | 新版用于会话等签名，**请生成新的随机值**，无需沿用 |
| `appName` / `baseUrl` / `subUrl` | `app.name` / `app.base_url` / `app.sub_url` | `sub_url` 不变则旧订阅链接继续可用 |
| `timeZone` | `app.time_zone` | `PRC` 写作 `Asia/Shanghai` |
| `debug` | `app.debug` | |
| `db_host` / `db_database` / `db_username` / `db_password` | `[database]` | 默认强制 TLS，见 deploy.md |
| `salt` | `security.legacy_salt` | **必须与原版一致**，否则老用户无法登录 |
| `pwdMethod` | `security.legacy_pwd_method` | **必须与原版一致**；老用户首次登录后自动升级为 argon2id |
| `muKey` / `muKeyList` | `security.mu_key` / `security.mu_key_list` | 沿用原值则节点后端只需改地址；空密钥不再被接受 |
| `rememberMeDuration`（天） | `security.session_lifetime` | 写作时长，例如 `"168h"` |
| `mailDriver` | `mail.driver` | |
| `smtp_passsword` | `mail.smtp_password` | 原版拼写多一个 s |
| `inviteNum` / `defaultTraffic` / `ramdom_group` | `register.invite_num` / `register.default_traffic` / `register.random_group` | |
| `checkinMin` / `checkinMax` / `mergeSub` | `users.checkin_min` / `users.checkin_max` / `users.merge_sub` | |
| `enable_telegram` / `telegram_token` / `telegram_chatid` / `telegram_bot` / `telegram_group_quiet` / `telegram_request_token` | `[telegram]` 中的 `enable` / `token` / `chat_id` / `bot` / `group_quiet` / `request_token` | `request_token` 现在必须设置 |
| `useScFtqq` / `ScFtqq_SCKEY` | `contact.use_scftqq` / `contact.scftqq_sckey` | |
| `captcha_provider` | `captcha.provider` | |
| `payment_system` | `payment.system` | 目前支持 `none`、`f2fpay` |
| `f2fpay_app_id` / `merchant_private_key` / `alipay_public_key` / `f2fNotifyUrl` | `[payment]` 中同名键 / `f2f_notify_url` | 原版可以写密钥文件路径，新版直接写密钥内容（建议用环境变量） |
| `detect_gfw_interval` / `_port` / `_url` / `_count` | `detect.gfw_interval` / `gfw_port` / `gfw_url` / `gfw_count` | |
| `enable_detect_offline` / `enable_detect_offline_useScFtqq` | `detect.enable_detect_offline` / `detect.detect_offline_use_scftqq` | |
| `Speedtest_duration` | `display.speedtest_duration` | |
| `enable_radius` / `radius_secret` | `radius.enable` / `radius.secret` | |
| `radius_db_host` / `radius_db_database` / `radius_db_user` / `radius_db_password` | `[radius.db]` 中的 `host` / `name` / `user` / `password` | 连接参数与 `[database]` 相同 |
| `cloudflare_*` | `[dns]` 中同名键 | `cloudflare_email` 留空时 `cloudflare_key` 视为 API Token |

**不再需要或已停用的配置**（写进配置文件会报“无法识别的配置项”，删除即可）：

- 运行环境类：`db_driver`、`db_charset`、`db_collation`、`db_prefix`、`authDriver`、`sessionDriver`、`cacheDriver`、
  `tokenDriver`、`redis_*`、`aws_*`、`theme`、`version`、`config_migrate_notice` —— 新版固定使用 MySQL 存储会话
- 已停止服务的第三方：`enable_tuling` / `tuling_*`（图灵机器人）、`usePushBear` / `PushBear_sendkey`、
  `enable_cloudxns` / `cloudxns_*`、`qrcode`（二维码识别改为内置）
- 已取消的功能：`auto_backup_email`、`auto_backup_passwd`、`backup_notify`（K41）、`detect_gfw_judge`（K43）、
  `relay_insecure_mode`（原版代码中实际不生效）、`v2ray_port` / `v2ray_protocol` / `v2ray_alter_id` / `v2ray_level`、
  `pacp_offset`、`pacpp_offset`、`enable_analytics_code`、`sspanelAnalysis`、`discord_token`
- 尚未移植的支付渠道：`codepay_*`、`pmw_*`、`alipay_id` / `alipay_key`、`zfbjk_*`、`trimepay_*`、`bitpay_secret`、`payjs_*`

## 3. 节点后端：从直连数据库改为 WebAPI

新版不支持节点直连数据库（每台节点都持有能改写整个面板数据库的帐号，一台节点被入侵就等于面板被入侵；
原版用来给节点下发数据库密码的 `/mod_mu/nodes/config` 接口也已停用，见 K36）。

2020 年前后的 shadowsocks-mod（Anankke 版）本身就同时支持两种模式，**只需修改每台节点的 `userapiconfig.py`**：

```python
API_INTERFACE = 'modwebapi'                 # 原来是 'glzjinmod'
WEBAPI_URL = 'https://panel.example.com'    # 新面板的 app.base_url，不带结尾斜杠
WEBAPI_TOKEN = '与面板 security.mu_key 相同' # 沿用原版 muKey 的值即可
NODE_ID = 1                                 # 不变
# MYSQL_* 配置不再使用，可以删除
```

然后重启节点后端（`./stop.sh && ./run.sh`，或 `systemctl restart ssr`）。
自己开发或改造过的后端，请对照 [node-api.md](node-api.md) 检查。

注意：

- 面板按**请求来源 IP**识别节点，节点在后台的“节点 IP”必须是它访问面板时的出口 IP。
  节点地址是域名时，面板每分钟自动解析更新（`jobs.sync_node`）；节点在 NAT 后、出口 IP 不同时，
  在“节点 IP”中用逗号把出口 IP 追加在主 IP 之后（第一个不带 `#` 的项是主 IP，由面板自动维护，其余项不会被覆盖）。
- 使用面板中转规则（`IP#源节点ID` 格式）的：原版默认目标取最后一个不带 `#` 的项，新版取主 IP，专用项优先。
  原版结果还受默认项与专用项的排列顺序影响（例如 `198.51.100.10#8,203.0.113.20` 对源节点 8，原版为 `203.0.113.20`，新版为 `198.51.100.10`），
  不能仅凭只有一个不带 `#` 的项就判断一致。导入后运行 `panel xcat checkNodeIP`（只读）列出结果不同的中转规则，逐个核对后再保存相关节点；
  每分钟同步只会更新目标 IP 已失效的规则，不会改动仍然有效的旧规则。
- 动态 IP 节点：用 DDNS 域名作为节点地址。IP 变化后面板会立即按域名重新解析，DDNS 尚未更新时还有 10 分钟宽限期
  （`security.node_ip_grace`），详见 [node-api.md](node-api.md)。
- 面板前面有 CDN 或多层代理时，要把它们加入 `http.trusted_proxies`，否则面板看到的是代理的 IP，节点请求会被拒绝
  （返回 `token or source is invalid`）。
- 改完后在节点日志中不应再有 `Server error`，后台节点列表显示在线，用户流量开始增长。
- 切换完成后，删除原来给节点使用的数据库帐号，并关闭数据库对节点的端口开放。

已用 2020 版 shadowsocks-mod 的 WebAPI 请求逐一验证：拉取节点信息、用户、审计规则、中转规则，
上报流量、负载、在线 IP、审计记录、封禁 IP，数据都正确写入；返回格式满足其“只有 ret 和 data 两个键”的校验。

## 4. 定时任务

删除旧面板的全部 crontab（`xcat checkjob`、`dailyjob`、`syncnode`、`sendDiaryMail`、`synclogin` 等）。

自动续费默认关闭（`users.enable_auto_renew = false`，见已知问题 K52）：导入的旧订单即使记录了续费时间也不会续费扣款，
购买记录中显示“本站未开启自动续费”。以后要开启时，先在数据库中关闭旧订单的续费，避免旧订单在开启后立刻扣款：
`UPDATE bought SET renew = 0;`（只影响自动续费，不影响已购套餐的效果与流量重置）。
新版在服务进程内调度，对应关系见 `config.example.toml` 的 `[jobs]` 段；需要手动执行时用 `panel xcat <任务名>`。

## 5. 切换后检查

- 后台“用户列表”“节点列表”数量与旧库一致
- 用老用户的帐号密码登录（验证 `legacy_salt` 与 `legacy_pwd_method`）
- 用户中心复制的订阅链接与旧面板相同，客户端更新订阅正常
- 节点后端改为新地址后，后台节点列表显示在线、流量开始增长
- 设置 Telegram 后执行 `panel xcat setTelegram`；支付宝开放平台的异步通知地址改为新域名
- 启用 Radius 时，执行一次 `panel xcat syncnasnode`

## 6. 回退

迁移没有修改旧库。新面板运行期间产生的新数据（注册、流量、充值）不会回写旧库，
如需回退，越早越好：停止新面板，把节点后端和域名改回旧面板即可。
