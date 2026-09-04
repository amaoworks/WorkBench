# ADR-010：持久通知中心与 SSE

- 状态：Accepted
- 日期：2026-09-04

## 决策

通知中心负责持久化、查询、未读、已读、归档和幂等创建。通知由 EventConsumer、ScheduledJob 或用户操作产生。MVP 不实现通用规则 DSL。页面打开时使用 SSE 实时提示，SQLite 始终是状态真相。

## 理由

通用规则引擎会过早引入条件、窗口、冷却、重放和领域查询 DSL。SSE 足以提供单站点实时体验，但不应被当作可靠消息队列。

## 后果

通知必须包含唯一 idempotency key。SSE 重连后客户端重新查询数据库。浏览器/OS/Web Push/IM 通知不属于 MVP，未来通过 Channel Adapter 增加并独立记录 delivery。

