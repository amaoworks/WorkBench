-- name: GetAuthUser :one
SELECT * FROM auth_users WHERE id = ?;

-- name: GetAuthUserByUsername :one
SELECT * FROM auth_users WHERE username = ?;

-- name: ListAuthUsers :many
SELECT * FROM auth_users ORDER BY created_at, id;

-- name: ActiveAuthUserIDs :many
SELECT id FROM auth_users WHERE status = 'active' ORDER BY id;

-- name: CountActiveAdmins :one
SELECT COUNT(*) FROM auth_users WHERE status = 'active' AND role = 'admin';

-- name: InsertAuthUser :exec
INSERT INTO auth_users(id, username, display_name, password_hash, role, status, credential_version, must_change_password, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateAuthUser :exec
UPDATE auth_users SET display_name = ?, role = ?, status = ?, updated_at = ? WHERE id = ?;

-- name: InitializeOwnerPassword :exec
UPDATE auth_users SET password_hash = ?, credential_version = ?, must_change_password = 1, updated_at = ? WHERE id = 'owner' AND password_hash = '';

-- name: ResetAuthUserPassword :execrows
UPDATE auth_users SET password_hash = sqlc.arg(password_hash), must_change_password = 1,
credential_version = CASE WHEN credential_version + 1 > CAST(sqlc.arg(now) AS BIGINT) THEN credential_version + 1 ELSE CAST(sqlc.arg(now) AS BIGINT) END,
updated_at = sqlc.arg(now) WHERE id = sqlc.arg(id);

-- name: BumpAuthUserVersion :exec
UPDATE auth_users SET credential_version = credential_version + 1 WHERE id = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = ?;

-- name: FindSession :one
SELECT data FROM sessions WHERE token = ? AND expiry > ?
AND NOT EXISTS (SELECT 1 FROM auth_browser_sessions b WHERE b.token_hash = sessions.token AND b.revoked_at IS NOT NULL)
AND (sessions.user_id = '' OR EXISTS (SELECT 1 FROM auth_users u WHERE u.id = sessions.user_id AND u.status = 'active'));

-- name: UpsertSession :exec
INSERT INTO sessions(token, data, expiry, user_id)
SELECT sqlc.arg(token), sqlc.arg(data), sqlc.arg(expiry), sqlc.arg(user_id)
WHERE NOT EXISTS (SELECT 1 FROM auth_browser_sessions b WHERE b.token_hash = sqlc.arg(token) AND b.revoked_at IS NOT NULL)
ON CONFLICT(token) DO UPDATE SET data = excluded.data, expiry = excluded.expiry, user_id = excluded.user_id;

-- name: UpdatePasswordHashIfCurrent :execrows
UPDATE auth_users
SET password_hash = sqlc.arg(password_hash),
    must_change_password = 0,
    credential_version = CASE
        WHEN credential_version + 1 > CAST(sqlc.arg(updated_at) AS BIGINT) THEN credential_version + 1
        ELSE CAST(sqlc.arg(updated_at) AS BIGINT)
    END,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(user_id) AND password_hash = sqlc.arg(previous_hash);

-- name: DeleteAllSessions :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expiry <= ?;
