# Workbench MVP 文档集

状态：**核心架构决策已确认，可以进入接口与 migration 实现阶段**。

本文档集是原 v1.1 草案经逐项评审后的 v1.2 基线。若本文档与旧草案冲突，以这里及 ADR 为准。

## 阅读顺序

1. [总体架构](architecture-v1.2.md)
2. [核心 Go 契约](contracts/core-contracts.md)
3. [数据库设计](database/schema.md)
4. [项目目录与依赖边界](project-layout.md)
5. [实施与验收清单](implementation-checklist.md)
6. [ADR 索引](adr/README.md)

## MVP 边界

MVP 包含：

- SQLite 初始化、迁移、备份约定
- 编译期模块注册与运行时启停
- Transactional Outbox 事件投递
- 持久化 Scheduler 与运行历史
- local/password 两种鉴权模式
- 通知中心与页面打开期间的 SSE 更新
- OpenAI Provider、工具调用和站内对话
- 动态导航、Widget 描述与待办验证模块

MVP 不包含：

- 多用户、团队协作及租户隔离
- 动态加载 `.so`、WASM 或第三方二进制插件
- Redis、Kafka、NATS 或多实例部署
- 通用通知规则 DSL
- Web Push、系统通知或外部 IM 渠道
- 真实券商交易能力

## 决策治理

- 已确认决策通过 ADR 记录，不直接覆写其历史。
- 核心契约按版本演进；“冻结”表示冻结 MVP v1 语义，不代表永不修改。
- 破坏兼容性的事件、工具参数或前端描述必须提升对应 `schemaVersion`。
