# Shared capabilities

| 目录 | 职责 |
|---|---|
| `ai/` | Provider、模型 profile、AITool runtime |
| `conversation/` | 渠道无关的站内对话用例 |
| `dashboard/` | Widget 聚合与 Dashboard API |
| `notifications/` | 通知持久化、未读状态与 SSE |
| `scheduler/` | Job 注册、调度、执行历史与恢复 |

通用能力通过窄接口提供给模块，不向模块暴露底层实现。

