# 订阅格式说明

订阅地址：`{面板地址}/link/{订阅令牌}?{格式参数}`。每个请求只能选一种格式，选多个返回 400。

## 协议范围

订阅只包含 **SS2022**（节点类型 14）、**VMess**（11 / 12）、**VLESS REALITY**（15）、**AnyTLS**（16）。
订阅里的 “SS” 一律表示 SS2022；传统 SS / SSR 节点记录保留在数据库中，但不会出现在任何订阅里。

## 格式参数

| 参数 | 输出 | 包含的协议 |
| --- | --- | --- |
| `sing-box=1` | sing-box 完整配置；`sb_version=1.14` / `1.15`，`quic=0/1`、`tailscale=0/1` 选择模式 | 四种 |
| `clash=1` / `list=clash` | Mihomo 完整配置 / proxy-provider | 四种 |
| `surge=5` / `list=surge` | Surge 5 完整配置 / 节点行 | SS2022、VMess、AnyTLS |
| `surge=4` | Surge 4 完整配置 | VMess |
| `loon=1` / `list=loon` | Loon 完整配置 / 节点行 | 按客户端能力 |
| `quantumultx=1` / `list=quantumultx` | Quantumult X 节点订阅 | 按客户端能力 |
| `shadowrocket=1` / `list=shadowrocket` | Shadowrocket | 四种 |
| `v2ray=1` / `list=v2rayn` | v2rayN / v2rayNG | SS2022、VMess、VLESS |
| `ss2022=1` / `sub=2` / `list=ss2022` | SS2022 URI 列表（Base64） | SS2022 |
| `list=ss` | SS2022 URI 列表（原文） | SS2022 |
| `vmess=1` / `sub=3` / `list=vmess` | VMess URI 列表 | VMess |
| `vless=1`、`anytls=1` | 各协议独立 URI 列表 | 对应协议 |
| `mu=2` | 旧 VMess 订阅（保留历史编码） | VMess |
| `mu=4` | 旧 Clash 模板 | VMess |
| `quantumult=1/2/3` / `list=quantumult` | 旧 Quantumult：节点订阅 / 含来源的完整配置 / 完整配置 | VMess |
| `kitsunebi=1` / `list=kitsunebi` | Kitsunebi 专用 VMess URI | VMess（见下方限制） |
| `surfboard=1` / `list=surfboard` | Surfboard 完整配置 / 节点行 | SS2022、VMess |

不支持旧的远程模板参数 `source`、`profiles`：填写非空值返回 400。

## 已停用的格式（返回 410）

- 不带格式参数的默认链接；
- SSR / ClashR：`mu=0`、`mu=1`、`sub=1`、`list=ssr`、`list=clashr`、`clash=2`；
- SSD：`ssd=1`、`list=ssd`、`mu=3`；
- 只用于传统 SS 的格式：`surge=1/2/3`、`list=ssa`、`list=quantumult-ss`。

## 筛选参数

可与任一格式同时使用。先按用户权限确定可用节点，再用筛选缩小范围，筛选不能获得无权使用的节点。

| 参数 | 作用 |
| --- | --- |
| `type` | `all` / `ss` / `ss2022` / `vmess` / `vless` / `anytls`（`ss` 与 `ss2022` 相同） |
| `class` | 只保留这些等级的节点，如 `0+1`、`0,1` |
| `noclass` | 排除这些等级；与 `class` 同时填写时以 `class` 为准 |
| `regex` | 按节点名称筛选（RE2 语法，最长 512 字节），支持 `/pattern/i` 写法 |
| `emoji` | `1` 按地区在名称前加旗帜（已有旗帜不重复）；`0` 不添加 |
| `extend` | `1` 附带流量与到期信息；`0` 不附带；不填保持各格式默认 |

参数重复、格式无效或正则无法使用时返回 400，不会忽略筛选。完整配置筛选后没有节点时返回失败，不会退回未筛选的结果。

## 流量与到期信息

所有格式都带 `Subscription-Userinfo` 响应头（`upload`、`download`、`total`，付费用户另有 `expire`）。
`extend=1` 时正文附带的信息与响应头使用同一套规则：

| 用户 | 响应头 `expire` | 正文 |
| --- | --- | --- |
| 付费用户（等级大于 0） | 等级到期时间 | 套餐到期：日期 |
| 普通用户（等级 0） | 不写 | 注册时间：日期 |
| 等级大于 0 但没有等级到期时间（异常数据） | 不写 | 套餐到期：未设置，请联系管理员 |

帐号有效期不在订阅中显示（新帐号有效期为 100 年）。异常数据会记录警告日志，可用 `xcat checkClassExpire` 列出，
后台保存用户时也会拒绝这种组合。

## 外部中转

节点地址末尾带 `;||中转地址;中转端口` 时，订阅和节点详情中的连接地址、端口换成中转入口；
密钥、UUID、SNI、REALITY 参数等不变。VMess 开启 TLS 时，各格式实际生效的证书校验名称（显式 SNI，否则 Host，否则原节点地址，
原地址是 IP 时为该 IP）与中转前相同；WebSocket / HTTP/2 等传输的 Host 也与中转前相同。纯 TCP 节点只替换地址和端口，不会开启 TLS。
节点后端拿到的仍是真实地址，见 [node-api-v2.md](node-api-v2.md)。

## 客户端限制

- **Kitsunebi**：该格式没有 SNI 字段，需要 TLS 且 SNI 与连接地址不同的 VMess 节点（包括配置了中转的 TLS 节点）不会输出。
- **旧 Clash（`mu=4`）**：模板没有 SNI 字段，配置了中转的 VMess TLS 节点不会输出。
- **Surge 4、旧 Quantumult、旧 Clash（`mu=4`）**：只支持 VMess。
- **Surfboard**：SS2022 需要支持该协议的客户端版本。

## 完整配置的规则来源

sing-box、Surge 4 / 5、Loon、Mihomo、Surfboard、旧 Quantumult 的完整配置模板内置在程序中（`internal/sub/profiles`、`internal/sub/templates`），
强制走「🍃 Proxy」的域名后缀来自配置 `app.sub_proxy_domains`（为空时不输出），排在 SiriAI、Apple 与国内规则之前。

面板不会请求远程模板。Surge / Loon 规则集引用的每个外部资源都由测试核对（`internal/sub/testdata/surge-loon-rule-audit.json`）。
所有完整配置都保留回环地址 `127.0.0.0/8` 与 `::1/128` 直连。
