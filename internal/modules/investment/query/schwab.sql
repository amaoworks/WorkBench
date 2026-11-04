-- name: GetSchwabApp :one
SELECT * FROM investment_schwab_app WHERE id = 1;

-- name: SaveSchwabApp :exec
INSERT INTO investment_schwab_app(id,app_key,app_secret,callback_url,version)
VALUES(1,?,?,?,1)
ON CONFLICT(id) DO UPDATE SET app_key=excluded.app_key,app_secret=excluded.app_secret,callback_url=excluded.callback_url,version=investment_schwab_app.version+1;

-- name: GetSchwab :one
SELECT app_key,app_secret,callback_url,version, c.* FROM investment_schwab_app a
JOIN investment_schwab_connections c ON c.app_version = a.version WHERE a.id=1 AND c.user_id=?;

-- name: UpsertSchwab :execrows
INSERT INTO investment_schwab_connections(user_id,access_token,refresh_token,token_expires_at,streamer_info,oauth_state,oauth_state_expires_at,last_error,updated_at,reauthorization_required,authorized_at,app_version,connection_version)
SELECT sqlc.arg(user_id),sqlc.arg(access_token),sqlc.arg(refresh_token),sqlc.arg(token_expires_at),sqlc.arg(streamer_info),sqlc.arg(oauth_state),sqlc.arg(oauth_state_expires_at),sqlc.arg(last_error),sqlc.arg(updated_at),sqlc.arg(reauthorization_required),sqlc.arg(authorized_at),sqlc.arg(app_version),CAST(sqlc.arg(connection_version) AS BIGINT)+1
WHERE EXISTS(SELECT 1 FROM investment_schwab_app WHERE version=sqlc.arg(app_version))
ON CONFLICT(user_id) DO UPDATE SET access_token=excluded.access_token,refresh_token=excluded.refresh_token,
token_expires_at=excluded.token_expires_at,streamer_info=excluded.streamer_info,oauth_state=excluded.oauth_state,
oauth_state_expires_at=excluded.oauth_state_expires_at,last_error=excluded.last_error,updated_at=excluded.updated_at,
reauthorization_required=excluded.reauthorization_required,authorized_at=excluded.authorized_at,app_version=excluded.app_version,
connection_version=excluded.connection_version
WHERE investment_schwab_connections.connection_version=excluded.connection_version-1
OR investment_schwab_connections.app_version<>excluded.app_version;

-- name: InvalidateSchwabOAuth :exec
UPDATE investment_schwab_connections
SET oauth_state='', oauth_state_expires_at=0, connection_version=connection_version+1
WHERE user_id=?;

-- name: FindSchwabOAuthOwner :one
SELECT user_id FROM investment_schwab_connections c JOIN investment_schwab_app a ON c.app_version=a.version
WHERE c.oauth_state=? AND c.oauth_state<>'';
