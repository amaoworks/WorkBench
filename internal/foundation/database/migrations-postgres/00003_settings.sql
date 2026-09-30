-- +goose Up
CREATE TABLE workspace_settings (
    section TEXT PRIMARY KEY,
    value TEXT NOT NULL CHECK ((value::json IS NOT NULL))
);

-- +goose Down
DROP TABLE workspace_settings;
