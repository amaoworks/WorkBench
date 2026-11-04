-- +goose Up
ALTER TABLE events_log ADD COLUMN user_id TEXT NOT NULL DEFAULT '';
UPDATE events_log SET user_id = 'owner';
CREATE INDEX idx_events_user_time ON events_log(user_id, occurred_at);
ALTER TABLE notifications ADD COLUMN recipient_id TEXT NOT NULL DEFAULT 'owner';
ALTER TABLE notifications DROP CONSTRAINT notifications_idempotency_key_key;
ALTER TABLE notifications ADD CONSTRAINT notifications_recipient_idempotency_key UNIQUE(recipient_id, idempotency_key);
DROP INDEX idx_notifications_unread;
DROP INDEX idx_notifications_active;
CREATE INDEX idx_notifications_unread ON notifications(recipient_id, created_at DESC) WHERE read_at IS NULL AND archived_at IS NULL;
CREATE INDEX idx_notifications_active ON notifications(recipient_id, created_at DESC) WHERE archived_at IS NULL;
CREATE TABLE telegram_recipients (
    user_id TEXT PRIMARY KEY,
    enabled BIGINT NOT NULL CHECK (enabled IN (0, 1)),
    chat_id TEXT NOT NULL,
    activated_at BIGINT NOT NULL,
    bot_generation BIGINT NOT NULL,
    status_json TEXT NOT NULL DEFAULT '{}'
);
INSERT INTO telegram_recipients(user_id, enabled, chat_id, activated_at, bot_generation, status_json)
SELECT 'owner', CASE WHEN COALESCE(value::jsonb->>'enabled', 'false') = 'true' THEN 1 ELSE 0 END,
       COALESCE(value::jsonb->>'chatId', ''), COALESCE((value::jsonb->>'activatedAt')::BIGINT, 0), 0,
       COALESCE((SELECT value FROM workspace_settings WHERE section = 'telegram_status'), '{}')
FROM workspace_settings WHERE section = 'telegram';
DELETE FROM workspace_settings WHERE section = 'telegram_status';
UPDATE workspace_settings SET value = (value::jsonb - 'chatId')::text WHERE section = 'telegram';

-- +goose Down
CREATE TABLE notification_rollback_guard (other_users BIGINT NOT NULL CHECK (other_users = 0));
INSERT INTO notification_rollback_guard SELECT
    (SELECT COUNT(*) FROM auth_users WHERE id <> 'owner') +
    (SELECT COUNT(*) FROM notifications WHERE recipient_id <> 'owner') +
    (SELECT COUNT(*) FROM telegram_recipients WHERE user_id <> 'owner') +
    (SELECT COUNT(*) FROM events_log WHERE user_id NOT IN ('', 'owner'));
DROP TABLE notification_rollback_guard;
UPDATE workspace_settings SET value = (value::jsonb || jsonb_build_object(
    'chatId', COALESCE((SELECT chat_id FROM telegram_recipients WHERE user_id = 'owner'), ''),
    'enabled', COALESCE((value::jsonb->>'enabled')::BOOLEAN, FALSE) AND
        COALESCE((SELECT enabled FROM telegram_recipients WHERE user_id = 'owner'), 0) = 1))::text
WHERE section = 'telegram';
INSERT INTO workspace_settings(section, value)
SELECT 'telegram_status', status_json FROM telegram_recipients WHERE user_id = 'owner'
ON CONFLICT(section) DO UPDATE SET value = excluded.value;
DROP TABLE telegram_recipients;
DROP INDEX idx_events_user_time;
ALTER TABLE events_log DROP COLUMN user_id;
DROP INDEX idx_notifications_unread;
DROP INDEX idx_notifications_active;
ALTER TABLE notifications DROP CONSTRAINT notifications_recipient_idempotency_key;
ALTER TABLE notifications DROP COLUMN recipient_id;
ALTER TABLE notifications ADD CONSTRAINT notifications_idempotency_key_key UNIQUE(idempotency_key);
CREATE INDEX idx_notifications_unread ON notifications(created_at DESC) WHERE read_at IS NULL AND archived_at IS NULL;
CREATE INDEX idx_notifications_active ON notifications(created_at DESC) WHERE archived_at IS NULL;
