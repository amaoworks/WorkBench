-- +goose Up
CREATE TABLE workspace_settings (
    section TEXT PRIMARY KEY,
    value TEXT NOT NULL CHECK (json_valid(value))
);

-- +goose Down
DROP TABLE workspace_settings;
