# 数据设计

实际表结构以 [平台迁移](../internal/foundation/database/migrations)、[Todo 迁移](../internal/modules/todo/migrations) 和 [Investment 迁移](../internal/modules/investment/migrations) 为准。本文说明归属和运行语义，不重复维护另一份建表 SQL。

## 工作空间

平台和业务共用一个 SQLite 数据库，由 [foundation/database](../internal/foundation/database/database.go) 统一打开、迁移、备份和关闭。业务接收同一个 `*sql.DB`，分别管理自己的表。

连接启用外键、WAL、`synchronous=FULL`、默认 5 秒 busy timeout 和 immediate 写事务。应用配置 4 个最大连接，使用 `<数据库路径>.lock` 限制同一数据库被多个工作台进程同时打开。

```text
工作空间目录/
  data.db                     主数据库（可通过 -data 改名或改路径）
  data.db-wal                 SQLite 运行时文件
  data.db-shm                 SQLite 运行时文件
  data.db.lock                进程锁文件
  backups/                    在线一致性备份
```

二进制和 Docker 使用相同的工作空间格式。Compose 将整个 `/data` 持久化到命名卷，包含运行文件和备份；容器使用 UID/GID `65532:65532`。目录挂载、迁移和权限步骤见[部署与发布](deployment.md)。升级会执行迁移，回退旧版本时可能需要一并恢复升级前备份。

业务表隔离由代码组织、契约和审查保证，共享连接不提供数据库级模块权限沙箱。

## 表归属

| 归属 | 表 | 保存内容 |
|---|---|---|
| 平台模块注册 | `modules` | 模块版本、契约版本、启用状态和时间；含平台 `core` 记录 |
| 可靠事件 | `events_log` | 领域事件、JSON 载荷、可投递时间 |
| 可靠事件 | `event_deliveries` | 事件/消费者唯一记录、租约、尝试次数和结果 |
| 鉴权 | `auth_credentials`、`sessions` | 单用户密码哈希、会话数据与到期时间 |
| 调度 | `scheduled_jobs`、`scheduled_job_runs` | 代码定义的持久化状态、执行次数、结果和错误 |
| 通知 | `notifications` | 提醒内容、幂等键、读取/归档/过期状态和来源 |
| 对话 | `ai_conversations`、`ai_messages` | 对话元数据和消息 |
| 工具运行时 | `ai_tool_calls` | 工具执行、参数审计、幂等键和结果 |
| 工作空间设置 | `workspace_settings` | 按 section 存储 JSON，目前有 `ai`、`appearance`、`dashboard`、`logging`；日志仅存最低等级，日志内容写入 stderr |
| Todo | `todo_tasks` | 标题、说明、提醒时间、完成状态和创建/更新时间 |
| Todo / Wallos | `todo_wallos_settings` | 联动配置、密钥、最近成功同步和错误 |
| Todo / Wallos | `todo_wallos_occurrences` | 外部来源、订阅 ID、付款日期与待办映射 |
| Investment | `investment_schwab` | App Key/Secret、回调、访问/刷新令牌、streamer 缓存和 OAuth state |

平台设置可供通用能力使用。例如总览布局由 Dashboard 能力管理，但保存在平台 `workspace_settings`；业务配置使用模块自己的表。

## 迁移和查询

平台使用 `goose_core_version`；每个业务使用 `goose_module_<id>_version`，独立记录迁移版本。启动先执行平台迁移，再执行所有已编译模块的迁移，停用模块也保留迁移和数据。

迁移文件随归属包保存并由本包嵌入。业务已有数据库升级应添加后续迁移，避免改写已执行迁移的含义。模块迁移在注册目录构建前执行，注册失败不自动回滚先前迁移。

[sqlc.yaml](../sqlc.yaml) 为平台、Todo、Investment 分别配置 schema、query 和生成目录。固定查询集中在各自 `query/`，生成代码在 `sqlc/`，提交到仓库。动态 IN、SQLite PRAGMA 及当前设置等少量 SQL 直接在实现中执行。测试脚本比较重新生成前后的所有 sqlc 目录，检测漂移。

## 事务和幂等

- Todo 写入、Wallos 待办映射、行情变更与对应领域事件使用同一事务。外部请求在写事务外完成。
- 事件投递以 `(event_id, consumer_id)` 唯一，领取后写入租约；失败重试，次数耗尽为 `dead`。处理中断可能再次投递，消费者须幂等。
- 通知的 `idempotency_key` 全局唯一；通过通知服务创建时连同通知变更事件一起提交。
- Job 执行记录以 `(job_id, scheduled_at, attempt)` 唯一，定义由代码装配；数据库保存恢复和审计状态。
- AI 写工具以工具名和幂等键查重。运行时审计与业务 Handler 不共用一个原子事务，不能视为任意副作用的严格一次执行保证。
- Wallos 以 `(source, subscription_id, payment_date)` 去重，其中 source 是配置地址的哈希。任务被删除后允许下次同步重建；完成状态会保留。
- Investment 按品种保存最新行情，同一时间或更早的快照不会再次覆盖并发布事件。

时间通常以 UTC Unix 毫秒持久化，HTTP 中的业务时间使用 RFC3339。实体 ID 使用 `foundation/identity`；业务自然键、模块 ID 和资源 ID 沿用各自语义。Wallos 的付款日期保留远端日历日期，并按配置时区计算提醒时间。

## 维护和备份

`core.maintenance` 每 24 小时清理到期 Session，以及创建时间超过 90 天且已读或已归档的通知。当前没有自动清理全部事件、Job 历史、AI 对话或备份文件的通用策略。

在线备份通过 `VACUUM INTO` 创建新数据库，再使用只读连接执行 `PRAGMA integrity_check`；成功后设置文件权限。备份文件保存在数据库同目录的 `backups/`。操作入口和恢复方式见[配置与运行](configuration.md)。

Investment 的 `00003_remove_demo.sql` 移除历史模拟行情和模拟摘要表；已登记的迁移保留，以支持已有工作空间升级。Schwab 凭据不受此迁移影响。
