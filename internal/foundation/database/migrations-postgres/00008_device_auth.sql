-- +goose Up
CREATE TABLE auth_signing_keys (
    id TEXT PRIMARY KEY,
    private_key BYTEA NOT NULL,
    created_at BIGINT NOT NULL,
    retired_at BIGINT
);
CREATE TABLE auth_device_sessions (
    id TEXT PRIMARY KEY,
    device_name TEXT NOT NULL,
    platform TEXT NOT NULL CHECK (platform IN ('ios', 'android', 'other')),
    credential_version BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    last_seen_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    revoked_at BIGINT
);
CREATE TABLE auth_refresh_tokens (
    token_hash TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES auth_device_sessions(id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL,
    used_at BIGINT
);
CREATE INDEX idx_auth_refresh_session ON auth_refresh_tokens(session_id);
CREATE TABLE auth_browser_grants (
    token_hash TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES auth_device_sessions(id) ON DELETE CASCADE,
    expires_at BIGINT NOT NULL,
    used_at BIGINT
);
CREATE TABLE auth_browser_sessions (
    id TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    device_session_id TEXT REFERENCES auth_device_sessions(id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL,
    last_seen_at BIGINT NOT NULL,
    revoked_at BIGINT
);
INSERT INTO auth_browser_sessions(id, token_hash, created_at, last_seen_at)
SELECT md5(random()::text || token), token, GREATEST(0, expiry - 86400000), GREATEST(0, expiry - 86400000)
FROM sessions;

-- +goose Down
DROP TABLE auth_browser_grants;
DROP TABLE auth_browser_sessions;
DROP TABLE auth_refresh_tokens;
DROP TABLE auth_device_sessions;
DROP TABLE auth_signing_keys;
