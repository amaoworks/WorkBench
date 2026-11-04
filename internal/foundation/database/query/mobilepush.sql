-- name: DeleteMobilePushTokenOwner :exec
DELETE FROM mobile_push_tokens
WHERE platform = sqlc.arg(platform)
  AND token = sqlc.arg(token)
  AND device_session_id <> sqlc.arg(device_session_id);

-- name: UpsertMobilePushToken :execrows
INSERT INTO mobile_push_tokens(id, device_session_id, platform, token, created_at, updated_at)
SELECT sqlc.arg(id), sqlc.arg(device_session_id), sqlc.arg(platform), sqlc.arg(token),
       sqlc.arg(created_at), sqlc.arg(updated_at)
FROM auth_device_sessions
WHERE id = sqlc.arg(device_session_id)
  AND user_id = sqlc.arg(user_id)
  AND EXISTS (SELECT 1 FROM auth_users u WHERE u.id = auth_device_sessions.user_id AND u.status = 'active')
  AND revoked_at IS NULL
  AND expires_at > sqlc.arg(updated_at)
ON CONFLICT(device_session_id, platform) DO UPDATE SET
    created_at = CASE WHEN mobile_push_tokens.token = excluded.token
                      THEN mobile_push_tokens.created_at ELSE excluded.created_at END,
    token = excluded.token,
    updated_at = excluded.updated_at;

-- name: DeleteMobilePushToken :exec
DELETE FROM mobile_push_tokens
WHERE mobile_push_tokens.device_session_id = sqlc.arg(device_session_id)
  AND mobile_push_tokens.platform = sqlc.arg(platform)
  AND mobile_push_tokens.token = sqlc.arg(token)
  AND EXISTS (SELECT 1 FROM auth_device_sessions s WHERE s.id = mobile_push_tokens.device_session_id AND s.user_id = sqlc.arg(user_id));

-- name: ListActiveMobilePushTokens :many
SELECT t.id, t.platform, t.token
FROM mobile_push_tokens t
JOIN auth_device_sessions s ON s.id = t.device_session_id
JOIN auth_users u ON u.id = s.user_id AND u.status = 'active'
WHERE s.user_id = sqlc.arg(recipient_id) AND s.revoked_at IS NULL
  AND s.expires_at > sqlc.arg(now)
  AND t.created_at <= sqlc.arg(notification_created_at);

-- name: DeleteInvalidMobilePushToken :exec
DELETE FROM mobile_push_tokens
WHERE id = sqlc.arg(id) AND token = sqlc.arg(token);

-- name: GetMobilePushState :one
SELECT value FROM workspace_settings WHERE section = 'mobile_push';

-- name: SaveMobilePushState :exec
INSERT INTO workspace_settings(section, value) VALUES ('mobile_push', sqlc.arg(value))
ON CONFLICT(section) DO UPDATE SET value = excluded.value;
