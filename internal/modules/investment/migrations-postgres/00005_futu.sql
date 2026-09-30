-- +goose Up
CREATE TABLE investment_futu (
    id BIGINT PRIMARY KEY CHECK (id = 1),
    host TEXT NOT NULL DEFAULT '127.0.0.1',
    port BIGINT NOT NULL DEFAULT 11111,
    enabled BIGINT NOT NULL DEFAULT 0,
    allow_non_local BIGINT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    updated_at BIGINT NOT NULL DEFAULT 0
);

INSERT INTO investment_futu(id, host, port, enabled, allow_non_local, last_error, updated_at)
VALUES (1, '127.0.0.1', 11111, 0, 0, '', 0);

CREATE TABLE investment_futu_bars (
    symbol TEXT NOT NULL,
    resolution TEXT NOT NULL,
    time_ms BIGINT NOT NULL,
    open DOUBLE PRECISION NOT NULL,
    high DOUBLE PRECISION NOT NULL,
    low DOUBLE PRECISION NOT NULL,
    close DOUBLE PRECISION NOT NULL,
    volume DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (symbol, resolution, time_ms)
);

-- +goose Down
DROP TABLE IF EXISTS investment_futu_bars;
DROP TABLE IF EXISTS investment_futu;
