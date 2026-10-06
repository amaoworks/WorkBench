-- +goose Up
CREATE TABLE investment_preferences (
    name TEXT PRIMARY KEY,
    content TEXT NOT NULL CHECK (content::jsonb IS NOT NULL),
    revision BIGINT NOT NULL DEFAULT 1,
    writer TEXT NOT NULL,
    base_revision BIGINT NOT NULL,
    sequence BIGINT NOT NULL
);

-- +goose Down
DROP TABLE investment_preferences;
