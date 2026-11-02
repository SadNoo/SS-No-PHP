# SS-No-PHP

使用 Go 重写的代理服务面板，业务与页面对应 SSPanel-UIM 2020 最终版，并修复了原版的已知安全问题。

- 一键部署（Docker + Cloudflare，推荐）：[docs/deploy-docker.md](docs/deploy-docker.md)
- 手动部署（systemd + nginx）：[docs/deploy.md](docs/deploy.md)
- 从 SSPanel-UIM 迁移：[docs/migration.md](docs/migration.md)
- 节点后端对接（WebAPI）：[docs/node-api.md](docs/node-api.md)；SS2022 / VLESS / AnyTLS 使用 [WebAPI v2](docs/node-api-v2.md)
- 订阅格式与筛选参数：[docs/subscription-formats.md](docs/subscription-formats.md)
- 客户端订阅教程：[docs/client-tutorial](docs/client-tutorial/README.md)
- 架构与技术决策：[docs/architecture.md](docs/architecture.md)
- 已知问题修复清单：[docs/known-issues.md](docs/known-issues.md)
- 开发进度：[docs/progress.md](docs/progress.md)

## 快速试用

装有 Docker 的任意机器上：

```sh
git clone https://github.com/SadNoo/SS-No-PHP.git && cd SS-No-PHP/deploy/demo
docker compose up -d --build      # 首次需要几分钟编译
```

打开 `http://服务器IP:8080`（不是本机访问时先设置 `DEMO_URL=http://服务器IP:8080`）。
演示帐号：管理员 `admin@demo.local` / `DemoAdmin123`，用户 `user@demo.local` / `DemoUser123`。
`docker compose down -v` 删除演示环境。演示环境密钥固定、未启用 HTTPS，不要用于正式运营。

## 本地开发

需要：Go（版本见 `go.mod`，`GOTOOLCHAIN=auto` 时会自动下载）、Docker。

```sh
make dev-up                       # 启动 MySQL 8.4（强制 TLS）和 Mailpit
cp config.example.toml config.toml
export NIUNIU_APP_KEY=$(openssl rand -hex 24)
export NIUNIU_DATABASE_PASSWORD=panelpass
export NIUNIU_DATABASE_TLS_CA=$PWD/deploy/dev/certs/ca.pem
export NIUNIU_SECURITY_COOKIE_SECURE=false   # 本地 http 开发时
make run                          # 启动服务，自动执行表结构变更
```

访问 http://127.0.0.1:8080 ，开发邮件在 http://127.0.0.1:8025 查看。

## 常用命令

```sh
panel -config config.toml serve           # 启动服务
panel -config config.toml migrate status  # 查看表结构变更状态
panel -config config.toml migrate up      # 手动执行变更
panel -config config.toml migrate down    # 回退最近一次变更
panel -config config.toml xcat            # 运维命令（原版 php xcat）
panel -config config.toml import-legacy   # 从 SSPanel-UIM 旧库导入数据
make test / make lint                     # 测试 / 静态检查
```

## 许可证

本项目以 MIT 许可证发布，见 [LICENSE](LICENSE)。

`web/static` 与 `web/templates` 中来自 [SSPanel-UIM](https://github.com/Anankke/SSPanel-Uim) 的页面、样式与图片遵循 MIT 许可证，见 `web/static/LICENSE-SSPanel-UIM`。
