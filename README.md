# Workbench

Workbench 是本地优先、单用户的个人工作台，采用 Go 模块化单体、React 前端和 SQLite 工作空间。前端资源嵌入可执行文件，平台统一提供鉴权、事件、调度、AI、通知、总览和设置。

当前业务包括待办及 Wallos 订阅提醒、投资（Schwab 行情、账户和 TradingView 终端）。业务可在设置中启停，数据和总览布局偏好会保留。新增业务按模块扩展，前后端使用对应的模块目录。

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

## 部署

从 [GitHub Releases](https://github.com/amaoworks/WorkBench/releases) 获取 Linux amd64/arm64 二进制包或部署文件包。二进制解压后直接运行 `./workbench`；前端、数据库迁移与时区数据均已嵌入。

Docker Compose 使用 GHCR 的对应版本镜像。准备部署文件后：

```bash
cp .env.example .env
# 填写已发布的版本、外部 HTTPS 地址和首次登录密码
chmod 600 .env
docker compose pull
docker compose up -d --wait
```

应用只提供 HTTP 服务，HTTPS 由反向代理负责。Compose 默认仅发布到宿主机 `127.0.0.1:8080`；容器内的数据保存在 `/data` 持久化卷。完整安装、systemd、Nginx/Caddy、容器代理网络及升级恢复步骤见[部署与发布](doc/deployment.md)。

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
- AI 面板提供对话和已注册的业务工具；未启用 AI 不影响普通业务操作。
- 设置提供业务模块、AI 配置、账户安全、外观、数据与运行五个标签。
- 数据备份保存到工作空间的 `backups/` 目录，包含业务内容与保存的凭据。

## 验证

```bash
./scripts/test.sh
```

脚本检查 sqlc 生成漂移、Go 测试、前端 lint 和生产构建。工具路径覆盖、浏览器回归和临时工作空间运行说明见 [scripts/README](scripts/README.md)。
