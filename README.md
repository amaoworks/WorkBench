# Workbench

Workbench 是本地优先、支持多用户的个人工作台，采用 Go 模块化单体、内嵌 React SPA 与共享数据库工作空间。前端静态产物嵌入单一二进制文件，平台统一提供鉴权、事件总线、定时调度、AI、通知、总览与设置；个人业务数据按用户隔离，管理员管理平台与共享服务配置。二进制默认使用 SQLite，Docker Compose 默认配套 PostgreSQL。

## 核心功能

| 领域 | 功能说明 |
|---|---|
| **总览看板** | 汇总待办、通知、行情与资讯卡片，支持卡片显隐、排序、尺寸调整与恢复默认 |
| **个人待办** | 按 已逾期 / 今天 / 本周 / 以后 / 已完成 分组，支持优先级、分类、到期提醒，以及与 [Wallos](doc/modules/todo-wallos.md) 订阅账期自动同步 |
| **投资终端** | 集成 TradingView 图表及只读持仓、订单与资产净值快照；未绑定 Schwab 时可通过共享富途服务只读看盘，个人 Schwab 授权有效时以 Schwab 为主、富途补充夜盘，下单/改单/撤单使用本人 Schwab 授权 |
| **监控规则** | 使用本人 Schwab 授权执行美股/ETF 分钟级价格监控，触发后生成站内通知并可推送至 Telegram 或移动设备 |
| **资讯与简报** | 支持 RSS/Atom、Google News 和 Yahoo Finance 订阅源、关注规则、AI 翻译摘要、定时简报生成及外部推送 |
| **AI 助手** | 内置多模型对话面板（`Ctrl+J` / `⌘J`），可调用已注册业务工具查询或创建待办、监控规则与资讯简报，刷新后保留会话上下文 |
| **多用户与移动端** | 管理员在「后台设置」管理用户、业务开关及共享凭据；每位用户在「个人设置」管理外观（宝蓝/陶橙、浅色/深色/跟随系统）、个人授权与通知接收；提供 iOS / Android 客户端接入与 APNs / FCM 推送 |

## 快速部署

### Docker Compose（默认 PostgreSQL）

仓库根目录的 [`compose.yaml`](compose.yaml) 从源码构建 Workbench 并启动 PostgreSQL 17 Alpine：

```bash
cp .env.example .env
chmod 600 .env
# 编辑 .env，设置 POSTGRES_PASSWORD
docker compose up -d --build
docker compose logs workbench
```

- **登录与初始化**：服务默认发布到宿主机 `127.0.0.1:8080`（可通过 `WORKBENCH_HOST_PORT` 修改，容器内固定为 `8080`）。首次启动自动生成管理员初始密码并输出到 `workbench` 容器日志，使用 `admin` 登录后须立即修改密码。
- **数据与持久化**：PostgreSQL 数据保存在 `postgres_data` 卷，应用运行文件（图表缓存、SQLite 数据库与备份）保存在 `workbench_data` 卷。切换 SQLite 需在 `.env` 设置 `WORKBENCH_DB_DRIVER=sqlite` 并将 `WORKBENCH_DATABASE_URL=` 置空。
- **反向代理与 HTTPS**：应用仅提供 HTTP。由宿主机 Nginx / Caddy 反代时设置 `WORKBENCH_PUBLIC_URL=https://实际域名`，模板见 [`deploy/nginx.conf.example`](deploy/nginx.conf.example)、[`deploy/Caddyfile.example`](deploy/Caddyfile.example)；反代同样运行在容器中时叠加 [`deploy/compose.proxy.yaml`](deploy/compose.proxy.yaml)，上游地址为 `http://workbench:8080`。
- **富途行情配套容器**：需要容器化 OpenD 时叠加 [`futu-opend/compose.workbench.yaml`](futu-opend/compose.workbench.yaml)，说明见 [`futu-opend/README.md`](futu-opend/README.md)。
- **发布镜像部署**：从 [Releases](https://github.com/amaoworks/WorkBench/releases) 下载 `workbench_<版本>_deployment.tar.gz`，在 `.env` 设置 `POSTGRES_PASSWORD` 与 `WORKBENCH_VERSION` 后执行 `docker compose -f compose.image.yaml up -d`。完整步骤见[部署与发布](doc/deployment.md)。

### 二进制部署（默认 SQLite）

从 [Releases](https://github.com/amaoworks/WorkBench/releases) 下载对应架构压缩包与 `SHA256SUMS`（以 `amd64` 为例）：

```bash
sha256sum --check --ignore-missing SHA256SUMS
tar -xzf workbench_v0.1.1_linux_amd64.tar.gz
cd workbench_v0.1.1_linux_amd64
./workbench -version
./workbench
```

默认监听 `http://127.0.0.1:8080`（单用户 `local` 模式），数据保存在 `~/.workbench/data.db`。服务器反代部署使用 `-auth password -public-url https://实际域名`，长期运行可使用 [`deploy/workbench.service`](deploy/workbench.service)。外部 PostgreSQL 与 systemd 配置见[部署与发布](doc/deployment.md)。

## 本地开发与验证

构建与开发需 Go 1.27.1（以 [`go.mod`](go.mod) 为准）、Node.js 24+、npm 11+；修改 SQL 查询需 sqlc 1.31.x。首次运行前先执行 `npm ci --prefix web`。

| 命令 | 用途 |
|---|---|
| `./scripts/dev.sh -auth password -listen 127.0.0.1:9090` | 启动 Go 后端与 Vite 开发服务器（前端默认 `http://127.0.0.1:5173`，支持热更新） |
| `./scripts/db-manage.sh init` | 在 Docker 中初始化本地开发用 PostgreSQL 并生成 `.env.dev-pg`（配合 `./scripts/dev.sh --config .env.dev-pg`） |
| `./scripts/test.sh` | 仓库基准检查：sqlc 漂移、Go 测试、前端/终端 lint、前后端契约测试与前端生产构建 |
| `./scripts/build.sh` | 构建前端生产产物并编译根目录 `./workbench` 静态二进制文件 |
| `sqlc generate` | 修改 `migrations/` 或 `query/*.sql` 后重新生成 SQL 代码 |

通过外部域名调试开发模式时，反向代理上游须指向 **Vite 前端端口** 并开启 WebSocket 支持，同时传入 `-public-url` 与 `-allowed-host`。完整参数、配置文件与浏览器 E2E 回归说明见[开发脚本](scripts/README.md)与[配置与运行](doc/configuration.md)。

## CI 与版本发布

GitHub Actions 自动构建并验证 Linux `amd64` / `arm64` 双架构二进制与 Docker 镜像：

- **PR 验证**：运行静态检查、测试、双架构镜像构建及 SQLite / PostgreSQL Compose 冒烟验证。
- **开发构建**：推送 `main` 或手动触发 [`ci.yml`](.github/workflows/ci.yml)，在运行结果 **Artifacts** 中生成保留 14 天的二进制与镜像包。
- **正式发布**：基于 `main` 分支提交推送 `v*` 标签触发 [`release.yml`](.github/workflows/release.yml)，自动发布 GitHub Release 与 GHCR 多架构镜像：

```bash
git checkout main && git pull --ff-only origin main
git tag -a v0.1.1 -m "Release v0.1.1"
git push origin v0.1.1
```

## 目录结构

```text
cmd/workbench/          可执行入口、命令行参数、健康探针与信号处理
internal/app/           平台装配、设置管理、启动与优雅退出
internal/contracts/     跨层接口与契约类型定义
internal/foundation/    基础设施层：数据库、认证、事件总线、日志
internal/capabilities/  通用能力层：AI、定时调度、通知、总览
internal/modules/       业务后端实现：todo、investment、news
internal/webui/         内嵌前端静态产物与 HTTP Handler
web/src/modules/        业务前端页面、Widget、设置、查询与 Schema
web/src/                应用外壳、平台页面、共享组件与工具
client/                 iOS 与 Android 原生客户端工程
futu-opend/             配套富途 OpenD 容器镜像与登录配置管理
deploy/                 systemd、Nginx/Caddy 反代与容器网络覆盖示例
scripts/                开发联调、构建发布、契约检查与浏览器回归脚本
doc/                    架构设计、业务开发、部署配置与接口文档
```

## 文档导航

完整索引见[文档入口](doc/README.md)：

- **架构与开发**：[架构与目录](doc/architecture.md) · [业务开发指南](doc/business-development.md) · [接口与契约](doc/contracts.md) · [数据设计](doc/data-model.md) · [前端界面规范](doc/frontend-guidelines.md) · [文案与文档规范](doc/copywriting-guidelines.md) · [AI 协作指南](AGENTS.md)
- **运维与安全**：[部署与发布](doc/deployment.md) · [配置与运行](doc/configuration.md) · [安全边界](doc/security.md) · [用户与权限](doc/multi-user.md)
- **业务与客户端**：[Todo](doc/modules/todo.md) · [Wallos 联动](doc/modules/todo-wallos.md) · [Investment](doc/modules/investment.md) · [资讯](doc/modules/news.md) · [外部通知](doc/notifications.md) · [移动端接入](doc/mobile.md)（[OpenAPI](doc/mobile.openapi.yaml) · [客户端路线](client/README.md)）
