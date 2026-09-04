-- name: CountAuthCredentials :one
SELECT COUNT(*) FROM auth_credentials;

-- name: GetPasswordHash :one
SELECT password_hash FROM auth_credentials WHERE id = 1;

-- name: InsertInitialPassword :exec
INSERT INTO auth_credentials(id, password_hash, updated_at) VALUES (1, ?, ?);

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = ?;

-- name: FindSession :one
SELECT data FROM sessions WHERE token = ? AND expiry > ?;

-- name: UpsertSession :exec
INSERT INTO sessions(token, data, expiry) VALUES (?, ?, ?)
ON CONFLICT(token) DO UPDATE SET data = excluded.data, expiry = excluded.expiry;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expiry <= ?;
