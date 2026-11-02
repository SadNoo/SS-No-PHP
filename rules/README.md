# 规则集

sing-box 订阅配置中「🫥SiriAI」策略组使用的规则集，面板生成的配置从本仓库下载：

- `sing-box/1.14/SiriAI.srs`、`sing-box/1.15/SiriAI.srs`：二进制规则集（客户端下载使用）
- `sing-box/*/SiriAI.json`：对应的源文件（sing-box source rule-set，version 2）

修改源文件后用 sing-box 重新编译：`sing-box rule-set compile sing-box/1.15/SiriAI.json`，两个版本目录的内容保持一致。
Surge / Loon / Clash 等格式的 SiriAI 规则内置在 `internal/sub/client_rules.go`，修改时一并更新。
