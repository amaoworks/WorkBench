# MVP 数据库设计

这里给出逻辑 schema 基线。正式实现必须通过 Goose migration 落地，并由 sqlc 查询和集成测试校验；不要在运行时用 `CREATE TABLE IF NOT EXISTS` 代替版本化 migration。

## 通用约定

- ID 使用应用生成的 128-bit、32 位小写十六进制、按毫秒近似可排序的文本值；其外部语义保持 opaque。
- 时间存储为 Unix 毫秒 UTC，列名以 `_at` 结尾。
- JSON 列以 `_json` 结尾，并在应用边界执行 schema 校验。
- 一个数据库文件就是一个用户工作空间，不设置 `owner_id`。
- 模块表以 `<module>_` 为前缀。

## Foundation 表

```sql
CREATE TABLE modules (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    version          TEXT NOT NULL,
    contract_version INTEGER NOT NULL,
    enabled          INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    installed_at     INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

-- migration 同时写入始终启用的 `core` 模块行，供核心 Job 外键引用。

CREATE TABLE events_log (
    id               TEXT PRIMARY KEY,
    topic            TEXT NOT NULL,
    schema_version   INTEGER NOT NULL,
    source_module    TEXT NOT NULL,
    aggregate_id     TEXT,
    payload_json     TEXT NOT NULL CHECK (json_valid(payload_json)),
    occurred_at      INTEGER NOT NULL,
    available_at     INTEGER NOT NULL,
    created_at       INTEGER NOT NULL
);

CREATE INDEX idx_events_dispatch
ON events_log(available_at, created_at);

CREATE INDEX idx_events_topic_time
ON events_log(topic, occurred_at DESC);

CREATE TABLE event_deliveries (
    event_id          TEXT NOT NULL REFERENCES events_log(id) ON DELETE CASCADE,
    consumer_id       TEXT NOT NULL,
    status            TEXT NOT NULL CHECK (
        status IN ('pending', 'running', 'succeeded', 'retry', 'dead')
    ),
    attempts          INTEGER NOT NULL DEFAULT 0,
    next_attempt_at   INTEGER,
    locked_until      INTEGER,
    last_error        TEXT,
    started_at        INTEGER,
    completed_at      INTEGER,
    updated_at        INTEGER NOT NULL,
    PRIMARY KEY (event_id, consumer_id)
);

CREATE INDEX idx_event_deliveries_claim
ON event_deliveries(status, next_attempt_at, locked_until);
```

事件保留策略必须在实现前确定。清理事件时应同时清理由外键关联的 delivery；dead delivery 在人工处理前不得自动删除。

## 鉴权表

SCS SQLite Store 的最终列名以适配器测试为准，逻辑结构如下：

```sql
CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BLOB NOT NULL,
    expiry INTEGER NOT NULL
);

CREATE INDEX idx_sessions_expiry ON sessions(expiry);

CREATE TABLE auth_credentials (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    password_hash TEXT NOT NULL,
    updated_at    INTEGER NOT NULL
);
```

不得保存明文密码。切换到 `password` 模式但尚未设置凭据时，服务不得对非 loopback 提供业务接口。

## Scheduler 表

```sql
CREATE TABLE scheduled_jobs (
    id                  TEXT PRIMARY KEY,
    module              TEXT NOT NULL REFERENCES modules(id),
    schedule_kind       TEXT NOT NULL CHECK (
        schedule_kind IN ('cron', 'interval', 'once')
    ),
    schedule_expr       TEXT NOT NULL,
    timezone            TEXT NOT NULL,
    timeout_ms          INTEGER NOT NULL,
    overlap_policy      TEXT NOT NULL,
    misfire_policy      TEXT NOT NULL,
    max_attempts        INTEGER NOT NULL,
    enabled             INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    next_run_at         INTEGER,
    last_run_at         INTEGER,
    definition_hash     TEXT NOT NULL,
    updated_at          INTEGER NOT NULL
);

CREATE TABLE scheduled_job_runs (
    id              TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL REFERENCES scheduled_jobs(id) ON DELETE CASCADE,
    scheduled_at    INTEGER NOT NULL,
    started_at      INTEGER,
    finished_at     INTEGER,
    attempt         INTEGER NOT NULL,
    status          TEXT NOT NULL CHECK (
        status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'timed_out')
    ),
    error           TEXT,
    UNIQUE(job_id, scheduled_at, attempt)
);

CREATE INDEX idx_job_runs_job_time
ON scheduled_job_runs(job_id, scheduled_at DESC);
```

`definition_hash` 用于识别编译期 Job 定义变化。数据库中不存在的 Handler 不得执行。

## Notifications 表

```sql
CREATE TABLE notifications (
    id                TEXT PRIMARY KEY,
    source_module     TEXT NOT NULL,
    severity          TEXT NOT NULL CHECK (
        severity IN ('info', 'success', 'warning', 'error')
    ),
    title             TEXT NOT NULL,
    content           TEXT NOT NULL,
    action_label      TEXT,
    action_route      TEXT,
    idempotency_key   TEXT NOT NULL UNIQUE,
    source_event_id   TEXT REFERENCES events_log(id),
    created_at        INTEGER NOT NULL,
    read_at           INTEGER,
    archived_at       INTEGER,
    expires_at        INTEGER
);

CREATE INDEX idx_notifications_unread
ON notifications(created_at DESC)
WHERE read_at IS NULL AND archived_at IS NULL;

CREATE INDEX idx_notifications_active
ON notifications(created_at DESC)
WHERE archived_at IS NULL;
```

`action_route` 必须在写入前验证为允许的站内相对路径。已读或归档通知默认保留 90 天，未读通知不自动清理。

## AI 与对话表

这组表属于 AI capability，而不是 Foundation：

```sql
CREATE TABLE ai_conversations (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    archived_at INTEGER
);

CREATE TABLE ai_messages (
    id               TEXT PRIMARY KEY,
    conversation_id  TEXT NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
    role             TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
    content_json     TEXT NOT NULL CHECK (json_valid(content_json)),
    provider         TEXT,
    model            TEXT,
    created_at       INTEGER NOT NULL
);

CREATE INDEX idx_ai_messages_conversation
ON ai_messages(conversation_id, created_at);

CREATE INDEX idx_ai_conversations_updated
ON ai_conversations(updated_at DESC, id DESC);

CREATE TABLE ai_tool_calls (
    id                TEXT PRIMARY KEY,
    conversation_id   TEXT REFERENCES ai_conversations(id) ON DELETE SET NULL,
    message_id        TEXT REFERENCES ai_messages(id) ON DELETE SET NULL,
    tool_name         TEXT NOT NULL,
    module            TEXT NOT NULL,
    risk              TEXT NOT NULL,
    arguments_json    TEXT NOT NULL CHECK (json_valid(arguments_json)),
    idempotency_key   TEXT,
    confirmation_at   INTEGER,
    status            TEXT NOT NULL,
    result_json       TEXT CHECK (result_json IS NULL OR json_valid(result_json)),
    error             TEXT,
    created_at        INTEGER NOT NULL,
    completed_at      INTEGER
);

CREATE UNIQUE INDEX idx_ai_tool_calls_idempotency
ON ai_tool_calls(tool_name, idempotency_key)
WHERE idempotency_key IS NOT NULL;

CREATE INDEX idx_ai_tool_calls_status
ON ai_tool_calls(status, created_at DESC);
```

敏感 Tool 参数的审计策略需允许字段级脱敏，不能无条件把 API Key、密码或完整隐私内容写入 `arguments_json`。

## 模块 migration

Todo MVP migration：

```sql
CREATE TABLE todo_tasks (
    id           TEXT PRIMARY KEY,
    title        TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    due_at       INTEGER,
    completed_at INTEGER,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE INDEX idx_todo_tasks_due
ON todo_tasks(due_at)
WHERE completed_at IS NULL AND due_at IS NOT NULL;

CREATE INDEX idx_todo_tasks_updated
ON todo_tasks(updated_at DESC);
```

各 scope 使用独立 Goose 版本表：Foundation 为 `goose_core_version`，Todo 为 `goose_module_todo_version`。

建议编号空间：

```text
internal/foundation/database/migrations/ 应用级 Foundation migration
internal/modules/todo/migrations/         todo 模块 migration
internal/modules/investment/migrations/   investment 模块 migration
```

模块 migration 必须是单向兼容启停的：禁用模块不回滚表，重新启用后继续执行缺失 migration。发布前必须使用旧版本数据库副本验证升级路径。

## 总览配置与第二业务（2026-09-05）

总览布局保存在 `workspace_settings` 的 `section = 'dashboard'`，value 是 `[{id, visible, size, order}]` JSON 数组。配置读写通过 `query/dashboard.sql` 生成，不另增 core migration；缺失 section 表示使用注册默认值。

Investment 使用独立 Goose 表 `goose_module_investment_version`，migration 为 `internal/modules/investment/migrations/00001_investment.sql`：

- `investment_quotes(symbol PK, name, price_cents, change_bps, as_of)`：模拟行情；金额为整数分、百分比为整数基点、时间为 UTC Unix 毫秒。只接受比已有记录更新的快照，避免重复发布事件。
- `investment_summary(id PK CHECK id = 1, content, created_at)`：最近一次主动生成的 AI 摘要，生成失败不覆盖旧值。

首次升级会为已知 Investment 模块执行 migration，保留已有 Todo 数据、模块状态和总览偏好。备份包含这些表和 workspace_settings。模块停用只控制入口，不删除上述数据。
