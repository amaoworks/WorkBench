-- +goose Up
CREATE TABLE mobile_push_tokens (
    id                TEXT PRIMARY KEY,
    device_session_id TEXT NOT NULL REFERENCES auth_device_sessions(id) ON DELETE CASCADE,
    platform          TEXT NOT NULL CHECK (platform IN ('ios', 'android')),
    token             TEXT NOT NULL,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    UNIQUE(device_session_id, platform),
    UNIQUE(platform, token)
);

-- +goose Down
DROP TABLE mobile_push_tokens;
