# Workbench

Workbench 是本地优先、单用户的个人工作台，采用 Go 模块化单体、React 前端和 SQLite 工作空间。前端资源嵌入可执行文件，平台统一提供鉴权、事件、调度、AI、通知、总览和设置。

当前业务包括待办及 Wallos 订阅提醒、投资（Schwab 行情、账户和 TradingView 终端）。业务可在设置中启停，数据和总览布局偏好会保留。新增业务按模块扩展，前后端使用对应的模块目录。

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

推送版本标签后，只有 **Release 工作流全部成功** 才会创建 Releases 条目并附上二进制与部署包；Git 标签本身不等于 GitHub Release。

| 触发方式 | 工作流 | 产物位置 |
|---|---|---|
| 推送 `main` | **CI and Build** | 验证通过后，在该次运行的 **Artifacts** 下载 Linux amd64/arm64 二进制包、Docker 镜像文件和部署文件 |
| Actions → **CI and Build** → **Run workflow**，选择分支 | **CI and Build** | 与主分支构建相同，可手动打包，无需先创建版本标签 |
| 提交 PR | **CI and Build** | 运行检查、双架构镜像构建及部署验证 |
| 推送 `v0.1.1` 等版本标签 | **Release** | 二进制包与校验文件发布至 **Releases**，多架构镜像推送至 **GHCR** |

普通分支构建的 Artifacts 保留 14 天，名称为 `workbench-linux-<架构>-<提交号>`。下载并解开 GitHub 的 artifact ZIP 后，其中包含可直接解压的二进制 `.tar.gz`、可用 `docker load` 导入的 `*_docker.tar.gz`、部署文件包和 `SHA256SUMS`。

```bash
sha256sum --check SHA256SUMS
docker load --input workbench_<构建版本>_linux_amd64_docker.tar.gz
```

导入后的镜像名为 `workbench:sha-<完整提交号>`，可在 `.env` 中设置 `WORKBENCH_IMAGE=workbench`、`WORKBENCH_VERSION=sha-<完整提交号>` 后使用 Compose。二进制包解压后运行其中的 `workbench`。普通构建不会创建正式 Release 或推送 GHCR。

正式发布时，在包含工作流的提交上创建并推送版本标签：

```bash
git tag v0.1.1
git push origin v0.1.1
```

**Release** 会复用检查、编译并验证两种架构的二进制、构建和推送 Docker 镜像，最后创建 GitHub Release。稳定版本更新镜像 `latest`；`v0.1.1-rc.1` 等预发布版本独立标记。失败的检查会阻止后续构建或发布，可在 Actions 查看对应步骤日志。工作流入口：[CI and Build](.github/workflows/ci.yml)、[Release](.github/workflows/release.yml)。

## 文档

[文档入口](doc/README.md)汇总当前实现的设计和使用说明：

- [架构与目录](doc/architecture.md)
- [业务开发指南](doc/business-development.md)
- [接口与契约](doc/contracts.md)
- [数据设计](doc/data-model.md)
- [部署与发布](doc/deployment.md)：二进制、Docker Compose、反向代理与 GitHub Actions
- [配置与运行](doc/configuration.md)
- [安全边界](doc/security.md)
- [Todo](doc/modules/todo.md)、[Wallos 联动](doc/modules/todo-wallos.md)、[Investment](doc/modules/investment.md)

文档以当前代码、迁移和测试为依据。后续开发按实际需求推进，现状说明不构成固定功能范围或路线图；变更实现时同步更新相关文档。

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
