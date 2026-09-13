-- +goose Up
ALTER TABLE modules ADD COLUMN kind TEXT NOT NULL DEFAULT 'builtin' CHECK (kind IN ('builtin', 'external'));

CREATE TABLE external_modules (
    module_id TEXT PRIMARY KEY REFERENCES modules(id) ON DELETE CASCADE,
    registration_id TEXT NOT NULL,
    connection_revision INTEGER NOT NULL DEFAULT 1,
    base_url TEXT NOT NULL,
    allow_non_local INTEGER NOT NULL DEFAULT 0 CHECK (allow_non_local IN (0, 1)),
    service_token TEXT NOT NULL,
    protocol_version INTEGER NOT NULL,
    manifest_json TEXT NOT NULL CHECK (json_valid(manifest_json)),
    generation INTEGER NOT NULL DEFAULT 0,
    observed_generation INTEGER,
    observed_enabled INTEGER CHECK (observed_enabled IS NULL OR observed_enabled IN (0, 1)),
    health TEXT NOT NULL DEFAULT 'unknown' CHECK (health IN ('unknown', 'ready', 'degraded', 'offline', 'incompatible')),
    last_error TEXT,
    last_checked_at INTEGER,
    last_success_at INTEGER,
    instance_id TEXT,
    connection_note TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX idx_modules_kind ON modules(kind, id);

-- +goose Down
DROP INDEX idx_modules_kind;
DROP TABLE external_modules;
