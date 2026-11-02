# 原版已知问题修复清单

重构时按“业务原样、修复已知问题”的原则处理。下表中的每一项，新版的行为都会与原版不同，
对比测试会跳过这些差异。状态：待修复 / 已修复（附提交或测试名）。

| 编号 | 问题 | 原版位置 | 修复方式 | 状态 |
| --- | --- | --- | --- | --- |
| K01 | 节点重复上报流量会被重复计费；多个节点同时上报同一用户时先读后写，可能丢失流量 | `Mod_Mu/UserController::addTraffic` | 流量用 `u = u + ?` 原子累加并放在事务中；上报幂等方案在节点 API 阶段确定 | 已修复（M1 原子累加；M5 整批上报在一个事务中，全部成功或全部不生效，见 `TestNodeAPI`） |
| K02 | 后台用户列表排序参数直接拼进 SQL（`orderByRaw`），存在 SQL 注入 | `Admin/UserController` 440、472 行 | 排序字段与方向使用白名单 | 已修复（M6，`internal/app/admin_table.go`，见 `TestAdminPages`） |
| K03 | 定时任务中任一用户出错（`die`、未捕获异常）会导致后续用户全部不处理 | `Command/Job::CheckJob` | 每个用户单独处理并记录错误，不中断整体；任务加锁防止重叠执行 | 已修复（M7，`internal/app/jobs*.go`） |
| K04 | 节点认证用 `LIKE 'IP%'` 前缀匹配，`1.2.3.4` 可匹配 `1.2.3.45` | `Middleware/Mod_Mu` | 精确匹配节点 IP | 已修复（M5 节点认证按逗号分隔的每个地址精确匹配，见 `TestNodeIPMatches`） |
| K05 | 依赖了 `slim/csrf` 但从未启用，所有表单无 CSRF 防护 | `config/routes.php` | 全局启用 `http.CrossOriginProtection` | 已修复（M0，`internal/server/server.go`） |
| K06 | Whoops 中间件在错误页暴露代码与配置 | `config/routes.php` | 统一错误页，详情只写日志 | 已修复（M0，`Recoverer`） |
| K07 | jQuery 1.11.3 / 2.2.1 / 3.2.1 存在已知 XSS 漏洞 | 模板与静态资源 | 统一升级为 jQuery 3.7.1（附 jquery-migrate 3.5.2） | 已修复（M0，`web/static/vendor/jquery`） |
| K08 | 默认密码哈希为 md5 + salt，容易被破解 | `Utils/Hash` | 新密码使用 argon2id；旧密码登录成功后自动升级 | 已修复（M2，`TestLegacyPasswordUpgrade`；已用 PHP 8.4 实际生成的 argon2id/argon2i/bcrypt 哈希验证兼容） |
| K09 | 数据库 `NOW()` 与 PHP 时区（PRC）不一致，按数据库时间统计会错开 | `Command/FinanceMail` 等 | 数据库统一 UTC，业务时间由程序按配置时区换算 | 已修复（M7，`TestFinanceAndSchedule`） |
| K10 | 直接采信 `X-Forwarded-For` 第一个值，客户端 IP 可被伪造 | `config/.config.php` 末尾 | 只采信可信代理传来的 XFF，从右往左取第一个不可信地址 | 已修复（M0，`RealIP`，见 `TestRealIPResolve`） |
| K11 | 默认向原作者的 Google Analytics 发送访问统计（`sspanelAnalysis = true`） | `footer.tpl` | 移除第三方统计代码（页脚与 paolu.html） | 已修复（M2） |
| K12 | 登录 Cookie 由用户 ID 与密码哈希签名组成，无法单独让某个登录失效 | `Services/Auth/Cookie` | 改为服务端会话；修改密码后旧会话自动失效 | 已修复（M2，`internal/auth/session.go`） |
| K13 | 多处使用 `rand()`/`md5(time())` 生成令牌，可预测 | 订阅、邀请码、重置密码等 | 统一使用 `crypto/rand` | 已修复（M1，`internal/util`） |
| K14 | 第三方 CDN（jsDelivr、Google Fonts）加载脚本与字体，对方被篡改会连带受影响 | 模板 | 全部改为本站提供（jQuery、二维码库、Material Icons、Font Awesome、nprogress、clipboard、jquery-validation、shake.js、markdown.css 均来自 npm 并校验了 sha1；教程 markdown 改为服务端渲染） | 进行中（用户中心已完成，含工单编辑器 editor.md；支付页面随 M8） |
| K15 | 模板中大量内联事件与 `setTimeout("字符串")`，无法启用严格 CSP | 模板 | 改为 data-* 属性 + 事件委托（`niuniu.js`），内联脚本使用 nonce | 进行中（随页面移植） |
| K16 | 极验服务器不可用时进入“宕机模式”，校验算法可在客户端计算，验证码形同虚设 | `Utils/GeetestLib` | 宕机模式直接拒绝；与极验的通信改为 HTTPS | 已修复（M2，`internal/captcha`） |
| K17 | 360 浏览器用户被跳转到非官方的“Chrome 下载站” chromedl.uk.to | `public/assets/js/fuck.js` | 改为 Google 官方下载页 | 已修复（M2） |
| K18 | 密码重置链接在 24 小时内可重复使用 | `PasswordController::handleToken` | 使用一次后作废该邮箱的全部重置链接 | 已修复（M2，`TestPasswordResetSingleUse`） |
| K19 | 登录、重置密码对不存在的邮箱给出不同提示，可探测已注册邮箱 | `AuthController`、`PasswordController` | 统一提示；登录时对不存在的邮箱也做一次哈希计算使耗时一致 | 已修复（M2，`TestRegisterLoginLogout`） |
| K20 | 登录、注册、发送验证码、重置密码均无频率限制 | 路由 | 按 IP 限流 | 已修复（M2） |
| K21 | 注册页密码强度提示的 JS 有赋值/比较写错，提示从不显示 | `auth/register.tpl` | 修正判断与事件绑定 | 已修复（M2） |
| K22 | 订阅地址只靠 16 位令牌保护，接口无频率限制，可被枚举 | `/link/{token}` | 按 IP 限流 | 已修复（M4） |
| K23 | 重置订阅链接、重置两步验证密钥、解绑 Telegram/Discord、重置邀请链接、返回管理员身份都是普通 GET 链接，可被其他网站诱导触发（跨站防护只覆盖 POST） | `config/routes.php` | 改为 POST，页面中用 `postTo()` 提交 | 已修复（M3，`TestStateChangingRoutesRequirePost`） |
| K24 | 签到先判断再写入，并发请求可重复签到、重复获得流量 | `UserController::doCheckIn` | 把“今天未签到”作为 UPDATE 条件 | 已修复（M3，`TestCheckinOncePerDay`） |
| K25 | 用户中心首页倒计时脚本引用了不存在的元素而报错，后面的“摇一摇签到”从未生效 | `user/index.tpl` | 元素不存在时跳过倒计时 | 已修复（M3） |
| K26 | 购买套餐时若开启 `enable_bought_extend`，`class` 分支缺少 break，会把用户限速改成等级数值 | `Models/Shop::buy` | 补上 break | 已修复（M3，`model.Shop.Apply`） |
| K27 | 添加 V2Ray 中转规则时用 `=` 写成了赋值，任何已有规则都会被判为冲突 | `RelayController::add` | 改为比较 | 已修复（M3） |
| K28 | 生成邀请码时循环检查的是“该用户是否已有邀请码”而不是“新码是否重复”，4 位码重复时写入失败 | `Models/User::addInviteCode` | 检查新码是否已存在 | 已修复（M3） |
| K29 | 节点详情页 JSON 配置的 plugin 字段缺少空格，生成 `--obfs http--obfs-host` | `URL::getJsonObfs` | 补上空格 | 已修复（M3） |
| K30 | 更换端口、指定端口在后台关闭（价格为负数）时接口仍可调用，且负价格会给用户加钱；扣款先读后写，并发可透支 | `UserController::ResetPort/SpecifyPort` | 价格为负时拒绝；在事务中锁定用户后扣款 | 已修复（M3，`TestUserSettings`） |
| K31 | 部分设置项不校验取值：主题可填任意值导致页面打不开，匿名、两步验证开关可写入任意数字 | `UserController::updateTheme/updateHide/GaSet` | 主题只接受已有主题，开关只接受 0 / 1 | 已修复（M3，`TestUserSettings`） |
| K32 | 购买套餐：优惠码不存在时因空对象报错；购买时不检查一次性优惠码的使用次数（只在“检查优惠码”时检查，可绕过）；余额检查与扣款先读后写，并发可透支 | `UserController::buy` | 提示优惠码无效；购买时检查使用次数；事务中锁定用户后扣款 | 已修复（M3，`TestShopBuy`） |
| K33 | 充值码先查询再标记已使用，同一充值码并发提交可使用多次；除余额充值外，其他类型充值码成功后不返回结果 | `UserController::codepost` | 事务中锁定充值码；统一返回结果 | 已修复（M3，`TestRechargeCodeOnce`） |
| K34 | 工单内容按 HTML 原样显示，只靠保存时的 AntiXSS 过滤 | `user/ticket_view.tpl` | 保存与显示时都用 bluemonday 过滤 | 已修复（M3；用户端工单现已停用，后台查看工单仍经过过滤） |
| K35 | 中转规则冲突检查遇到环形规则会无限递归；链路表遇到“所有节点”规则会因空对象报错 | `Tools::has_conflict_rule`、`RelayController::index` | 记录已访问的规则；以当前节点作为起点 | 已修复（M3，`TestRelayConflictLoop`） |
| K36 | 节点接口 `/mod_mu/nodes/config` 会把面板数据库地址、账号、密码返回给任何持有节点密钥的请求；未设置 muKeyList 时默认密钥为全角空格，mu_key 为空时空字符串也能通过校验 | `Mod_Mu/NodeController::getConfig`、`Config::getMuKey` | 停用该接口（节点使用 WebAPI 模式）；空密钥一律拒绝，比较使用常数时间 | 已修复（M5，`TestNodeAPI`） |
| K37 | 节点可以通过 `/mod_mu/func/autoexec` 写入任意类型记录，即任何一台节点（或泄露的节点密钥）都能向所有节点下发要执行的命令 | `Mod_Mu/FuncController::addAutoexec` | 节点只能写入执行回执（type = 2） | 已修复（M5，`TestNodeAPI`） |
| K38 | 后台列表把查询结果原样作为 HTML 输出，用户名、联络方式、工单标题等用户填写的内容会在管理员浏览器中执行（存储型 XSS） | `Ozdemir\Datatables` 与各后台 ajax | 除程序生成的按钮列与管理员编写的公告外一律转义 | 已修复（M6，`TestAdminPages`） |
| K39 | 后台部分功能本身不可用：为用户添加套餐缺少 Shop 类引用、增加邀请次数填 0 时变量未定义、添加中转规则选“不进行中转”时报“目标节点错误” | `Admin/UserController::buy`、`AdminController::addInvite`、`Admin/RelayController::add` | 修正后可正常使用 | 已修复（M6，`TestAdminActions`） |
| K40 | “以用户身份登录”从请求参数中读取管理员 ID | `Admin/UserController::changetouser` | 使用当前登录的管理员；返回地址只接受本站路径 | 已修复（M6） |
| K41 | 自动备份把整个数据库与含全部密钥的配置文件打成 zip（传统 zip 加密，强度很低）用邮件发送；mysqldump 的密码出现在进程命令行中 | `Command/Job::backup` | 不再提供邮件备份；使用 `migrate.backup_command` 或数据库服务商的备份功能（见部署文档） | 已处理（M7） |
| K42 | 在线设备数限制判断“是否已记录”时把 IP 与记录 ID 比较，超限期间每分钟重复写入记录、重复追加 IP | `Command/Job::CheckJob` | 按用户与 IP 判断 | 已修复（M7，`TestCheckJobConnector`） |
| K43 | 节点被墙检测用 `eval` 执行配置中的判断表达式；跳过掉线节点时文件名少了一个点，掉线节点仍被检测 | `Command/Job::detectGFW` | 固定为原版默认判断（status 为 "true"）；正确跳过掉线节点 | 已修复（M7） |
| K44 | 同步节点 IP 时域名解析失败会把域名本身写入 node_ip，导致节点认证失败 | `Command/Job::syncnode`、`Node::changeNodeIp` | 解析失败时保持原 IP | 已修复（M7） |
| K45 | 支付回调先读订单状态再保存，支付网关重复通知并发到达时会重复入账、重复返利 | `AbstractPayment::postPayment` | 事务中用条件更新标记订单，只有一次通知生效 | 已修复（M8，`TestAlipayF2F`） |
| K46 | 订单状态查询 `/payment/status` 不需要登录，可按订单号查询任意订单 | `config/routes.php`、`AopF2F::getStatus` | 需要登录，只能查询自己的订单 | 已修复（M8，`TestAlipayF2F`） |
| K47 | 当面付下单只检查金额非空，负数、非数字都会写入订单；支付宝返回错误时仍返回成功和空二维码；回调未核对通知金额与订单金额 | `AopF2F::purchase`、`AopF2F::notify` | 校验金额（大于 0、最多两位小数、单笔上限 10 万）；下单失败返回错误；金额不符不入账 | 已修复（M8，`TestAlipayF2F`） |
| K48 | Telegram webhook 只比较 URL 中的令牌，未设置 telegram_request_token 时两边都为空即通过，任何人都能伪造更新冒充任意 Telegram 用户（签到、取订阅、确认网页登录）；同一个 Telegram 帐号可以绑定多个用户 | `HomeController::telegram`、`TelegramProcess` | 令牌必须非空、常数时间比较，并支持 Telegram 的 secret_token 头；绑定前解除该 Telegram 帐号的旧绑定 | 已修复（M8，`TestTelegramBot`） |
| K49 | Radius：`syncusers` 在邮件中明文发送连接密码；`synclogin` 把认证失败也计为流量；`syncvpn` 遇到面板中不存在的用户名出错中断；两者每分钟只看“最近 60 秒”，任务延迟时会漏记或重复记录 | `Command/SyncRadius` | 邮件只告知用户名；只计认证成功；跳过未知用户；记录处理位置 | 已修复（M8，`TestRadiusSync`） |
| K50 | Cloudflare：用去掉主域名后的前缀查询记录，Cloudflare 按完整域名匹配，查不到已有记录，每添加一次节点就新增一条重复的 A 记录；节点地址不在该域名下或解析失败时也会写入 | `CloudflareDriver::updateRecord`、`Admin/NodeController::add` | 使用完整域名；只处理该域名下、已解析出 IPv4 的节点；支持 API Token | 已修复（M8） |
| K51 | 同时在线 IP 数超限时按 IP 字符串顺序挑选要限制的 IP，可能断开早已在线的设备而放行新设备；购买套餐会用套餐的“IP限制”覆盖用户的 IP 数，管理员给个别用户设置的值（如 0、10）会被冲掉 | `Command/Job::CheckJob`、`Models/Shop::buy` | 保留先上线的 IP（按本次连续在线的开始时间，保存在 `ip_online` 表），限制后加入的；先解除到期限制再检查，仍超出的 IP 立即重新限制（原版会留出一两分钟空档）；节点 IP 按逗号拆分并去掉 # 后缀后排除（原版整串比较，多 IP 节点的中转连接会占用名额）；IP 统一写法后比较，无效的在线 IP 记录跳过（旧接口不再保存无效 IP）；后台保存的 IP 数必须是非负整数（原版无法识别的输入当作 0 即不限）；购买套餐不再修改用户的 IP 数（套餐中的值只作展示） | 已修复（`TestCheckJobConnectorOrder`、`TestCheckJobConnectorLongLived`、`TestCheckJobConnectorNormalize`、`TestCheckJobConnectorMalformed`、`TestShopBuy`、`TestSubscriptionExpiryHTTP`） |
| K52 | 自动续费：购买新套餐时“关闭旧套餐自动续费”可以不勾选（后台添加套餐默认不关闭），同一帐号多个套餐同时续费、重复扣款，旧套餐续费时把等级改回旧套餐并覆盖新套餐的等级时间；续费任务先列出到期订单再逐个扣款，扣款前不再确认订单仍需续费，用户刚关闭续费或刚购买新套餐时仍会被扣款 | `Command/Job::CheckJob`、`ShopController::buy` | 新增 `users.enable_auto_renew`，**默认关闭**（不记录续费时间、不续费扣款，含导入的旧订单）；开启后每个帐号只有最新购买的套餐自动续费（购买、后台添加、续费时都关闭其他订单的续费）；扣款在锁住用户后确认订单是该帐号最新的订单（较旧的订单不续费并关闭续费，旧数据中最新订单余额不足或未到期时也不回退续费旧套餐），并用条件更新确认订单仍开启续费且已到期，否则不扣款 | 已修复 |

## 订阅定制审核中发现的问题

以下问题来自订阅定制分支（`dingyue`）的新代码或本版此前的实现，不是原版问题，合并前已修复。

| 编号 | 问题 | 位置 | 修复方式 | 状态 |
| --- | --- | --- | --- | --- |
| S01 | 订阅响应头 `expire` 使用等级到期时间：普通用户为 1989 哨兵或注册后的默认值，客户端显示“已过期”或明天到期；响应头与正文口径不一致 | `internal/app/link.go`、`internal/sub/subscription_info.go` | 统一由 `sub.UserExpiry` 判断：付费用户写等级到期时间，普通用户不写 `expire`、正文显示注册时间，异常数据不写并记录警告；后台禁止保存等级大于 0 却没有等级到期时间的用户 | 已修复（`TestSubscriptionExpiryHTTP`、`TestUserExpiry`） |
| S02 | v2 接口的 DDNS 宽限期从查询参数 `node_id` 取节点，查询参数可以覆盖路径中的节点 ID；当场解析成功后没有记录认证时间 | `internal/app/nodeapi_v2.go` | 只认路径中的节点 ID，查询参数不一致返回 400；解析成功后记录认证时间；旧接口不变 | 已修复（`TestV2AuthRoutes`） |
| S03 | 流量批次去重表没有清理，记录无限增长；批次没有时间，无法确定去重记录可以保留多久 | `node_traffic_batch` | 批次必须带 `reported_at`，只接受 7 天内的批次（更早返回 410）；去重记录保留 30 天，每日任务每次删除 5000 条分批清理 | 已修复（`TestV2BatchWindow`） |
| S04 | 每分钟同步节点 IP 只处理 SS / V2Ray 等旧类型，SS2022 / VLESS / AnyTLS 节点的 DDNS 不会更新；同步时用解析结果覆盖整个“节点 IP”，手动添加的出口 IP 被删掉 | `internal/app/jobs.go` | 覆盖全部类型；只替换主 IP（第一个不带 # 的项），保留手动添加的项与 # 专用项；解析失败保持原值 | 已修复（`TestNodeIPSync`） |
| S05 | 后台保存域名节点时解析失败会把域名写入“节点 IP” | `internal/app/admin_node.go` | 解析失败时沿用填写或原有的 IP；新节点没有可用 IP 时提示错误，不保存 | 已修复（`TestNodeIPSync`） |
| S06 | 面板中转规则的默认目标 IP 取“节点 IP”的最后一项，追加出口 IP 后中转会指向出口 IP | `internal/app/relay.go` | 默认取主 IP（第一个不带 # 的项），`IP#源节点ID` 的专用目标优先 | 已修复（`TestNodeIPHelpers`） |
| S07 | 外部中转的 VMess TLS 节点：sing-box 等格式的证书校验名称原来回退到 Host 或原 IP，中转后变成原域名或中转地址 | `internal/sub/relay.go` | 各格式保留中转前实际生效的校验名称与传输 Host（按各自原有回退规则使用原地址）；旧 Clash 模板没有 SNI 字段，不输出此类节点 | 已修复（`TestVMessRelayKeepsTLSName`） |
| S08 | 旧面板格式 `198.51.100.10#8,203.0.113.20`：主 IP 取了第一项（专用项），其他源节点也被指向专用目标；自动更新主 IP 时覆盖了 #8 绑定；每分钟同步会按新规则改写仍然有效的旧中转规则 | `internal/app/nodeip.go`、`relay.go`、`jobs.go` | 主 IP 为第一个不带 # 的项，# 专用项永远保留；同步只更新目标已失效的规则；`xcat checkNodeIP` 只读列出新旧规则结果不同的规则 | 已修复（`TestNodeIPHelpers`、`TestLegacyRelayNodeIP`） |
| S09 | 在节点地址中直接粘贴 `原配置||地址;端口` 时按原文保存，没有统一为 `;||` | `internal/app/admin_node.go` | 保存时统一为 `;||`，原节点配置（含空字段）不变；只影响本次保存的记录 | 已修复（`TestComposeNodeServerRelay`） |
| S10 | WebAPI v2 的用户列表没有被限制的 IP，也没有上报在线 IP 的接口，使用 v2 的节点完全不受同时在线 IP 数限制 | `internal/app/nodeapi_v2.go` | 用户列表增加 `ip_limit`、`blocked_ips`；新增 `POST /mod_mu/v2/nodes/{id}/alive_ip`（与其他 v2 接口相同的认证；节点停用或流量超限时忽略上报），与旧接口的上报合并计算 | 已修复（`TestV2AliveIP`） |
| S11 | v2 流量批次中只要有一个用户已删除（422）或已不属于本节点（403，如套餐到期降为 0 级），整批永久失败；后端按顺序重试，该节点之后所有用户的流量都不入账，7 天后被丢弃 | `internal/app/nodeapi_v2.go` | 只跳过这几条、不计费，其余照常入账，响应 `ignored` 列出跳过的用户；节点已用流量按实际转发计算 | 已修复（`TestV2TrafficIgnoredUsers`） |
| S12 | 旧链接 `?mu=2`（VMess 链接列表）与用户首页「全部 URL」生成 VMess 链接时，`ws;tls`、`tcp;tls` 写法的节点遗漏 TLS 标记，客户端会用不加密的方式连接而失败；`?sub=3` 与各客户端格式正常 | `internal/sub` 的旧 VMess URI 生成 | 不修复：VMess 节点只使用不带 TLS 的 TCP 写法，需要 TLS 的场景使用 VLESS REALITY。新增或修改 VMess 节点时不要使用带 tls 的写法 | 已知，不修复（第三方验收 user-ui-sub 时发现） |
| S13 | 节点接口把 `user.disconnect_ip` 只按逗号拆分后原样下发（v2 `blocked_ips`、旧接口 `disconnect_ip`），旧库中的非法项（数字段超过 255、不完整或超过四段的 IPv4、冒号 / 分号、前导零等）使严格校验的后端拒绝整份用户列表：SS2022 退出、VMess 无法启动、VLESS 拒绝新连接；这些项没有 `disconnect_ip` 表记录，到期解除也清除不了 | `internal/app/nodeapi_v2.go`、`internal/app/nodeapi.go`、`internal/app/jobs_check.go` | 接口只下发合法的单个 IPv4 / IPv6（`internal/blockedip`，与后端判断一致），非法项跳过并按节点限频告警（分类与长度，不含原文）；到期解除按标准写法比较；新增 `panel xcat checkDisconnectIP` / `repairDisconnectIP`（影响清单、回滚 SQL、条件更新）；导入后报告异常 | 已修复 |
| S14 | 每分钟检查（超限新增）与到期解除先在事务外读取 `user.disconnect_ip`，之后用这份旧值整体写回：与 `xcat repairDisconnectIP` 或另一方并发时，已清理的历史项被写回（线上实际出现：修复后 1 个用户重新出现 108 项），或新增的限制被解除的旧值覆盖掉 | `internal/app/jobs_check.go` | 写入 `disconnect_ip` 的事务统一先锁用户行、再读写该用户的限制记录，字段在锁内重新读取后再写；新增记录与字段更新、删除记录与字段更新各在同一事务中；删除用户也先锁用户行。这些事务使用 READ COMMITTED（不锁索引间隙：REPEATABLE READ 下修复事务读取空记录时的间隙锁会挡住其他用户的插入，与用户行锁形成跨用户死锁），死锁时有界重试整个事务（3 次）；检查与解除单个用户失败时继续处理其他用户。并发回归测试覆盖线上时序、解除与新增两种先后顺序、空记录表多用户修复与检查并发、三方同时反复运行 | 已修复 |
| S15 | 后台首页「节点在线情况」沿用原版统计 `sort IN (0, 10, 11, 12, 13)` 且心跳 > 0 的全部节点：漏掉 SS2022 / VLESS / AnyTLS，停用的历史节点计入总数与离线，从未上报的新节点不计入（线上显示 59 个、在线 17，实际启用 45 个、在线 43） | `db/queries/admin.sql` AdminNodeStats | 只统计启用且属于当前支持类型（后台可选的 VMess、SS2022、VLESS、AnyTLS）的节点；90 秒内有心跳为在线，过期与从未上报为离线；停用与旧类型不计入 | 已修复 |
