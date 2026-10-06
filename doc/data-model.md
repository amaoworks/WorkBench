# 数据设计

实际表结构以平台和业务包内的 `migrations/`、`migrations-postgres/` 为准；例如[平台 SQLite 迁移](../internal/foundation/database/migrations)与[平台 PostgreSQL 迁移](../internal/foundation/database/migrations-postgres)。本文说明归属和运行语义，不重复维护另一份建表 SQL。

## 工作空间

平台和业务共用一个数据库，由 [foundation/database](../internal/foundation/database/database.go) 统一打开、迁移和关闭。原生运行默认 SQLite，Docker Compose 默认 PostgreSQL。业务接收同一个 `*sql.DB`，分别管理自己的表；业务连接池最多 4 个连接。

SQLite 启用外键、WAL、`synchronous=FULL`、默认 5 秒 busy timeout 和 immediate 写事务，并使用 `<数据库路径>.lock` 限制同一数据库被多个工作台进程同时打开。

PostgreSQL 另外保留一个连接会话，以 advisory lock 限制同一数据库和 schema 只运行一个工作台进程。数据库操作和事务提交前检查该会话；连接丢失后，当前实例停止数据库读写，健康检查返回未就绪，需重启实例。显式事务默认使用 Serializable 隔离级别；并发写入发生序列化冲突时，事务回滚并向调用方返回错误。

```text
工作空间目录/
  data.db                     SQLite 主数据库；PostgreSQL 部署时作为本地运行目录定位
  data.db-wal                 SQLite 运行时文件
  data.db-shm                 SQLite 运行时文件
  data.db.lock                进程锁文件
  backups/                    SQLite 在线一致性备份
```

二进制和 Docker 使用相同的本地工作空间目录。SQLite 在其中保存数据库、运行文件和备份；PostgreSQL 数据保存在外部数据库，本地目录仍保存图表缓存、OpenD 运行内容等文件。Compose 将整个 `/data` 持久化到命名卷，容器使用 UID/GID `65532:65532`。目录挂载、迁移和权限步骤见[部署与发布](deployment.md)。升级会执行所选方言的迁移，回退旧版本时可能需要一并恢复升级前备份。

业务表隔离由代码组织、契约和审查保证，共享连接不提供数据库级模块权限沙箱。

## 表归属

| 归属 | 表 | 保存内容 |
|---|---|---|
| 平台模块注册 | `modules` | 模块版本、契约版本、来源 `kind`（builtin/external）、启用意图和时间；含平台 `core` 记录 |
| 外部模块连接 | `external_modules` | 服务 origin、非本机许可、服务凭据、最后有效 manifest、控制代数、观察状态和脱敏错误 |
| 可靠事件 | `events_log` | 领域事件、JSON 载荷、可投递时间 |
| 可靠事件 | `event_deliveries` | 事件/消费者唯一记录、租约、尝试次数和结果 |
| 鉴权 | `auth_credentials`、`sessions` | 单用户密码哈希、凭据版本、首次改密状态、会话数据与到期时间 |
| 调度 | `scheduled_jobs`、`scheduled_job_runs` | 代码定义的持久化状态、执行次数、结果和错误 |
| 通知 | `notifications` | 提醒内容、幂等键、读取/归档/过期状态、来源和显式外发标志 |
| 对话 | `ai_conversations`、`ai_messages` | 对话元数据和消息 |
| 工具运行时 | `ai_tool_calls` | 工具执行、参数审计、幂等键和结果 |
| 工作空间设置 | `workspace_settings` | 按 section 存储 JSON：`ai`、`appearance`、`dashboard`、`logging`、`telegram`、`telegram_status`；日志内容写入 stderr |
| Todo | `todo_tasks` | 标题、说明、提醒时间、完成状态和创建/更新时间 |
| Todo / Wallos | `todo_wallos_settings` | 联动配置、密钥、最近成功同步和错误 |
| Todo / Wallos | `todo_wallos_occurrences` | 外部来源、订阅 ID、付款日期与待办映射 |
| Investment | `investment_schwab` | App Key/Secret、回调、访问/刷新令牌、streamer 缓存和 OAuth state |
| Investment | `investment_futu` | 富途账号、登录密码 MD5 摘要、富途与夜盘开关和最近错误；历史地址及许可字段不再控制连接，连接由部署配置管理 |
| Investment | `investment_futu_bars` | 夜盘 K 线本地缓存（避免重复消耗富途历史额度） |
| Investment | `investment_price_rules` | 标的、阈值、启停、规则版本及最近有效报价/错误 |
| Investment | `investment_price_triggers` | `(rule_id, trading_date)` 唯一的触发快照、站内通知 ID；删除规则后保留 |
| Investment | `investment_price_monitor` | 最近后台检查时间、状态和错误 |
| Investment | `investment_watchlists` | 自选列表、分组和顺序、当前列表，以及防止迟到请求和旧窗口覆盖的编辑版本/会话/序号 |
| Investment | `investment_preferences` | 图表选择、完整图表与指标/画线、TradingView 偏好、当前交易账户、页面视图，以及各项独立的编辑版本/会话/序号 |
| News | `news_sources`、`news_origins` | 来源配置、条件请求与退避状态、条目身份和出处 |
| News | `news_articles`、`news_rules` | 文章、阅读状态、匹配规则、翻译摘要及缓存 |
| News | `news_settings`、`news_digests`、`news_ai_usage` | 处理与简报配置、简报快照和通知 ID、每日调用额度 |

平台设置可供通用能力使用。例如总览布局由 Dashboard 能力管理，但保存在平台 `workspace_settings`；业务配置使用模块自己的表。

## 迁移和查询

平台使用 `goose_core_version`；每个业务使用 `goose_module_<id>_version`，独立记录迁移版本。启动先执行平台迁移，再执行所有已编译模块的迁移，停用模块也保留迁移和数据。应用按数据库驱动选择 `migrations/` 或 `migrations-postgres/`。

两个方言的迁移文件随归属包保存在同一个嵌入文件系统中，使用相同版本号表达相同结构。PostgreSQL DDL 使用 `BIGINT` 保持 Go `int64` 和 Unix 毫秒语义，二进制与浮点列分别使用 `BYTEA` 和 `DOUBLE PRECISION`，JSON 继续以 `TEXT` 保存并在约束中校验。业务已有数据库升级应为两个方言添加后续迁移，避免改写已执行迁移的含义。模块迁移在注册目录构建前执行，注册失败不自动回滚先前迁移。

[sqlc.yaml](../sqlc.yaml) 使用 SQLite engine 为平台、Todo、Investment 分别配置 schema、query 和生成目录。固定查询集中在各自 `query/`，生成代码在 `sqlc/`，由两种数据库共用；PostgreSQL 驱动在执行和预处理时将 `?`、`?N` 占位符转换为 `$N`。动态 IN、SQLite PRAGMA 及当前设置等少量方言 SQL 直接在实现中执行。测试脚本比较重新生成前后的所有 sqlc 目录，检测漂移。

## 事务和幂等

- Todo 写入、Wallos 待办映射与对应领域事件使用同一 `*sql.Tx`。投资触发快照、站内通知与通知创建事件也原子提交。外部请求在写事务外完成。PostgreSQL 显式事务默认使用 Serializable，冲突时整体回滚。
- 事件投递以 `(event_id, consumer_id)` 唯一，领取后写入租约；失败重试，次数耗尽为 `dead`。处理中断可能再次投递，消费者须幂等。
- 通知的 `idempotency_key` 全局唯一；通过通知服务创建时连同通知变更事件一起提交。
- Job 执行记录以 `(job_id, scheduled_at, attempt)` 唯一，定义由代码装配；数据库保存恢复和审计状态。
- AI 写工具以工具名和幂等键查重。运行时审计与业务 Handler 不共用一个原子事务，不能视为任意副作用的严格一次执行保证。
- Wallos 以 `(source, subscription_id, payment_date)` 去重，其中 source 是配置地址的哈希。任务被删除后允许下次同步重建；完成状态会保留。
- Investment 按规则和美东交易日去重；规则版本校验阻止在 HTTP 查询期间被修改、暂停或删除的规则产生迟到提醒。

时间通常以 UTC Unix 毫秒持久化，HTTP 中的业务时间使用 RFC3339。实体 ID 使用 `foundation/identity`；业务自然键、模块 ID 和资源 ID 沿用各自语义。Wallos 的付款日期保留远端日历日期，并按配置时区计算提醒时间。

## 维护和备份

`core.maintenance` 每 24 小时清理到期 Session，以及创建时间超过 90 天且已读或已归档的通知。当前没有自动清理全部事件、Job 历史、AI 对话或备份文件的通用策略。

SQLite 在线备份通过 `VACUUM INTO` 创建新数据库，再使用只读连接执行 `PRAGMA integrity_check`；成功后设置文件权限。备份文件保存在数据库同目录的 `backups/`。PostgreSQL 备份由数据库服务使用 `pg_dump` 等外部工具管理，应用的备份接口返回 HTTP 501。操作入口和恢复方式见[配置与运行](configuration.md)。核心备份包含外部模块的宿主注册数据，不表示已经备份远端业务数据。

外部服务凭据保存在 `external_modules.service_token`，由 SQLite 文件权限或 PostgreSQL 服务访问控制保护；API 列表、配置读回、日志和浏览器 URL 都不得出现该值。数据库内容未由应用另行加密，拥有数据库读取权限的主体仍能读取凭据。宿主重启后持久化的健康状态只作历史，重新确认前不开放业务代理。启动同步内置模块时不会覆盖 `kind=external` 的行。

Investment 的 `00003_remove_demo.sql` 移除历史模拟行情和模拟摘要表，`00004_cleanup_demo.sql` 清理模拟提醒、行情事件及旧调度记录。已登记的迁移保留用于旧工作空间升级；回归测试同时验证相关事件投递被清理，Schwab 凭据及其他业务通知保留。运行时不包含模拟行情功能。
