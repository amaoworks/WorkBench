-- +goose Up
CREATE TABLE investment_schwab (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    app_key TEXT NOT NULL DEFAULT '',
    app_secret TEXT NOT NULL DEFAULT '',
    callback_url TEXT NOT NULL DEFAULT '',
    access_token TEXT NOT NULL DEFAULT '',
    refresh_token TEXT NOT NULL DEFAULT '',
    token_expires_at INTEGER NOT NULL DEFAULT 0,
    streamer_info TEXT NOT NULL DEFAULT '',
    oauth_state TEXT NOT NULL DEFAULT '',
    oauth_state_expires_at INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE investment_schwab;
