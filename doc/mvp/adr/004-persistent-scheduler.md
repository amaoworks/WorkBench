# ADR-004：持久化 Scheduler 语义

- 状态：Accepted
- 日期：2026-09-04

## 决策

调度器采用 `go-co-op/gocron/v2`，统一支持 cron、interval 和 one-time。任务配置与运行历史写入 SQLite，并明确时区、超时、重入、misfire 和重试语义。

## 理由

`cron_expr + last_run_at` 无法描述一次性任务、间隔任务和故障恢复。统一调度还能避免模块各自启动 ticker。

## 后果

模块只能注册 Job Handler，不能创建私有长期 goroutine。数据库保存定义状态，编译期注册表保存可执行函数，两者必须按稳定 Job ID 对应。

