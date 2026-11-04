-- name: InsertNotification :execrows
INSERT INTO notifications(
    id, recipient_id, source_module, external, severity, title, content, action_label,
    action_route, idempotency_key, source_event_id, created_at, expires_at
) VALUES (
    sqlc.arg(id), sqlc.arg(recipient_id), sqlc.arg(source_module), sqlc.arg(external), sqlc.arg(severity),
    sqlc.arg(title), sqlc.arg(content), NULLIF(sqlc.arg(action_label), ''), NULLIF(sqlc.arg(action_route), ''),
    sqlc.arg(idempotency_key), NULLIF(sqlc.arg(source_event_id), ''),
    sqlc.arg(created_at), sqlc.arg(expires_at)
)
ON CONFLICT(recipient_id, idempotency_key) DO NOTHING;

-- name: GetNotificationByIdempotencyKey :one
SELECT id, recipient_id, source_module, external, severity, title, content,
       COALESCE(action_label, '') AS action_label,
       COALESCE(action_route, '') AS action_route,
       idempotency_key, COALESCE(source_event_id, '') AS source_event_id,
       created_at, read_at, archived_at, expires_at
FROM notifications WHERE idempotency_key = sqlc.arg(idempotency_key) AND recipient_id = sqlc.arg(recipient_id);

-- name: GetNotificationByID :one
SELECT id, recipient_id, source_module, external, severity, title, content,
       COALESCE(action_label, '') AS action_label,
       COALESCE(action_route, '') AS action_route,
       idempotency_key, COALESCE(source_event_id, '') AS source_event_id,
       created_at, read_at, archived_at, expires_at
FROM notifications WHERE id = sqlc.arg(id) AND recipient_id = sqlc.arg(recipient_id);

-- name: MarkAllNotificationsRead :exec
UPDATE notifications SET read_at = sqlc.arg(read_at)
WHERE recipient_id = sqlc.arg(recipient_id) AND read_at IS NULL AND archived_at IS NULL AND created_at <= sqlc.arg(created_at);

-- name: CountUnreadNotifications :one
SELECT COUNT(*) FROM notifications
WHERE recipient_id = sqlc.arg(recipient_id) AND read_at IS NULL AND archived_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(expires_at));

-- name: CountUnreadNotificationsBySource :one
SELECT COUNT(*) FROM notifications
WHERE recipient_id = sqlc.arg(recipient_id) AND read_at IS NULL AND archived_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(expires_at))
  AND (sqlc.arg(source_module) = '' OR source_module = sqlc.arg(source_module));

-- name: ListNotifications :many
SELECT id, recipient_id, source_module, external, severity, title, content,
       COALESCE(action_label, '') AS action_label,
       COALESCE(action_route, '') AS action_route,
       idempotency_key, COALESCE(source_event_id, '') AS source_event_id,
       created_at, read_at, archived_at, expires_at
FROM notifications
WHERE recipient_id = sqlc.arg(recipient_id) AND archived_at IS NULL
  AND (created_at < sqlc.arg(cursor_created_at)
       OR (created_at = sqlc.arg(cursor_created_at) AND id < sqlc.arg(cursor_id)))
  AND (sqlc.arg(unread_only) = 0 OR read_at IS NULL)
  AND (sqlc.arg(source_module) = '' OR source_module = sqlc.arg(source_module))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_size);

-- name: DeleteExpiredSessionsForMaintenance :exec
DELETE FROM sessions WHERE expiry <= ?;

-- name: DeleteOldHandledNotifications :exec
DELETE FROM notifications
WHERE created_at < ? AND (read_at IS NOT NULL OR archived_at IS NOT NULL);

-- name: GetTelegramRecipient :one
SELECT user_id, enabled, chat_id, activated_at, bot_generation, status_json
FROM telegram_recipients WHERE user_id = ?;

-- name: SaveTelegramRecipient :exec
INSERT INTO telegram_recipients(user_id, enabled, chat_id, activated_at, bot_generation)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(user_id) DO UPDATE SET enabled=excluded.enabled, chat_id=excluded.chat_id,
    activated_at=excluded.activated_at, bot_generation=excluded.bot_generation;

-- name: SaveTelegramRecipientStatus :exec
UPDATE telegram_recipients SET status_json = ? WHERE user_id = ?;
