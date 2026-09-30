-- +goose Up
CREATE TABLE investment_watchlists (
    id BIGINT PRIMARY KEY CHECK (id = 1),
    revision BIGINT NOT NULL DEFAULT 0,
    writer TEXT NOT NULL DEFAULT '',
    base_revision BIGINT NOT NULL DEFAULT 0,
    sequence BIGINT NOT NULL DEFAULT 0,
    content TEXT NOT NULL DEFAULT 'null'
);
INSERT INTO investment_watchlists(id) VALUES (1);

-- +goose Down
DROP TABLE investment_watchlists;
