-- +goose Up
CREATE TABLE investment_price_rules (
    id TEXT PRIMARY KEY,
    symbol TEXT NOT NULL,
    direction TEXT NOT NULL CHECK (direction IN ('up', 'down')),
    threshold_bps INTEGER NOT NULL CHECK (threshold_bps BETWEEN 1 AND 100000),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    price REAL,
    previous_close REAL,
    change_percent REAL,
    quote_at INTEGER,
    checked_at INTEGER,
    last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE investment_price_triggers (
    id TEXT PRIMARY KEY,
    rule_id TEXT NOT NULL,
    trading_date TEXT NOT NULL,
    symbol TEXT NOT NULL,
    direction TEXT NOT NULL,
    threshold_bps INTEGER NOT NULL,
    price REAL NOT NULL,
    previous_close REAL NOT NULL,
    change_percent REAL NOT NULL,
    quote_at INTEGER NOT NULL,
    triggered_at INTEGER NOT NULL,
    notification_id TEXT NOT NULL,
    UNIQUE(rule_id, trading_date)
);
CREATE INDEX investment_price_triggers_time ON investment_price_triggers(triggered_at DESC, id DESC);
CREATE TABLE investment_price_monitor (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    status TEXT NOT NULL DEFAULT 'waiting',
    checked_at INTEGER,
    last_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO investment_price_monitor(id) VALUES(1);

-- +goose Down
DROP TABLE investment_price_monitor;
DROP TABLE investment_price_triggers;
DROP TABLE investment_price_rules;
