# 架构与技术决策

本项目是对 SSPanel-UIM（2020-01 最终版，提交 `1a48707`）的业务原样重构：页面和业务流程与原版一致，
同时修复已知问题（见 [known-issues.md](known-issues.md)）。

## 原则

1. **业务原样重构**：页面、业务规则与原版保持一致，不额外增加功能。
2. **修复已知问题**：原版的安全问题和明显缺陷在重构时修复，每一处都记录在 known-issues.md。
3. **依赖最少**：优先使用 Go 标准库；第三方依赖必须是仍在维护、可替换的项目。
4. **安全默认**：SQL 全部参数化、模板默认转义、数据库强制 TLS、会话 Cookie 只含随机令牌。

## 技术栈

| 层 | 选型 | 说明 |
| --- | --- | --- |
| 语言 | Go（go.mod 中固定工具链版本） | 编译为单个静态程序文件，服务器上无需运行环境 |
| 前端 | 标准库 `html/template` + 原版 Material 主题 CSS + jQuery 3.7 | 页面结构一对一移植；jQuery 从 1.11/2.2/3.2 升级到 3.7.1（附 jquery-migrate） |
| 路由 | chi v5 | 与标准库 `net/http` 完全兼容；路由分组 + 中间件写法与原版 Slim 一致 |
| 数据库 | MySQL 8.4 LTS，驱动 go-sql-driver/mysql | 强制 TLS 并校验服务器证书与主机名（`tls_mode = verify`），不提供跳过校验的选项 |
| 数据访问 | sqlc | SQL 写在 `db/queries/`，生成类型安全的代码；动态排序只允许白名单字段 |
| 表结构变更 | goose | 变更文件打包进程序；启动时自动执行（MySQL `GET_LOCK` 加锁，失败则不启动）；变更前可执行备份命令 |
| 缓存 | 程序内存 + 统一接口 | 初期只缓存系统设置；不引入 Redis |
| 会话 | alexedwards/scs + MySQL 存储 | Cookie：HttpOnly、Secure、SameSite=Lax；登录可随时失效 |
| 密码 | argon2id | 兼容原版 md5/sha256 + salt 等格式，登录成功后自动升级 |
| 两步验证 | 自行实现 TOTP（标准库 HMAC-SHA1） | 与原版 PHPGangsta 实现兼容；约 60 行代码，不再引入 pquerna/otp |
| CSRF | 标准库 `http.CrossOriginProtection` | 基于 Sec-Fetch-Site / Origin，不影响节点后端和支付回调 |
| 定时任务 | 程序内置调度 + `GET_LOCK` | 取代 crontab + xcat；单个用户出错不影响其他用户 |
| 日志 | 标准库 `log/slog` | 访问日志不记录查询参数，订阅令牌等路径会被隐藏 |
| 配置 | TOML 文件 + 环境变量 `NIUNIU_<段>_<键>` | 未知配置项直接报错；密钥建议只用环境变量 |
| 部署 | 单个程序文件 + systemd，前置 Nginx | 见 [deploy.md](deploy.md)（待编写） |

## 目录结构

```
cmd/panel/        程序入口（serve / migrate / 后续的运维命令）
internal/
  config/         配置读取与校验
  db/             MySQL 连接（TLS）与命名锁
  migrate/        表结构变更执行
  server/         HTTP 服务：中间件、会话、渲染、错误页
  view/           模板渲染器
  logx/           日志
db/
  migrations/     goose 变更文件（打包进程序）
  queries/        sqlc 查询
web/
  templates/      页面模板（layouts / partials / pages）
  static/         静态资源，按原版路径对外提供（/theme、/assets、/images、/vendor）
deploy/dev/       本地开发环境（MySQL 8.4 + Mailpit）
docs/             项目文档
```

## 时间与时区

- 数据库会话时区固定为 UTC，DATETIME/TIMESTAMP 字段一律存 UTC。
- 页面显示和业务判断（到期、流量重置日等）按配置 `app.time_zone` 换算，默认 `Asia/Shanghai`（对应原版 `PRC`）。
- 原版数据中的时间是北京时间字符串，导入时统一换算为 UTC（见数据迁移文档）。

## 中间件顺序

真实 IP → 访问日志 → panic 恢复 → 安全响应头（含 CSP nonce）→ 跨站请求防护 → 会话 → 业务路由（各分组再挂登录、管理员、节点认证等中间件）。

## 内容安全策略（CSP）

- `script-src` 只允许本站文件和带本次请求 nonce 的内联脚本；模板中的内联脚本必须写成 `<script nonce="{{.Nonce}}">`。
- 不允许 `onclick=` 这类内联事件属性，移植模板时统一改为在脚本中绑定事件。
- 第三方脚本（如验证码服务）需要在配置中显式加入白名单。

## 模板移植约定

原版 Smarty 模板通过转换脚本机械转换后逐页人工复核，约定如下：

| 原版写法 | 新版写法 |
| --- | --- |
| `{$config['键名']}` | `{{cfg "键名"}}`（只暴露原版公开给模板的配置，见 `internal/server/legacycfg.go`） |
| `{$user->字段}` | `{{$.User.字段}}`（`*model.User`，未登录时为访客对象，`IsLogin` 为 false） |
| `{$变量}` | `{{$.Data.变量}}`（处理函数传入的 map） |
| `==`、`!=`、`<` 等 | `peq`、`pne`、`plt` 等，按 PHP 宽松比较语义实现 |
| `{if 条件}` | `{{if truthy 条件}}`，按 PHP 真假规则判断（`"0"` 为假） |
| `{include file='x.tpl'}` | `{{template "partials/x" $}}` |
| `onclick="return false;"` | `data-prevent` |
| `onClick="fn(a, b)"` | `data-call="fn" data-args='[a, b]'` |
| `setTimeout("location.href='/x'", 延时)` | `jumpTo('/x', 延时)` |
| `<script>` 内联脚本 | `<script nonce="{{$.Nonce}}">` |
