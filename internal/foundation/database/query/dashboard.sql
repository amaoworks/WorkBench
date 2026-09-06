-- name: GetDashboardLayout :one
SELECT value FROM workspace_settings WHERE section = 'dashboard';

-- name: SaveDashboardLayout :exec
INSERT INTO workspace_settings(section, value) VALUES ('dashboard', ?)
ON CONFLICT(section) DO UPDATE SET value = excluded.value;

-- name: ResetDashboardLayout :exec
DELETE FROM workspace_settings WHERE section = 'dashboard';
