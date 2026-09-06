-- +goose Up
CREATE TABLE investment_quotes (
    symbol TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    price_cents INTEGER NOT NULL CHECK (price_cents > 0),
    change_bps INTEGER NOT NULL,
    as_of INTEGER NOT NULL
);
CREATE TABLE investment_summary (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    content TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE investment_summary;
DROP TABLE investment_quotes;
