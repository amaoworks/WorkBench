# Workbench

Workbench 是本地优先、单用户的个人工作台，采用 Go 模块化单体、React 前端和 SQLite 工作空间。前端资源嵌入可执行文件，平台统一提供鉴权、事件、调度、AI、通知、总览和设置。

当前业务包括待办及 Wallos 订阅提醒、投资（Schwab 行情、账户和 TradingView 终端）。业务可在设置中启停，数据和总览布局偏好会保留。新增业务按模块扩展，前后端使用对应的模块目录。

## 核心特性

- **个人待办**：支持待办管理、到期提醒，以及与 Wallos 订阅账期的自动联动。
- **投资终端**：对接 Charles Schwab API，集成 TradingView 图表，支持实时行情、持仓与订单管理。
- **价格预警**：美股/ETF 分钟级价格监控，支持站内通知与 Telegram 自动推送。
- **AI 助手**：内置对话面板，支持动态调用已启用的业务工具。
- **总览看板**：可定制卡片显隐、排序、尺寸与布局偏好。
- **独立可控**：单可执行文件内嵌前端，本地 SQLite 持久化，支持零外部依赖单机运行或容器部署。

## Docker Compose 部署

从 [GitHub Releases](https://github.com/amaoworks/WorkBench/releases) 下载 `workbench_<版本>_deployment.tar.gz` 并解压，或在本仓库根目录操作。程序镜像为 `ghcr.io/amaoworks/workbench`，支持 Linux amd64/arm64。

```bash
cp .env.example .env
chmod 600 .env
```

编辑 `.env`，填写已发布版本、反向代理的外部地址和首次登录密码：

```dotenv
WORKBENCH_IMAGE=ghcr.io/amaoworks/workbench
WORKBENCH_VERSION=v0.1.1
WORKBENCH_PORT=8080
WORKBENCH_PUBLIC_URL=https://workbench.example.com
WORKBENCH_PASSWORD='替换为自己的强密码'
```

`v0.1.1` 是示例，请使用实际已发布的版本。首次密码至少 8 个字符，包含大写、小写、数字、特殊符号四类中的至少三类。配置后启动：

```bash
docker compose pull
docker compose up -d --wait
docker compose ps
docker compose logs -f --tail 100 workbench
```

应用只提供 HTTP，Compose 默认将端口发布到宿主机 `127.0.0.1:8080`，由 Nginx/Caddy 反代并提供 HTTPS。代理需要保留原始 Host、支持 WebSocket 和 SSE；模板见 [Nginx](deploy/nginx.conf.example)、[Caddy](deploy/Caddyfile.example)。通过 `.env` 中配置的 HTTPS 地址访问和登录。

数据保存在挂载到 `/data` 的持久化卷，包含数据库和备份。升级时先在设置中备份，再修改 `.env` 的版本，执行 `docker compose pull && docker compose up -d --wait`。`docker compose down` 保留数据，`docker compose down --volumes` 会删除数据卷。

已有反代也运行在容器内时，使用[容器网络覆盖配置](deploy/compose.proxy.yaml)，同一网络的上游地址为 `http://workbench:8080`。完整部署、权限及恢复步骤见[部署与发布](doc/deployment.md)。

尚未发布版本时，也可在仓库根目录先构建本地镜像：

```bash
docker build -t workbench:local .
# 在 .env 中设置 WORKBENCH_IMAGE=workbench、WORKBENCH_VERSION=local
docker compose up -d --wait
```

## 二进制部署

从 [Releases](https://github.com/amaoworks/WorkBench/releases) 下载对应架构的二进制包和 `SHA256SUMS`。例如 amd64：

```bash
sha256sum --check --ignore-missing SHA256SUMS
tar -xzf workbench_v0.1.1_linux_amd64.tar.gz
cd workbench_v0.1.1_linux_amd64
./workbench -version
./workbench
```

默认访问 `http://127.0.0.1:8080`，数据保存在 `~/.workbench/data.db`。前端、迁移和时区数据已嵌入，无需安装 Go、Node.js 或数据库服务。服务器反代部署使用 `-auth password -public-url https://实际域名`，首次启动通过 `WORKBENCH_PASSWORD` 设置密码；长期运行可使用 [systemd 示例](deploy/workbench.service)。

## 日志与排障

进入 **设置 → 数据与运行 → 运行日志**，选择 `debug / info / warn / error` 最低等级，点击「保存日志等级」后立即生效，重启后保留。默认 `info`；选择 `warn` 会同时记录警告和错误，选择 `error` 仅记录错误。

二进制和容器均输出 JSON 日志到 stderr。Docker 使用 `docker compose logs -f --tail 100 workbench`，systemd 使用 `journalctl -u workbench -f`。首次启动默认等级可通过 `WORKBENCH_LOG_LEVEL=info` 或 `-log-level info` 设置；页面已保存的等级优先。详细字段、等级规则和轮转说明见[运行日志](doc/configuration.md#运行日志)。

## GitHub Actions 编译与发布

项目通过 GitHub Actions 自动化构建多架构（Linux amd64/arm64）二进制与 Docker 镜像：

- **开发构建**：推送 `main` 或在 [CI and Build](.github/workflows/ci.yml) 手动触发分支构建，通过验证后可在运行结果的 **Artifacts** 中下载 14 天有效的预构建二进制与 Docker 镜像包。
- **PR 验证**：提交 PR 时运行检查、双架构镜像构建与部署验证，不上传可下载的构建包。
- **正式版本发布**：推送 `v*` 版本标签触发 [Release](.github/workflows/release.yml) 工作流，通过多架构交叉编译与运行验证后，自动发布 GitHub Release 并推送 GHCR 镜像。发布标签必须基于 `main` 分支提交：

```bash
git checkout main
git pull origin main
git tag v0.1.1
git push origin v0.1.1
```

流水线矩阵、镜像导入验证及分支约束细节见[部署与发布 - GitHub Actions](doc/deployment.md#github-actions-编译与发布)。

## 文档

[文档入口](doc/README.md)汇总当前实现的设计和使用说明：

- [架构与目录](doc/architecture.md)
- [业务开发指南](doc/business-development.md)
- [接口与契约](doc/contracts.md)
- [数据设计](doc/data-model.md)
- [部署与发布](doc/deployment.md)：二进制、Docker Compose、反向代理与 GitHub Actions
- [配置与运行](doc/configuration.md)
- [安全边界](doc/security.md)
- [文案与文档规范](doc/copywriting-guidelines.md)
- [Todo](doc/modules/todo.md)、[Wallos 联动](doc/modules/todo-wallos.md)、[Investment](doc/modules/investment.md)
- [AI 协作指南](AGENTS.md)：面向 AI 编程助手的架构红线与开发工作流


## 目录

```text
cmd/workbench/          可执行入口、参数和信号
internal/app/           平台装配、设置、启动与退出
internal/contracts/     跨层接口和描述类型
internal/foundation/    基础设施层
internal/capabilities/  通用能力层
internal/modules/       业务应用层：todo、investment
internal/webui/         内嵌前端资源和 HTTP Handler
web/src/modules/        对应业务的页面、Widget、设置、查询和类型
web/src/                应用外壳、平台功能、共享组件和工具
deploy/                 systemd、反向代理和容器网络配置示例
scripts/                构建、发布、测试和浏览器回归
doc/                    当前设计与开发运行说明
```

## 从源码构建与运行

日常修改前端可使用开发模式。首次安装前端依赖后，一条命令启动 Go 后端和 Vite：

```bash
npm ci --prefix web
./scripts/dev.sh -auth password -listen 127.0.0.1:9090 -allowed-host w.vm2.de5.net
```

浏览器访问启动摘要中的前端地址（默认 `http://127.0.0.1:5173`）。React 和 CSS 保存后热更新，投资终端 HTML/JS 保存后自动刷新。Go 仅在启动脚本时编译；修改 Go 后，按 Ctrl+C 完整退出，再执行同一条命令。现有后端参数和环境变量继续有效。开发模式使用指定的 `-data` 工作空间，未指定时仍使用默认数据库。

通过域名调试时，反向代理的上游须指向 **Vite 前端端口**，并支持 WebSocket；`-allowed-host` 本身不会更改反向代理。开发参数、HTTPS 示例和退出机制见[开发脚本](scripts/README.md#开发模式)。

使用 Go 1.27.1、Node.js 24+、npm 11+；Go 要求以 `go.mod` 为准，前端依赖以锁文件为准。检查 SQL 生成结果还需要 sqlc 1.31.x。

```bash
./scripts/build.sh
./workbench
```

默认访问 `http://127.0.0.1:8080`，数据库保存在 `~/.workbench/data.db`。可通过 `-listen` 和 `-data` 修改地址和工作空间。

默认 local 模式仅允许本机监听。反向代理部署使用 `-auth password -public-url https://实际域名`，首次启动须设置 `WORKBENCH_PASSWORD`；应用自动允许外部地址的 Host，并为浏览器启用 Secure Cookie。监听、数据路径、认证与外部地址均支持环境变量。详细参数和前端开发方式见[配置与运行](doc/configuration.md)。

## 日常使用

- 总览支持卡片显隐、排序、尺寸和恢复默认。
- 待办支持创建、完成、删除、分页和到期提醒；Wallos 在待办模块设置中连接。
- 投资连接 Schwab 后提供 TradingView 终端，查看真实行情、持仓和订单，并支持下单、改单和撤单。
- 投资价格监控每分钟检查美股/ETF 常规时段涨跌阈值，保存站内提醒，可通过 Telegram 推送；关闭网页仍由服务器执行。
- AI 面板提供对话和已注册的业务工具；未启用 AI 不影响普通业务操作。
- 设置提供业务模块、AI 配置、通知推送、账户安全、外观、数据与运行六个标签；TG 配置见 [Telegram 通知](doc/notifications.md)。
- 数据备份保存到工作空间的 `backups/` 目录，包含业务内容与保存的凭据。

## 验证

```bash
./scripts/test.sh
```

脚本检查 sqlc 生成漂移、Go 测试、前端 lint 和生产构建。工具路径覆盖、浏览器回归和临时工作空间运行说明见 [scripts/README](scripts/README.md)。
