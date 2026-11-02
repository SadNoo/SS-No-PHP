# 节点后端 WebAPI 说明

给节点后端开发者对照用：节点后端只要按本文调用这些接口，就能对接 niuniupanel。
本文描述旧版兼容接口；新增 VMess / SS2022 / VLESS / AnyTLS 结构化接口见 [WebAPI v2](node-api-v2.md)。

兼容接口与 SSPanel-UIM 2020 版的 `/mod_mu`（后端配置里常叫 `modwebapi`）完全一致，返回内容逐字相同；
原来能以 WebAPI 方式对接 SSPanel-UIM 的后端不需要修改。

## 1. 公共约定

- 地址：`{面板地址}/mod_mu/{接口}`，例如 `https://panel.example.com/mod_mu/users`
- 认证：每个请求都在查询参数中带 `key={节点密钥}`，节点密钥为面板配置 `security.mu_key`（或 `mu_key_list` 中任一个）
- **来源 IP 校验**：请求的来源 IP 必须等于后台某个节点的“节点 IP”（逗号分隔的多个地址中的任一个，精确匹配），
  否则返回 `{"ret":0,"data":"token or source is invalid, Your ip address is x.x.x.x"}`。从面板本机（127.0.0.1）发起的请求不检查
- **动态 IP（DDNS）节点**：后台节点“地址”填 DDNS 域名即可。来源 IP 对不上时，面板会立即重新解析该域名，
  已指向新 IP 就直接通过并更新“节点 IP”的第一项；DDNS 还没更新时，有宽限期（`security.node_ip_grace`，默认 10 分钟）：
  节点已停止从原 IP 发来请求（超过 90 秒）后，带正确密钥和 `node_id` 的请求可以从新 IP 通过。
  节点仍在从原 IP 正常上报时宽限期不生效，因此密钥泄露也无法冒充正常运行中的节点。动态 IP 节点请求时务必带上 `node_id`
- 节点 ID：查询参数 `node_id={后台节点列表中的 ID}`；为 `0` 时按来源 IP 自动识别节点
- POST 请求体：JSON（`Content-Type: application/json`，推荐）或表单均可
- 返回：`{"ret":1,"data":...}`，失败为 `{"ret":0,"data":"原因"}`。返回固定只有 `ret`、`data` 两个键
- 时间为 Unix 秒；流量为字节；HTTP 状态码始终为 200，以 `ret` 判断成功与否

## 2. 节点每个周期需要调用的接口

一般每 60 秒执行一轮：先拉取配置和用户，再上报上一周期的数据。

### GET `/mod_mu/func/ping` — 连通性检查

返回 `{"ret":1,"data":"pong"}`。

### GET `/mod_mu/nodes/{节点ID}/info` — 读取本节点配置

```json
{"ret":1,"data":{"node_group":0,"node_class":0,"node_speedlimit":0,"traffic_rate":1,"mu_only":0,"sort":0,"server":"hk.example.com","type":"ss-panel-v3-mod_Uim"}}
```

`traffic_rate` 为流量倍率（面板记账时自动乘以倍率，节点上报原始流量即可）；`node_speedlimit` 单位 Mbps，0 不限速；
`sort` 为节点类型（0 SS/SSR、9 单端口多用户、10 SS 中转、11 V2Ray、12 V2Ray 中转、13 SS + V2Ray 插件）。

### GET `/mod_mu/users?node_id={节点ID}` — 拉取可用用户（同时记录节点心跳）

只返回有权使用本节点（等级、分组符合）、帐号启用、未过期、流量未用完的用户：

```json
{"ret":1,"data":[{"id":2,"email":"a@example.com","passwd":"连接密码","u":0,"d":0,"port":20001,"method":"aes-256-cfb",
 "node_speedlimit":0,"protocol":"origin","protocol_param":"","obfs":"plain","obfs_param":"",
 "forbidden_ip":"","forbidden_port":"","disconnect_ip":"","is_multi_user":0,"uuid":"V2Ray 使用的 UUID"}]}
```

节点本月流量超过后台设置的上限时返回 `{"ret":1,"data":null}`（此时应停止全部用户）。
**节点后台显示“在线”依赖这个接口的心跳**，即使没有用户也要定期调用。

### POST `/mod_mu/users/traffic?node_id={节点ID}` — 上报用户流量

```json
{"data":[{"user_id":2,"u":1048576,"d":5242880}]}
```

`u` 上传、`d` 下载，为本周期的**增量**（不是累计值）。整批在一个事务中记账，要么全部成功要么全部不生效，
失败（`ret` 为 0）时可以整批重试，不会重复计费。

### POST `/mod_mu/nodes/{节点ID}/info?node_id={节点ID}` — 上报负载

```json
{"uptime":"12345.6","load":"0.10 0.20 0.30"}
```

### POST `/mod_mu/users/aliveip?node_id={节点ID}` — 上报在线 IP

```json
{"data":[{"user_id":2,"ip":"203.0.113.9"}]}
```

用于“在线设备数限制”和用户的在线 IP 记录。没有数据时发送 `{"data":[]}`。

## 3. 可选接口

| 接口 | 用途 | 请求 / 返回 |
| --- | --- | --- |
| GET `/mod_mu/nodes` | 全部节点（后端用来识别中转、互相放行） | 返回节点数组，含 `id`、`node_ip`、`sort` 等 |
| GET `/mod_mu/func/detect_rules` | 审计规则 | `[{"id":1,"name":"","text":"","regex":"正则","type":1}]`，`type` 1 匹配明文、2 匹配十六进制 |
| POST `/mod_mu/users/detectlog` | 上报触发审计规则的记录 | `{"data":[{"user_id":2,"list_id":1}]}` |
| GET `/mod_mu/func/relay_rules?node_id=` | 以本节点为起点的中转规则 | `[{"id":1,"user_id":0,"source_node_id":1,"dist_node_id":2,"dist_ip":"1.2.3.4","port":443,"priority":0,...}]`，`user_id` 为 0 表示对所有用户生效 |
| POST `/mod_mu/func/block_ip` | 上报要封禁的 IP（如扫描者） | `{"data":[{"ip":"198.51.100.7"}]}` |
| GET `/mod_mu/func/block_ip` | 最近一分钟新增的封禁 IP | `[{"id":1,"nodeid":1,"ip":"...","datetime":1700000000}]` |
| GET `/mod_mu/func/unblock_ip` | 最近一分钟解封的 IP | `[{"id":1,"ip":"...","datetime":1700000000,"userid":2}]` |
| POST `/mod_mu/func/speedtest` | 上报测速结果 | `{"data":[{"telecomping":"","telecomeupload":"","telecomedownload":"","unicomping":"","unicomupload":"","unicomdownload":"","cmccping":"","cmccupload":"","cmccdownload":""}]}` |
| GET / POST `/mod_mu/func/autoexec` | 读取后台下发的命令 / 上报执行回执 | 节点只能上报回执，不能下发命令（K37） |

**不再提供**：`POST /mod_mu/nodes/config`（原版会把面板数据库的帐号密码返回给节点，K36），调用时返回 `{"ret":0,"data":"database mode is not supported, please use webapi"}`。
节点不能、也不需要直连面板数据库。

## 4. 对接自查

在节点服务器上执行（替换地址、密钥、节点 ID）：

```sh
curl -s 'https://panel.example.com/mod_mu/func/ping?key=节点密钥'
curl -s 'https://panel.example.com/mod_mu/users?key=节点密钥&node_id=1'
```

- 第一条返回 `{"ret":1,"data":"pong"}`、第二条返回用户列表：对接正常
- 返回 `token or source is invalid, Your ip address is x.x.x.x`：把返回中的 IP 加到后台该节点的“节点 IP”
  （面板前面有 CDN / 代理时见 deploy.md 中的 `trusted_proxies`）
- 返回 `token or source is invalid`（不带 IP）：密钥不对
