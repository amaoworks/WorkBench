# Workbench

Workbench 是一个本地优先、单用户的个人工作台。项目采用 Go 模块化单体、React 前端和单文件 SQLite 数据库，目标部署形态是“一个可执行文件 + 一个数据库文件”。

当前仓库处于 MVP 架构落盘阶段，尚未开始业务实现。

- MVP 文档入口：[doc/mvp/README.md](doc/mvp/README.md)
- 总体架构：[doc/mvp/architecture-v1.2.md](doc/mvp/architecture-v1.2.md)
- 核心契约：[doc/mvp/contracts/core-contracts.md](doc/mvp/contracts/core-contracts.md)
- 数据设计：[doc/mvp/database/schema.md](doc/mvp/database/schema.md)
- 开工清单：[doc/mvp/implementation-checklist.md](doc/mvp/implementation-checklist.md)

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
