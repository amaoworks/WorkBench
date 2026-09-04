# Architecture Decision Records

| ADR | 决策 | 状态 |
|---|---|---|
| [001](001-go-modular-monolith.md) | Go 模块化单体 | Accepted |
| [002](002-http-and-module-contract.md) | chi 与受控模块注册 | Accepted |
| [003](003-transactional-outbox.md) | SQLite Transactional Outbox | Accepted |
| [004](004-persistent-scheduler.md) | gocron/v2 持久调度语义 | Accepted |
| [005](005-frontend-module-registry.md) | 前端编译期模块注册表 | Accepted |
| [006](006-single-workspace-database.md) | 删除 owner_id | Accepted |
| [007](007-authentication-modes.md) | local/password 鉴权模式 | Accepted |
| [008](008-sqlite-stack.md) | SQLite 数据栈与备份策略 | Accepted |
| [009](009-ai-provider.md) | AI Provider 与 OpenAI MVP | Accepted |
| [010](010-notification-center.md) | 持久通知中心与 SSE | Accepted |

新决策使用下一连续编号。对已接受 ADR 的修改通过新 ADR 替代（Supersede），不删除历史记录。

