-- name: GetUserSetting :one
SELECT value FROM user_settings WHERE user_id = ? AND section = ?;

-- name: PutUserSetting :exec
INSERT INTO user_settings(user_id, section, value) VALUES (?, ?, ?)
ON CONFLICT(user_id, section) DO UPDATE SET value = excluded.value;
