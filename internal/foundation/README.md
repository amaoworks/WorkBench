# Foundation

| 目录 | 职责 |
|---|---|
| `auth/` | local/password 模式、Session 与请求安全 |
| `database/` | SQLite 生命周期、migration、备份与文件锁 |
| `events/` | Transactional Outbox 与可靠事件投递 |
| `modules/` | Module Registry、原子注册与 enabled gate |

Foundation 不依赖具体业务模块。

