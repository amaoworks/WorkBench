# ADR-008：SQLite 数据栈与备份

- 状态：Accepted
- 日期：2026-09-04

## 决策

采用 Go 1.27.x、`modernc.org/sqlite v1.58.x`、`database/sql + sqlc` 和 Goose。数据库使用 WAL、`synchronous=FULL`、最多 4 个连接和进程文件锁。一致性备份使用 `VACUUM INTO` 或 backup API，并执行 `integrity_check`。

## 理由

纯 Go SQLite 便于交叉编译；sqlc 保持 SQL 可见且提供类型安全；版本化 migration 支持可靠升级。在 WAL 模式运行时只复制主数据库文件不能保证一致性。

## 后果

必须测试旧库升级、锁竞争、busy timeout、备份和恢复。具体依赖 patch 版本可在兼容范围内升级，但升级需经过回归测试。

