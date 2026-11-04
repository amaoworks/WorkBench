-- +goose Up
ALTER TABLE events_log ADD COLUMN user_id TEXT NOT NULL DEFAULT '';
UPDATE events_log SET user_id = 'owner';
CREATE INDEX idx_events_user_time ON events_log(user_id, occurred_at);

CREATE TABLE notifications_scoped (
    id TEXT PRIMARY KEY,
    recipient_id TEXT NOT NULL DEFAULT 'owner',
    source_module TEXT NOT NULL,
    external INTEGER NOT NULL DEFAULT 0 CHECK (external IN (0, 1)),
    severity TEXT NOT NULL CHECK (severity IN ('info', 'success', 'warning', 'error')),
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    action_label TEXT,
    action_route TEXT,
    idempotency_key TEXT NOT NULL,
    source_event_id TEXT REFERENCES events_log(id),
    created_at INTEGER NOT NULL,
    read_at INTEGER,
    archived_at INTEGER,
    expires_at INTEGER,
    UNIQUE(recipient_id, idempotency_key)
);
INSERT INTO notifications_scoped(id, recipient_id, source_module, external, severity, title, content, action_label, action_route, idempotency_key, source_event_id, created_at, read_at, archived_at, expires_at)
SELECT id, 'owner', source_module, external, severity, title, content, action_label, action_route, idempotency_key, source_event_id, created_at, read_at, archived_at, expires_at FROM notifications;
DROP TABLE notifications;
ALTER TABLE notifications_scoped RENAME TO notifications;
CREATE INDEX idx_notifications_unread ON notifications(recipient_id, created_at DESC) WHERE read_at IS NULL AND archived_at IS NULL;
CREATE INDEX idx_notifications_active ON notifications(recipient_id, created_at DESC) WHERE archived_at IS NULL;

CREATE TABLE telegram_recipients (
    user_id TEXT PRIMARY KEY,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    chat_id TEXT NOT NULL,
    activated_at INTEGER NOT NULL,
    bot_generation INTEGER NOT NULL,
    status_json TEXT NOT NULL DEFAULT '{}'
);
INSERT INTO telegram_recipients(user_id, enabled, chat_id, activated_at, bot_generation, status_json)
SELECT 'owner', COALESCE(json_extract(value, '$.enabled'), 0), COALESCE(json_extract(value, '$.chatId'), ''), COALESCE(json_extract(value, '$.activatedAt'), 0), 0,
       COALESCE((SELECT value FROM workspace_settings WHERE section = 'telegram_status'), '{}')
FROM workspace_settings WHERE section = 'telegram';
DELETE FROM workspace_settings WHERE section = 'telegram_status';
UPDATE workspace_settings SET value = json_remove(value, '$.chatId') WHERE section = 'telegram';

-- +goose Down
CREATE TABLE notification_rollback_guard (other_users INTEGER NOT NULL CHECK (other_users = 0));
INSERT INTO notification_rollback_guard SELECT
    (SELECT COUNT(*) FROM auth_users WHERE id <> 'owner') +
    (SELECT COUNT(*) FROM notifications WHERE recipient_id <> 'owner') +
    (SELECT COUNT(*) FROM telegram_recipients WHERE user_id <> 'owner') +
    (SELECT COUNT(*) FROM events_log WHERE user_id NOT IN ('', 'owner'));
DROP TABLE notification_rollback_guard;
UPDATE workspace_settings SET value = json_set(value,
    '$.chatId', COALESCE((SELECT chat_id FROM telegram_recipients WHERE user_id = 'owner'), ''),
    '$.enabled', json(CASE WHEN json_extract(value, '$.enabled') = 1 AND
        COALESCE((SELECT enabled FROM telegram_recipients WHERE user_id = 'owner'), 0) = 1
        THEN 'true' ELSE 'false' END)) WHERE section = 'telegram';
INSERT INTO workspace_settings(section, value)
SELECT 'telegram_status', status_json FROM telegram_recipients WHERE user_id = 'owner'
ON CONFLICT(section) DO UPDATE SET value = excluded.value;
DROP TABLE telegram_recipients;
DROP INDEX idx_events_user_time;
ALTER TABLE events_log DROP COLUMN user_id;
CREATE TABLE notifications_unscoped (
    id TEXT PRIMARY KEY,
    source_module TEXT NOT NULL,
    external INTEGER NOT NULL DEFAULT 0 CHECK (external IN (0, 1)),
    severity TEXT NOT NULL CHECK (severity IN ('info', 'success', 'warning', 'error')),
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    action_label TEXT,
    action_route TEXT,
    idempotency_key TEXT NOT NULL UNIQUE,
    source_event_id TEXT REFERENCES events_log(id),
    created_at INTEGER NOT NULL,
    read_at INTEGER,
    archived_at INTEGER,
    expires_at INTEGER
);
INSERT INTO notifications_unscoped(id, source_module, external, severity, title, content, action_label, action_route, idempotency_key, source_event_id, created_at, read_at, archived_at, expires_at)
SELECT id, source_module, external, severity, title, content, action_label, action_route, idempotency_key, source_event_id, created_at, read_at, archived_at, expires_at FROM notifications;
DROP TABLE notifications;
ALTER TABLE notifications_unscoped RENAME TO notifications;
CREATE INDEX idx_notifications_unread ON notifications(created_at DESC) WHERE read_at IS NULL AND archived_at IS NULL;
CREATE INDEX idx_notifications_active ON notifications(created_at DESC) WHERE archived_at IS NULL;
