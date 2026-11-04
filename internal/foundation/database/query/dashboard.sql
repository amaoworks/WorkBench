-- name: GetDashboardLayout :one
SELECT value FROM user_settings WHERE user_id = ? AND section = 'dashboard';

-- name: SaveDashboardLayout :exec
INSERT INTO user_settings(user_id, section, value) VALUES (?, 'dashboard', ?)
ON CONFLICT(user_id, section) DO UPDATE SET value = excluded.value;

-- name: ResetDashboardLayout :exec
DELETE FROM user_settings WHERE user_id = ? AND section = 'dashboard';
