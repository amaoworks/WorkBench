# 基础设施层

| 目录 | 当前职责 |
|---|---|
| `auth/` | local/password 模式、密码与 Session、Host/Origin/CSRF |
| `database/` | SQLite 生命周期、迁移、备份、文件锁和平台 SQL |
| `events/` | 事务事件存储、消费者投递、租约与重试 |
| `modules/` | 注册资源目录、持久化启停、HTTP 门控 |
| `httpapi/` | JSON 请求体校验、响应与错误 |
| `identity/` | 实体 ID 生成 |
| `logging/` | JSON 默认输出、等级校验和共享的并发安全动态等级过滤 |

底座不依赖通用能力或具体业务实现。业务通过注入使用数据连接和事件契约，也可以调用公开 HTTP/ID 工具。模块业务 SQL 和迁移保留在业务目录；本目录承载平台数据。

详见[架构](../../doc/architecture.md)、[数据设计](../../doc/data-model.md)和[安全边界](../../doc/security.md)。
