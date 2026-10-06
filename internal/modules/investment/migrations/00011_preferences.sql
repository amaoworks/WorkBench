-- +goose Up
CREATE TABLE investment_preferences (
    name TEXT PRIMARY KEY,
    content TEXT NOT NULL CHECK (json_valid(content)),
    revision INTEGER NOT NULL DEFAULT 1,
    writer TEXT NOT NULL,
    base_revision INTEGER NOT NULL,
    sequence INTEGER NOT NULL
);

-- +goose Down
DROP TABLE investment_preferences;
