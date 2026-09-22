# 通用能力层

| 目录 | 当前职责 |
|---|---|
| `ai/` | Provider、文本生成、Gateway、AI Tool runtime 与审计 |
| `conversation/` | 对话持久化、普通与流式 HTTP 对话；组合 AI Gateway |
| `dashboard/` | Widget 聚合、完整目录、布局保存与恢复 |
| `notifications/` | 通知持久化、已读/归档、未读数、SSE、Telegram 设置与投递 |
| `scheduler/` | Job 注册、持久化状态、执行记录、重试与恢复 |

能力由 `internal/app` 构造，业务使用 `internal/contracts` 的接口和注册描述，不直接导入能力实现。能力层按职责使用底座，必要时组合其他能力。

接口语义见[接口与契约](../../doc/contracts.md)，数据归属见[数据设计](../../doc/data-model.md)。
