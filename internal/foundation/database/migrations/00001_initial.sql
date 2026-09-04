-- +goose Up
CREATE TABLE modules (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    version          TEXT NOT NULL,
    contract_version INTEGER NOT NULL,
    enabled          INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    installed_at     INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

INSERT INTO modules(id, name, version, contract_version, enabled, installed_at, updated_at)
VALUES ('core', 'Workbench Core', '0.1.0', 1, 1, unixepoch('subsec') * 1000, unixepoch('subsec') * 1000);

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

CREATE INDEX idx_events_dispatch ON events_log(available_at, created_at);
CREATE INDEX idx_events_topic_time ON events_log(topic, occurred_at DESC);

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

-- +goose Down
DROP TABLE ai_tool_calls;
DROP TABLE ai_messages;
DROP TABLE ai_conversations;
DROP TABLE notifications;
DROP TABLE scheduled_job_runs;
DROP TABLE scheduled_jobs;
DROP TABLE auth_credentials;
DROP TABLE sessions;
DROP TABLE event_deliveries;
DROP TABLE events_log;
DROP TABLE modules;
