-- name: GetSchwab :one
SELECT app_key, app_secret, callback_url, access_token, refresh_token, token_expires_at,
    streamer_info, oauth_state, oauth_state_expires_at, last_error, updated_at, reauthorization_required
FROM investment_schwab WHERE id = 1;

-- name: UpsertSchwab :exec
INSERT INTO investment_schwab(
    id, app_key, app_secret, callback_url, access_token, refresh_token, token_expires_at,
    streamer_info, oauth_state, oauth_state_expires_at, last_error, updated_at, reauthorization_required
) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    app_key = excluded.app_key,
    app_secret = excluded.app_secret,
    callback_url = excluded.callback_url,
    access_token = excluded.access_token,
    refresh_token = excluded.refresh_token,
    token_expires_at = excluded.token_expires_at,
    streamer_info = excluded.streamer_info,
    oauth_state = excluded.oauth_state,
    oauth_state_expires_at = excluded.oauth_state_expires_at,
    last_error = excluded.last_error,
    updated_at = excluded.updated_at,
    reauthorization_required = excluded.reauthorization_required;
