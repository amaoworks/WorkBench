-- +goose Up
DROP TABLE investment_summary;
DROP TABLE investment_quotes;

-- +goose Down
CREATE TABLE investment_quotes (
    symbol TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    price_cents BIGINT NOT NULL CHECK (price_cents > 0),
    change_bps BIGINT NOT NULL,
    as_of BIGINT NOT NULL
);
CREATE TABLE investment_summary (
    id BIGINT PRIMARY KEY CHECK (id = 1),
    content TEXT NOT NULL,
    created_at BIGINT NOT NULL
);
