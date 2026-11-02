# WebAPI v2：VMess / SS2022 / VLESS REALITY / AnyTLS

给节点后端开发者对照实现。面板侧接口已完成；节点后端需要按本文适配后才能使用 SS2022 / VLESS / AnyTLS。
原 `/mod_mu`、`/mu` 接口见 [node-api.md](node-api.md)，行为保持不变，不会因为本文的扩展而改变。

## 1. 认证

地址：`https://panel.example.com/mod_mu/v2/nodes/{节点ID}/{接口}?key={节点密钥}`

- **节点 ID 只认路径中的那个**：必须是正整数，不能是 0、不能带前导零（`/nodes/021/` 无效）。
  查询参数里也带了 `node_id` 时必须与路径一致，否则返回 400——查询参数不能覆盖路径中的节点 ID。
- 密钥：`security.mu_key`（或 `mu_key_list` 中任一个），只能出现一次。
- **来源 IP 绑定该节点**：请求的来源 IP 必须属于路径中的那个节点（“节点 IP”列表中的任一项），来自另一个节点的 IP 也不行。
  来源 IP 不符时，面板会当场重新解析该节点的域名（30 秒缓存），已指向来源 IP 则通过；
  DDNS 尚未更新时有宽限期（`security.node_ip_grace`，默认 10 分钟）：该节点已超过 90 秒没有从登记 IP 发来请求、
  且未超过宽限期时放行。节点仍在从登记 IP 正常上报时，其他 IP 一律拒绝。
- 必须 HTTPS 并校验面板证书；反向代理日志应屏蔽 `key` 参数。

| 情况 | HTTP 状态 |
| --- | --- |
| 路径中的节点 ID 无效，或查询参数 `node_id` 与路径不一致 | 400 |
| 密钥错误、缺少、重复，或节点不存在（两者结果相同，不暴露节点是否存在） | 403 |
| 来源 IP 不属于该节点 | 403 |

错误响应为纯文本说明；成功响应为 JSON `{"ret":1,"data":...}`。HTTP 200 且 `ret=1` 才算成功；
`ret=0` 表示面板内部写入失败，应保留数据并重试、告警。

“节点 IP”规则：逗号分隔。第一个不带 `#` 的项为节点主 IP（节点地址为域名时由面板按解析结果自动维护）；
其余项面板自动维护时不会覆盖——节点访问面板的出口 IP 与入口 IP 不同时（NAT、出口走其他线路），把出口 IP 加在后面即可。
带 `#源节点ID` 的项是面板中转规则的专用目标 IP（旧面板格式），同样保留。

## 2. 轮询

每 30～60 秒：GET `config` → GET `users`（请求 `users` 会记录节点心跳）；v2lite WebAPI 适配还需 GET `policy`（完整策略快照，见第 8 节）；每 60 秒 POST `alive_ip` 上报在线 IP（见第 6 节）。先校验完整成功的响应，再原子替换节点的内存 / 磁盘快照。
网络错误、非 200、`ret=0`、无法解析或未知协议都必须**保留上次成功的状态**；只有成功返回的空数组才能清空用户。

## 3. GET `config`

```json
{"ret":1,"data":{"api_version":2,"node_id":14,"sort":14,"enabled":true,"node_class":0,"node_group":0,"node_speedlimit":0,"traffic_rate":1.5,"traffic_unit":"raw_bytes_delta","protocol_config":{"protocol":"shadowsocks","server":"node.example.com","server_port":8443,"method":"2022-blake3-aes-256-gcm","server_key":"<server-key-base64>"}}}
```

| sort / protocol | protocol_config 字段 | 后端职责 |
| --- | --- | --- |
| 11、12 / vmess | server, server_port, alter_id, transport, tls, host, path, server_name | 用户 UUID 绑定到 VMess inbound，保持 AEAD / alter ID、传输与 TLS 一致 |
| 14 / shadowsocks | server, server_port, method, server_key | 多用户 SS2022：server_key 为共享服务密钥，每个用户的 password 为 user key |
| 15 / vless | server, server_port, server_name, public_key, short_id, flow, transport；可有 min_client_version | REALITY 私钥、证书、监听配置留在节点，不由面板返回；public key 必须对应节点上的私钥。short_id 可以为空 |
| 16 / anytls | server, server_port, server_name, transport | 节点自行配置有效的 TLS 证书，并加载每个用户的派生 password |

- `server` / `server_port` 是**真实节点地址和端口**（节点地址中 `;||` 之前的部分）。节点配置了外部 NAT 中转时，
  中转入口只出现在用户的订阅里，**不会**下发给节点后端；节点按真实端口监听即可。
- `enabled=false` 时应停止开放此节点的服务，但仍可上报已经产生的流量。
- 节点地址格式（面板侧校验）：只允许可见 ASCII；含中转后缀总长不超过 512 个字符；VLESS / AnyTLS 的原节点配置不超过 448 个字符
  （原为 128）。后端如有自己的长度限制，请按此放宽。

## 4. GET `users`

```json
{"ret":1,"data":[{"id":7,"node_speedlimit":0,"forbidden_ip":"","forbidden_port":"","expire_at":1792550400,"ip_limit":5,"blocked_ips":["203.0.113.9"],"method":"2022-blake3-aes-256-gcm","password":"<user-key>"}]}
```

- 公共字段：id、node_speedlimit（Mbps）、forbidden_ip、forbidden_port、expire_at（帐号有效期，Unix 秒）。不含 email、原始 passwd 或登录密码。
- 同时在线 IP 数限制：`ip_limit` 为该用户允许同时在线的 IP 数（0 不限，仅供参考）；`blocked_ips` 为面板判定超出限制、
  暂时禁止连接的 IP（数组，没有时为 `[]`）。**后端必须拒绝来自 `blocked_ips` 的新连接并断开已有连接**；列表每分钟更新，
  一个 IP 被限制 5 分钟后自动解除。限制由面板统一判断（合并所有节点上报的在线 IP），后端不要自行计算。
- SS2022：method、password。**password 只是 user key**，不要把 `server_key:user_key` 整串当作用户密钥（客户端订阅才使用组合密码）。
- VMess：uuid；VLESS：uuid + flow=`xtls-rprx-vision`；AnyTLS：password。
- 只返回可用用户：帐号启用、未过期、有剩余流量，且等级 / 分组符合节点（管理员例外）；节点 Type 与带宽限制同样生效。
- 节点地址格式无效时返回 422，**不是**成功的空列表，后端必须保留上次成功的用户列表。

凭据由面板派生，后端直接使用返回值即可，无需自行计算。派生规则（供核对）：

| 协议 | 用户凭据 |
| --- | --- |
| SS2022 | Base64(SHA256(`用户ID|passwd|2022-blake3-aes-256-gcm|节点ID|server_key_base64`))；客户端密码为 `server_key:user_key` |
| VMess、VLESS | UUID v3（DNS 命名空间，名称为 `用户ID|passwd`） |
| AnyTLS | 不带填充的 Base64URL(SHA256(`sspanel-anytls-v1` + NUL + 十进制用户ID + NUL + passwd)) |

保持历史用户 ID、passwd、节点 ID 与 server key 不变，导入后凭据就不变；其中任一值改变都会使连接凭据变化。

## 5. POST `traffic`

仅 JSON（`Content-Type: application/json`），最大 1 MiB，每批 1～10000 个用户，同一批中用户 ID 不能重复。

```json
{"batch_id":"node14-20261022-000123","reported_at":1792550400,"data":[{"user_id":7,"u":1024,"d":2048}]}
```

- `u` / `d` 为**原始字节增量**（非负整数），不是累计值，也不是已乘倍率的值。面板在事务内只乘一次节点 `traffic_rate`。
- `batch_id`：1～64 位 `[A-Za-z0-9_-]`，每个节点内唯一。后端先把未确认的批次落盘，成功后才删除；
  超时或断线必须用**同一个 batch_id、相同内容**重试，不能重新生成。
- `reported_at`（必填）：这批流量的产生时间，Unix 秒。重试时必须与首次提交相同（参与内容校验）。

**去重约定（重要）：**

- 面板只接受 `reported_at` 在 **7 天之内**的批次；去重记录保留 **30 天**，每日任务分批清理更早的记录。
- 接受窗口短于保留窗口，所以任何能被接受的批次，其去重记录一定还在，重复提交只会返回 `duplicate=true`，不会重复计费。
- 超过 7 天的批次返回 **410**：后端应**丢弃该批并告警，不要再重试**（节点断网超过 7 天期间的流量不再计入）。
- `reported_at` 晚于面板当前时间 10 分钟以上返回 400：请检查节点时钟（建议开启 NTP）。

| 结果 | 响应 | 后端处理 |
| --- | --- | --- |
| 成功 | `{"ret":1,"data":"ok","duplicate":false,"ignored":[]}` | 删除该批 |
| 重复提交（已计费） | `{"ret":1,"data":"ok","duplicate":true}` | 删除该批 |
| 同一 batch_id、不同内容 | 409 | 人工核查，不要换 ID 重发 |
| 超过 7 天 | 410 | 丢弃并告警 |
| 节点格式无效 | 422 | 保留该批，修正节点配置后自动恢复 |
| 数值或格式无效、reported_at 缺失或在未来 | 400 | 保留该批，修复后端 |
| 认证失败 | 403 | 保留该批，检查配置 |
| `ret=0` | 面板内部写入失败 | 保留该批，稍后重试 |

只有 410 表示应丢弃，其他所有失败都应保留该批。

- 可以上报刚过期、被禁用或流量刚用尽的用户的最后一批流量，照常计费。
- **已删除的用户、等级 / 分组已不符合本节点的用户**（如套餐到期被降为 0 级）：面板只跳过这几条、不计费，其余用户照常入账，
  整批仍返回成功，并在 `ignored` 中列出，如 `[{"user_id":7,"reason":"user_not_on_node"}]`（`reason` 为 `user_not_found` 或
  `user_not_on_node`）。后端按成功处理、删除该批即可，不要为这些用户重报。重复提交（`duplicate=true`）的响应不含 `ignored`。
- 用户相关的原因不会再让整批失败，所以一批流量不会因为某个用户的状态变化而永久卡住、挡住该节点之后的批次。
- 整批在一个事务中处理：数据库写入失败时整批都不生效（`ret=0` 或 5xx），不会只计入一部分。
- v2 不包含负载、审计记录等辅助接口，仍按 [node-api.md](node-api.md) 的旧接口上报。在线 IP 使用下面的 v2 接口。

## 6. POST `alive_ip`

每 60 秒上报一次最近 60 秒内有连接的用户 IP，面板据此执行同时在线 IP 数限制（结果通过 `users` 的 `blocked_ips` 下发）。
**不上报在线 IP 的节点，其用户不受 IP 数限制。**

```json
{"data":[{"user_id":7,"ip":"198.51.100.20"},{"user_id":7,"ip":"2001:db8::1"}]}
```

- 仅 JSON，最大 1 MiB，每次最多 10000 条；`data` 必填，没有在线用户时传 `[]`。
- `ip` 为客户端的真实来源 IP（IPv4 或 IPv6，IPv4 映射地址按 IPv4 处理）；同一请求中重复的条目只计一次。
- 只记录可以使用本节点的用户，其他用户的条目忽略。响应：`{"ret":1,"data":"ok","accepted":2,"ignored":0}`。
- 用户 ID 为 0、IP 无效、格式错误返回 400；失败时无需重试，下一分钟照常上报即可。
- 超出限制时，面板保留先上线的 IP（按本次连续在线的开始时间，离线超过 10 分钟后再出现视为重新上线），限制后加入的 IP。
  IP 按标准写法比较（IPv6 的不同写法视为同一个地址），每个 IPv6 地址单独计数。
- 节点停用或流量超限时（`users` 返回空列表），上报全部忽略（计入 `ignored`）。
- 被拒绝连接的 IP 不应再作为在线 IP 上报（没有连接就不是在线）。

## 7. 数据库

节点后端不需要、也不能访问面板数据库。面板侧新增去重表 `node_traffic_batch`（迁移 `00004`），与流量更新在同一事务中；
面板启动时自动执行迁移。数据库回滚或恢复时，去重表与流量数据必须一起回滚，不要单独删除去重记录后重放旧批次。


## 8. GET `policy`（v2lite 完整策略快照）

新增 `GET /mod_mu/v2/nodes/{id}/policy?key=...`，与 `config/users/traffic` 共用 `v2Node` 认证、节点绑定和 `Cache-Control: no-store`。既有 localhost 信任、DDNS 与宽限期语义不变。此接口只读，无数据库结构变更。

```json
{"ret":1,"data":{"api_version":2,"node_id":1,"generated_at":1792771200,"block_retention_sec":3600,"blocked_ips":[{"ip":"192.0.2.1","blocked_at":1792770900}],"detect_rules":[{"id":1,"regex":"blocked\\.example"}]}}
```

- `blocked_ips` 是该节点当前有效的完整来源 IP 封禁集合，只取 `blockip.nodeid = 路径节点 ID`；不把 nodeid=0 或其他节点事件合并。全体节点共享 `unblockip` 解封事件。
- 按规范化 IP 的最新时间归并；IPv4 mapped IPv6 转为 IPv4，IPv6 去 zone。解封时间严格大于封禁时间才移除；相同时间仍封禁。仅保留最近 3600 秒，忽略未来事件。空集合始终为 `[]`。
- 不依赖旧 `/func/block_ip`、`/func/unblock_ip` 的最近 60 秒窗口，因此后端重启能恢复仍在有效期内的封禁。
- 不是有效 IP 的封禁 / 解封记录（旧接口或旧后端原样写入的文本）跳过并在面板日志告警，不让整个快照失败。
- 用户在用户中心解封、管理员在管理后台解封时，面板都按标准化后的 IP 删除该 IP 在所有节点上的全部封禁记录（包括节点以 `::ffff:` 等写法记录的），
  解封记录 5 分钟后被清理也不会让这个 IP 重新出现在快照中。
- `detect_rules` 包含现有 `detect_list` 的 id/regex，空集合为 `[]`；后端沿用既有正则数量/长度限制。此接口不下发命令。
- 三次读取在只读 repeatable-read 事务中完成，策略查询超时 15 秒。每类事件最多读取 50000 行，规则最多 50000 条且单条最多 65536 字节；编码响应最多 32 MiB，读取过程中同步累计编码大小。超过上限或读取异常会整体失败，绝不返回成功的截断快照。
- 后端校验身份、版本、生成时间、完整数组后替换缓存；失败保留上次成功策略。后端现有 active block cap 为 20000，面板超过时后端拒绝该轮快照并告警，不静默丢弃封禁。

410 的具体落盘策略由后端决定：v2lite 将过期流量批次移出自动重试队列前写入持久卷的 `webapi-expired/` 并告警，保留原 batch_id/reported_at/data 供人工核对。不得更换 batch_id 自动补报；曾成功计费但响应丢失的批次，其历史计费结果不能仅凭 410 判断。
