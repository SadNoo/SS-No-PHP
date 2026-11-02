# 开发进度

参照版本：SSPanel-UIM 2020-01 最终版（提交 `1a48707`，约 200 个路由、103 个模板、2.1 万行 PHP）。

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| M0 | 项目骨架：配置、数据库 TLS 连接、表结构变更、日志、中间件、错误页、静态资源、CI | ✅ 完成 |
| M1 | 表结构（utf8mb4 / UTC）与 sqlc 数据访问层 | ✅ 完成 |
| M2 | 鉴权：登录、注册、找回密码、会话、两步验证、邮件 | ✅ 完成 |
| M3 | 用户中心页面与业务 | ✅ 完成（在线支付页面随 M8） |
| M4 | 订阅生成 | ✅ 完成（提前于 M3，因为用户中心首页依赖订阅链接） |
| M5 | 节点 API（/mu、/mod_mu） | ✅ 完成 |
| M6 | 管理后台 | ✅ 完成（支付配置页随 M8） |
| M7 | 定时任务与运维命令（取代 xcat） | ✅ 完成 |
| M8 | 支付、Telegram 等外部集成；部署与迁移文档 | ✅ 完成（其他支付渠道待确认） |

## 订阅定制（2026-10）

- 订阅只包含 SS2022 / VMess / VLESS REALITY / AnyTLS；SSR、SSD、传统 SS 与不带参数的默认链接停用（返回 410）。格式与筛选见 [subscription-formats.md](subscription-formats.md)
- WebAPI v2（[node-api-v2.md](node-api-v2.md)）：四种协议的节点配置与用户凭据、原始流量增量、批次去重；认证绑定路径中的节点 ID
- 到期显示：付费用户显示等级到期时间，普通用户显示注册时间；新帐号有效期 100 年，`xcat extendAccounts` 延长已有帐号，`xcat checkClassExpire` 列出异常数据
- 节点 IP：第一个不带 # 的项自动维护（含新协议），其余手动添加的出口 IP 与旧面板 `IP#源节点ID` 专用项不会被覆盖；`xcat checkNodeIP` 核对中转规则
- 外部 NAT 中转（第一阶段，不改表结构）：节点地址 `原节点配置;||中转地址;中转端口`，后台有单独的输入框；VLESS 的 SNI / 公钥 / short ID 输入框
- 审核中发现并修复的问题见 [known-issues.md](known-issues.md) 的 S01–S06

## 用户中心精简（2026-10）

- 首页「客户端订阅」恢复旧正式版样式（sing-box 版本 / 模式按钮、灰底图标按钮），「重置订阅链接」移到资料编辑；「快速添加节点」换回「按协议订阅」（SS2022、VMess 的 `sub=3` 链接与全部 URL、VLESS REALITY、AnyTLS）
- 「客户端下载」按 iPhone / Mac、Android、Windows / 电脑列出客户端订阅教程中的官方下载地址
- 菜单去掉中转规则、延迟检测；「使用教程」改为客户端订阅教程；「工单系统」改为「联系我们」（显示 `[contact]` 中的 `admin_contact1`–`3`），用户端不能发起工单
- 资料编辑去掉选择客户端、加密方式、协议与混淆设置，`/user/method`、`/user/ssr` 返回“不支持修改”；旧链接 `/user/relay`、`/user/lookingglass`、`/user/tutorial`、`/user/ticket` 跳转到替代页面，提交返回失败，数据库原有数据不改动
- 重置节点连接密码会同时更换 VMess / VLESS 的 UUID、SS2022 用户密钥与 AnyTLS 密码（都由连接密码派生）

## M8 交付内容（外部集成、部署与迁移）

- 通用支付流程（`payment.go`）：下单写入 paylist、回调入账、充值记录、邀请返利、捐赠通知，对应原版 `AbstractPayment`
- 支付宝当面付（`internal/payment/alipay`）：直接调用官方接口 `alipay.trade.precreate`，RSA2 签名与验签（请求、返回、异步通知），
  密钥支持 PEM 与一行 Base64、PKCS#1 与 PKCS#8；不再依赖 Omnipay
- 充值页面的支付组件改为事件绑定，修复重复下单与支付成功后仍持续轮询
- 使用充值码充值余额时按原版发送捐赠 Telegram 通知
- Telegram 机器人（`internal/telegram`、`telegram.go`）：webhook `/telegram_callback`，命令 /ping /traffic /checkin /prpr /rss /help，
  订阅按钮，发送二维码图片绑定帐号（纯 Go 的 zxing 识别，也可直接发送二维码文字），6 位数字 / 二维码确认网页登录，
  入群欢迎，群组安静模式；`panel xcat setTelegram`、`sendDailyUsageByTG`
- 原版普通聊天转给图灵机器人，其旧接口已停用，不再移植
- Radius（`internal/radius`、`radius.go`）：注册、修改连接密码、后台改邮箱、删除帐号时同步 FreeRADIUS 帐号；
  VPN 节点增删时同步 NAS；每分钟维护 radius_ban（流量用完 / 禁用 / 过期时删除帐号，恢复后重建）；
  启用时内置调度每分钟执行 `synclogin`、`syncvpn`；`panel xcat syncusers`、`syncnasnode`。Radius 库连接参数与面板库相同（含 TLS），
  库不可用时面板照常运行
- Cloudflare DNS（`internal/cloudflare`）：添加节点时把节点域名的 A 记录指向节点 IP；支持原版的 Global API Key 与推荐的 API Token
- 原版被墙检测中切换 CloudXNS 解析的功能：CloudXNS 已停止服务，不再移植
- 数据迁移 `panel import-legacy`（`internal/legacy`）：从旧库逐表复制，北京时间换算为 UTC（保留哨兵值）、NULL 与无效日期转换、
  保留全部 ID；只能导入空库；`-skip-logs` 跳过日志表。用原版建表脚本建立旧库验证（`TestImportLegacy`：导入后老用户可登录）
- 文档：[deploy.md](deploy.md)（部署、systemd、nginx、升级、备份）、[migration.md](migration.md)（导入步骤、配置对照表）；
  `config.example.toml` 写出全部配置项，`TestExampleConfig` 保证与代码同步；移除原版已失效的配置项
- 修复：K45、K46、K47、K48、K49、K50
- 测试：`internal/payment/alipay`（签名、密钥格式、伪造返回 / 通知）、`TestAlipayF2F`（模拟网关下单、伪造签名、金额不符、
  5 个重复通知并发只入账一次、订单归属）；Chromium 中验证下单与二维码显示；
  `TestTelegramBot`（模拟 Bot API：伪造令牌、二维码图片绑定、一次性二维码、各命令、订阅按钮、数字登录确认、安静模式）；
  `TestRadiusSync`（FreeRADIUS 表结构：帐号同步、封禁与恢复、改名、认证与计费流量不重复、NAS、删除帐号）
- 原版其他渠道（码支付、TrimePay、PAYJS、ChenPay、PaymentWall、SPay、DoiAM、BitPay）待确认实际使用后再移植

## M0 交付内容

- `panel serve / migrate status|up|down / version` 命令
- 数据库强制 TLS，已在 MySQL 8.4.11 上验证：证书无 SAN、主机名不符时均拒绝连接
- 中间件：真实 IP（K10）、访问日志、panic 恢复（K06）、安全响应头 + CSP nonce、跨站请求防护（K05）、会话、限流器
- 错误页 404 / 405 / 500 与原版一致
- 静态资源按原路径提供；jQuery 升级为 3.7.1（K07）；国旗图片改为十六进制文件名
- 单元测试：配置、真实 IP、限流、页面渲染与转义
- golangci-lint（含 gosec）0 问题；GitHub Actions CI（lint、测试、govulncheck、编译）

## M1 交付内容

- `00002_schema.sql`：原版全部 30 张业务表 + config 表，统一 utf8mb4、补齐索引、计数字段改 BIGINT，
  与原版的差异写在文件头注释中（数据迁移按此转换）
- sqlc 配置与首批查询（用户、节点）；DECIMAL 字段映射为两位小数定点数 `internal/dec`
- `internal/util`：原版 Tools 中的通用函数（流量单位显示、随机串、时间格式等），输出格式与原版一致，随机数改用 crypto/rand（K13）
- `internal/testdb`：集成测试自动创建临时数据库并执行全部变更
- 集成测试：变更 up/down/up、流量并发原子累加（K01）、邮箱唯一、节点 IP 精确匹配（K04）

## M2 交付内容

- 页面：首页、登录、注册、找回密码、重置密码、服务条款、Telegram 登录结果页（与原版一致，已在 Chromium 中验证无 JS 报错、无 CSP 拦截）
- 鉴权：服务端会话（K12）、argon2id + 原版哈希兼容（K08）、两步验证（与原版 GA 兼容，RFC 6238 测试向量）
- 验证码：reCAPTCHA、极验 v3（K16）；Telegram 登录（Login Widget 签名校验、扫码/数字登录会话）
- 邮件：none / smtp / mailgun / sendgrid，原版 10 个邮件模板全部移植
- 业务配置：按原版 .config.php 分组定义全部业务配置项
- 模板工具：PHP 宽松比较语义的模板函数（`peq`、`truthy` 等）、原版配置键映射（`cfg`）、公共脚本 `niuniu.js`
- 修复：K11、K16～K21；第三方 CDN 资源改为本站提供（K14）

## M3 交付内容

第 1 批：首页、签到、节点列表 / 节点详情 / 节点图表、使用教程、公告、捐赠、测速、流量记录、sys、账户禁用页、
重置订阅链接、返回管理员身份。

- 原版的 Smarty 模板函数、跨块变量在 Go 模板中无法照搬，节点列表页改为后端算好每个节点的显示数据，模板只负责输出
- 节点图表：原版片段内联脚本在严格 CSP 下不会执行，改为返回 JSON 数据，由固定脚本 `node-charts.js` 绘制同样的 5 个图表
- 使用教程：原版在浏览器中用 CDN 上的 marked.js 渲染 markdown，改为服务端渲染（goldmark）
- 分页：`Pager` 输出与原版 illuminate/pagination 5.2 相同的分页 HTML
- 修复：K23（会修改数据的 GET 改为 POST）、K24（签到并发）、K25（首页倒计时脚本报错导致摇一摇签到失效）、K29
- 集成测试 `TestUserCenterPages`、`TestCheckinOncePerDay`、`TestStateChangingRoutesRequirePost`；Chromium 中验证无 JS 报错、无 CSP 拦截
- 尚未移植的页面模板暂放在 `web/pending/`（不打包），移植时复核后移回

第 2 批：账户信息（在线 IP、登录 IP 与归属地、返利记录）、资料编辑页及全部设置项（登录密码、连接密码、加密方式、
协议与混淆、联络方式、每日邮件、匿名、主题、PAC、两步验证、更换/指定端口、解封 IP、Telegram/Discord 解绑）、删除账户。

- IP 归属地：`internal/ipgeo` 读取纯真 IP 库 qqwry.dat（配置 `display.qqwry_path`），文件不存在时不显示归属地，与原版一致
- 修改登录密码后当前登录保持有效，其他设备上的登录失效；删除账户在一个事务中删除全部关联数据
- 修复：K30（端口更换在关闭时仍可调用、负价格加钱、并发透支）、K31（设置项不校验取值）
- Radius 同步（修改连接密码、删除账户时）随 Radius 模块在 M8 移植

第 3 批：商店、优惠码、购买套餐、购买记录与关闭自动续费、充值页与充值码、邀请页、购买邀请次数、定制邀请链接、重置邀请链接。

- `model.Shop`：按原顺序解析商品内容，套餐生效逻辑（`Apply`）供购买与之后的自动续费共用
- 扣款按原版 bcsub 的截断规则（相当于按分向上取整），余额检查与扣款在事务中完成
- 在线支付渠道随支付模块在 M8 移植，目前充值页只显示充值码
- 修复：K26、K28、K32、K33；`href="javascript:void(0)"` 占位链接统一阻止跳转（避免 CSP 报错）

第 4 批：工单（列表、新建、查看、回复）、中转规则（列表、链路表、添加、编辑、删除）、审计规则与审计记录。

- 工单编辑器 editor.md 1.5.0 改为本站提供（含 CodeMirror 等依赖）；工单内容保存与显示时都经过 HTML 过滤（K34）
- 新工单与回复按原版通知管理员（邮件 / Server 酱），在后台发送，不影响用户提交
- 修复：K27、K34、K35

M3 完成情况：原版用户中心的 60 多个路由中，除在线支付（支付渠道、支付回调，随 M8）外全部移植；
21 个页面在 Chromium 中逐一打开并操作主要功能，无 JS 报错、无 CSP 拦截。

## M7 交付内容（定时任务与运维命令）

- 内置调度器（`panel serve` 启动后自动运行，不再需要 crontab）：
  - 每分钟：checkjob（在线设备数限制、自动续费、节点掉线通知、异地登录提醒、账户过期、流量不足提醒、过期删除、
    长期未签到 / 未使用清理、等级到期）、syncnode（解析节点域名）
  - 每天：dailyjob（日期变化后执行：节点流量按日重置、清理 3 天前日志、套餐周期重置流量、每日结算、按日重置流量）、
    每日流量报告邮件（`jobs.daily_mail_time`，默认 22:30）
  - 可选：财务日报 / 周报 / 月报（`jobs.finance_mail`）、节点被墙检测（`jobs.detect_gfw`）
- 每个任务执行时持有 MySQL 命名锁，多实例部署也不会重复执行；首次部署时不会在白天执行每日结算
- 原版 storage/ 下的状态文件改存数据库表 `job_state`（新增表结构变更 `00003_job_state.sql`，启动时自动执行）
- `panel xcat <命令>`：与原版 php xcat 同名的运维命令（createAdmin、resetTraffic、resetPort、resetAllPort、cleanRelayRule、
  userga、reall）及手动执行任意定时任务
- 原版依赖的外部服务已停止的部分未移植：纯真 IP 库镜像自动更新、CloudXNS 自动切换解析、PushBear、SSR 客户端下载目录更新
- 修复：K03、K09、K41～K44；集成测试 `TestCheckJobUsers`、`TestCheckJobRenew`、`TestDailyJob`、`TestCheckJobConnector`、
  `TestDetectOffline`、`TestFinanceAndSchedule`

## M6 交付内容（管理后台）

- 原版后台 10 个控制器、约 90 个路由、37 个页面：系统概览、公告、工单、命令下发、节点、流量记录、封禁 / 解封 IP、用户、
  中转规则与链路查询、邀请与返利、登录记录、在线 IP、审计规则与记录、充值码与财务记录、商品、订单、优惠码
- 通用 DataTables 服务端组件（`admin_table.go`）：SQL 由程序写定，排序只能使用列表中定义的列（K02），输出默认转义（K38）
- DataTables 1.10.19、material-design-lite 改为本站提供；表格脚本改为由后端输出列定义（JSON），符合严格 CSP
- 节点、审计规则、公告变更时按原版发送 Telegram 群通知；原版的 PushBear 服务已停止，选项保留但不再推送
- 修复：K02、K38、K39、K40
- Radius NAS、Cloudflare DNS 同步与 ChenPay 支付配置随 M8 移植
- 集成测试 `TestAdminPages`（全部页面与列表接口）、`TestAdminActions`（增删改与切换用户）、`TestAdminRequiresAdmin`；
  Chromium 中逐页验证无 JS 报错、无 CSP 拦截

## M5 交付内容（节点 API）

- `/mod_mu` 全部 17 个接口与旧版 `/mu` 4 个接口：拉取用户（记录心跳）、上报流量 / 在线 IP / 审计记录 / 负载 / 测速 / 封禁 IP、
  读取节点配置、中转规则、审计规则、封禁与解封列表、命令下发与回执
- 输出按原版 json_encode 规则生成（字段顺序、数字类型、`\/` 转义与原版一致），节点后端程序无需修改；
  请求同时支持 JSON 与 PHP 风格表单（`data[0][user_id]=…`）
- 修复：K01（整批上报在一个事务中）、K04（节点 IP 精确匹配）、K36（停用泄露数据库密码的接口、空密钥）、K37（节点不能下发命令）

## M4 交付内容（订阅生成）

- `internal/sub`：对应原版 `URL.php`、`Tools::v2Array/ssv2Array`、`LinkController`，支持 ssr://、ss://（SIP002 与旧格式）、vmess://、ssd://、Clash
- 中转规则选择、单端口多用户、合并订阅（mergeSub）、管理员节点范围等逻辑与原版一致
- **对拍验证**：在 PHP 8.4 下直接运行原版 `URL.php`（数据库查询用内存替身），对同一份测试数据生成 48 组完整订阅内容，
  Go 版逐字节一致（`internal/sub/testdata/golden_full.json`、`TestGoldenFullSubscription`）；
  PHP 的 json_encode、number_format、rawurlencode、浮点数输出规则均已对齐
- Clash 配置模板来自原版 `clash.tpl`；仅包含模板逻辑的行不再输出空行（YAML 语义不变）
- 原版在循环中反复查询数据库，改为一次读取后在内存中计算
- `/link/{token}` 订阅接口，增加限流（K22）

## 上线前补充

- 购买说明 `payment.notice`：不开放在线支付时，在充值、套餐页面显示联系方式等说明
- 节点 WebAPI 对接文档 [node-api.md](node-api.md)
- 动态 IP（DDNS）节点：来源 IP 不符时立即重新解析节点域名；DNS 未更新时的宽限期 `security.node_ip_grace`
  （默认 10 分钟，只对已停止从原 IP 上报的节点生效），`TestNodeAuthDDNS`
