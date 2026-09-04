-- name: InsertNotification :execrows
INSERT INTO notifications(
    id, source_module, severity, title, content, action_label,
    action_route, idempotency_key, source_event_id, created_at, expires_at
) VALUES (
    sqlc.arg(id), sqlc.arg(source_module), sqlc.arg(severity), sqlc.arg(title), sqlc.arg(content),
    NULLIF(sqlc.arg(action_label), ''), NULLIF(sqlc.arg(action_route), ''),
    sqlc.arg(idempotency_key), NULLIF(sqlc.arg(source_event_id), ''),
    sqlc.arg(created_at), sqlc.arg(expires_at)
)
ON CONFLICT(idempotency_key) DO NOTHING;

-- name: GetNotificationByIdempotencyKey :one
SELECT id, source_module, severity, title, content,
       COALESCE(action_label, '') AS action_label,
       COALESCE(action_route, '') AS action_route,
       idempotency_key, COALESCE(source_event_id, '') AS source_event_id,
       created_at, read_at, archived_at, expires_at
FROM notifications WHERE idempotency_key = ?;

-- name: MarkAllNotificationsRead :exec
UPDATE notifications SET read_at = ?
WHERE read_at IS NULL AND archived_at IS NULL AND created_at <= ?;

-- name: CountUnreadNotifications :one
SELECT COUNT(*) FROM notifications
WHERE read_at IS NULL AND archived_at IS NULL
  AND (expires_at IS NULL OR expires_at > ?);

-- name: DeleteExpiredSessionsForMaintenance :exec
DELETE FROM sessions WHERE expiry <= ?;

-- name: DeleteOldHandledNotifications :exec
DELETE FROM notifications
WHERE created_at < ? AND (read_at IS NOT NULL OR archived_at IS NOT NULL);
