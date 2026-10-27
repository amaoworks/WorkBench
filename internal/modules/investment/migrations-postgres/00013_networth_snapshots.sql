-- +goose Up
CREATE TABLE investment_networth_snapshots (
    account_hash TEXT NOT NULL,
    day          TEXT NOT NULL,
    net_worth    DOUBLE PRECISION NOT NULL,
    cash         DOUBLE PRECISION NOT NULL,
    invested     DOUBLE PRECISION NOT NULL,
    day_pnl      DOUBLE PRECISION NOT NULL,
    captured_at  BIGINT NOT NULL,
    PRIMARY KEY (account_hash, day)
);

CREATE INDEX idx_investment_networth_day
ON investment_networth_snapshots(day);

CREATE TABLE investment_networth_account_scopes (
    authorized_at BIGINT NOT NULL,
    account_hash  TEXT NOT NULL,
    PRIMARY KEY (authorized_at, account_hash)
);

-- +goose Down
DROP TABLE investment_networth_account_scopes;
DROP TABLE investment_networth_snapshots;
