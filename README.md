# Workbench

Workbench 是一个本地优先、单用户的个人工作台。项目采用 Go 模块化单体、React 前端和单文件 SQLite 数据库，目标部署形态是“一个可执行文件 + 一个数据库文件”。

MVP 已完成并通过最终验收：Foundation、通用能力、Todo 验证模块和 Web Shell 均已落地。开发环境需要 Go 1.27.x、Node.js 24+、npm 11+ 与 sqlc 1.31.x；只构建已检入的生成产物时不要求安装 sqlc。

- MVP 文档入口：[doc/mvp/README.md](doc/mvp/README.md)
- 总体架构：[doc/mvp/architecture-v1.2.md](doc/mvp/architecture-v1.2.md)
- 核心契约：[doc/mvp/contracts/core-contracts.md](doc/mvp/contracts/core-contracts.md)
- 数据设计：[doc/mvp/database/schema.md](doc/mvp/database/schema.md)
- 配置与运行：[doc/mvp/configuration.md](doc/mvp/configuration.md)
- 安全威胁模型：[doc/mvp/security-threat-model.md](doc/mvp/security-threat-model.md)
- 实施与验收清单：[doc/mvp/implementation-checklist.md](doc/mvp/implementation-checklist.md)

## 目录

```text
cmd/workbench/          可执行文件入口
internal/app/           依赖装配与应用生命周期
internal/contracts/     各层共享的稳定接口和描述类型
internal/foundation/    数据库、鉴权、事件、模块注册等底座
internal/capabilities/  AI、调度、通知、对话、总览等通用能力
internal/modules/       待办、投资等业务模块
internal/webui/         内嵌前端产物及其 HTTP Handler
web/                    React + TypeScript 前端
scripts/                开发、构建和发布脚本
doc/mvp/                MVP 架构与决策文档
```

## 构建与运行

```bash
GO_BIN=.tools/go/bin/go ./scripts/test.sh
GO_BIN=.tools/go/bin/go ./scripts/build.sh
./workbench
```

默认监听 `127.0.0.1:8080`，数据保存在 `~/.workbench/data.db`。首次启用密码模式时：

```bash
WORKBENCH_PASSWORD='请设置至少十二个字符的安全密码' ./workbench -auth password
```

非 loopback 监听必须同时提供 `-tls-cert` 与 `-tls-key`。AI 为可选能力，设置 `OPENAI_API_KEY` 后启用；模型可通过 `OPENAI_MODEL` 调整。

使用通配监听或域名访问时，以可重复的 `-allowed-host host:port`（或逗号分隔的 `WORKBENCH_ALLOWED_HOSTS`）明确允许浏览器发送的 Host；例如 `-listen 0.0.0.0:8443 -allowed-host workbench.lan:8443`。自定义兼容端点可通过受信任的启动环境变量 `OPENAI_BASE_URL` 设置。
