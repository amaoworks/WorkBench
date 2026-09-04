# ADR-003：SQLite Transactional Outbox

- 状态：Accepted
- 日期：2026-09-04

## 决策

可靠事件使用 SQLite Transactional Outbox。事件交付为异步、至少一次；使用稳定 Consumer ID、幂等 Handler 和 `events_log + event_deliveries` 持久化状态。

## 理由

纯内存 Pub/Sub 在崩溃和重启时丢事件，也无法保证业务写入与事件发布一致。单机 SQLite 足以实现当前所需的可靠性，无需 Kafka/NATS。

## 后果

Consumer 必须容忍重复投递。系统需要 dispatcher、claim lease、重试、dead delivery、保留与人工恢复机制。Go channel 只能作为唤醒优化。

