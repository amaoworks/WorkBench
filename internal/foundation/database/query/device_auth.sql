-- name: GetActiveSigningKey :one
SELECT * FROM auth_signing_keys WHERE retired_at IS NULL ORDER BY created_at DESC LIMIT 1;

-- name: GetSigningKey :one
SELECT * FROM auth_signing_keys WHERE id = ?;

-- name: InsertSigningKey :exec
INSERT INTO auth_signing_keys(id, private_key, created_at) VALUES (?, ?, ?);

-- name: RetireSigningKeys :exec
UPDATE auth_signing_keys SET retired_at = ? WHERE retired_at IS NULL;

-- name: InsertDeviceSession :exec
INSERT INTO auth_device_sessions(id, device_name, platform, credential_version, created_at, last_seen_at, expires_at, user_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetDeviceSession :one
SELECT * FROM auth_device_sessions WHERE id = ?;

-- name: ListDeviceSessions :many
SELECT * FROM auth_device_sessions WHERE revoked_at IS NULL AND expires_at > ? AND user_id = ? ORDER BY created_at DESC, id;

-- name: TouchDeviceSession :exec
UPDATE auth_device_sessions SET last_seen_at = ? WHERE id = ?;

-- name: RevokeDeviceSession :execrows
UPDATE auth_device_sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL;

-- name: RevokeAllDeviceSessions :exec
UPDATE auth_device_sessions SET revoked_at = ? WHERE revoked_at IS NULL AND user_id = ?;

-- name: InsertRefreshToken :exec
INSERT INTO auth_refresh_tokens(token_hash, session_id, created_at) VALUES (?, ?, ?);

-- name: GetRefreshToken :one
SELECT * FROM auth_refresh_tokens WHERE token_hash = ?;

-- name: ConsumeRefreshToken :execrows
UPDATE auth_refresh_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL;

-- name: InsertBrowserSession :exec
INSERT INTO auth_browser_sessions(id, token_hash, created_at, last_seen_at, device_session_id, user_id) VALUES (?, ?, ?, ?, ?, ?);

-- name: TouchBrowserSession :exec
UPDATE auth_browser_sessions SET last_seen_at = ? WHERE token_hash = ?;

-- name: ListBrowserSessions :many
SELECT b.id, b.token_hash, b.created_at, b.last_seen_at, s.expiry
FROM auth_browser_sessions b JOIN sessions s ON s.token = b.token_hash
WHERE s.expiry > sqlc.arg(now) AND b.revoked_at IS NULL AND b.user_id = sqlc.arg(user_id)
AND (b.device_session_id IS NULL OR EXISTS (
    SELECT 1 FROM auth_device_sessions d WHERE d.id = b.device_session_id AND d.revoked_at IS NULL AND d.expires_at > sqlc.arg(now)
)) ORDER BY b.created_at DESC, b.id;

-- name: GetBrowserSession :one
SELECT * FROM auth_browser_sessions WHERE id = ?;

-- name: RevokeBrowserSession :exec
UPDATE auth_browser_sessions SET revoked_at = ? WHERE token_hash = ?;

-- name: RevokeAllBrowserSessions :exec
UPDATE auth_browser_sessions SET revoked_at = ? WHERE revoked_at IS NULL AND user_id = ?;

-- name: DeleteExpiredDeviceSessions :exec
DELETE FROM auth_device_sessions WHERE expires_at <= ?;

-- name: DeleteExpiredSigningKeys :exec
DELETE FROM auth_signing_keys WHERE retired_at IS NOT NULL AND retired_at <= ?;

-- name: DeleteExpiredBrowserSessions :exec
DELETE FROM auth_browser_sessions WHERE created_at <= ?
AND NOT EXISTS (SELECT 1 FROM sessions WHERE sessions.token = auth_browser_sessions.token_hash);

-- name: InsertBrowserGrant :exec
INSERT INTO auth_browser_grants(token_hash, session_id, expires_at) VALUES (?, ?, ?);

-- name: GetBrowserGrant :one
SELECT * FROM auth_browser_grants WHERE token_hash = ?;

-- name: ConsumeBrowserGrant :execrows
UPDATE auth_browser_grants SET used_at = ? WHERE token_hash = ? AND used_at IS NULL;

-- name: DeleteExpiredBrowserGrants :exec
DELETE FROM auth_browser_grants WHERE expires_at <= ?;
