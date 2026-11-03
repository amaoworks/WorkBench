-- name: CountAuthCredentials :one
SELECT COUNT(*) FROM auth_credentials;

-- name: GetPasswordHash :one
SELECT password_hash FROM auth_credentials WHERE id = 1;

-- name: GetAuthCredential :one
SELECT password_hash, updated_at, must_change_password FROM auth_credentials WHERE id = 1;

-- name: InsertInitialPassword :exec
INSERT INTO auth_credentials(id, password_hash, updated_at, must_change_password) VALUES (1, ?, ?, 1);

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = ?;

-- name: FindSession :one
SELECT data FROM sessions WHERE token = ? AND expiry > ?
AND NOT EXISTS (SELECT 1 FROM auth_browser_sessions b WHERE b.token_hash = sessions.token AND b.revoked_at IS NOT NULL);

-- name: UpsertSession :exec
INSERT INTO sessions(token, data, expiry)
SELECT sqlc.arg(token), sqlc.arg(data), sqlc.arg(expiry)
WHERE NOT EXISTS (SELECT 1 FROM auth_browser_sessions b WHERE b.token_hash = sqlc.arg(token) AND b.revoked_at IS NOT NULL)
ON CONFLICT(token) DO UPDATE SET data = excluded.data, expiry = excluded.expiry;

-- name: UpdatePasswordHashIfCurrent :execrows
UPDATE auth_credentials
SET password_hash = sqlc.arg(password_hash),
    must_change_password = 0,
    updated_at = CASE
        WHEN updated_at + 1 > CAST(sqlc.arg(updated_at) AS BIGINT) THEN updated_at + 1
        ELSE CAST(sqlc.arg(updated_at) AS BIGINT)
    END
WHERE id = 1 AND password_hash = sqlc.arg(previous_hash);

-- name: DeleteAllSessions :exec
DELETE FROM sessions;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expiry <= ?;
