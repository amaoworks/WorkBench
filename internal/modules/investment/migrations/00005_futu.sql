-- +goose Up
CREATE TABLE investment_futu (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    host TEXT NOT NULL DEFAULT '127.0.0.1',
    port INTEGER NOT NULL DEFAULT 11111,
    enabled INTEGER NOT NULL DEFAULT 0,
    allow_non_local INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT INTO investment_futu(id, host, port, enabled, allow_non_local, last_error, updated_at)
VALUES (1, '127.0.0.1', 11111, 0, 0, '', 0);

CREATE TABLE investment_futu_bars (
    symbol TEXT NOT NULL,
    resolution TEXT NOT NULL,
    time_ms INTEGER NOT NULL,
    open REAL NOT NULL,
    high REAL NOT NULL,
    low REAL NOT NULL,
    close REAL NOT NULL,
    volume REAL NOT NULL,
    PRIMARY KEY (symbol, resolution, time_ms)
);

-- +goose Down
DROP TABLE IF EXISTS investment_futu_bars;
DROP TABLE IF EXISTS investment_futu;
