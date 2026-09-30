-- +goose Up
CREATE TABLE investment_price_rules (
    id TEXT PRIMARY KEY,
    symbol TEXT NOT NULL,
    direction TEXT NOT NULL CHECK (direction IN ('up', 'down')),
    threshold_bps BIGINT NOT NULL CHECK (threshold_bps BETWEEN 1 AND 100000),
    enabled BIGINT NOT NULL CHECK (enabled IN (0, 1)),
    version BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    price DOUBLE PRECISION,
    previous_close DOUBLE PRECISION,
    change_percent DOUBLE PRECISION,
    quote_at BIGINT,
    checked_at BIGINT,
    last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE investment_price_triggers (
    id TEXT PRIMARY KEY,
    rule_id TEXT NOT NULL,
    trading_date TEXT NOT NULL,
    symbol TEXT NOT NULL,
    direction TEXT NOT NULL,
    threshold_bps BIGINT NOT NULL,
    price DOUBLE PRECISION NOT NULL,
    previous_close DOUBLE PRECISION NOT NULL,
    change_percent DOUBLE PRECISION NOT NULL,
    quote_at BIGINT NOT NULL,
    triggered_at BIGINT NOT NULL,
    notification_id TEXT NOT NULL,
    UNIQUE(rule_id, trading_date)
);
CREATE INDEX investment_price_triggers_time ON investment_price_triggers(triggered_at DESC, id DESC);
CREATE TABLE investment_price_monitor (
    id BIGINT PRIMARY KEY CHECK (id = 1),
    status TEXT NOT NULL DEFAULT 'waiting',
    checked_at BIGINT,
    last_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO investment_price_monitor(id) VALUES(1);

-- +goose Down
DROP TABLE investment_price_monitor;
DROP TABLE investment_price_triggers;
DROP TABLE investment_price_rules;
