-- +goose Up
CREATE TABLE investment_networth_snapshots (
    account_hash TEXT NOT NULL,
    day          TEXT NOT NULL,
    net_worth    REAL NOT NULL,
    cash         REAL NOT NULL,
    invested     REAL NOT NULL,
    day_pnl      REAL NOT NULL,
    captured_at  INTEGER NOT NULL,
    PRIMARY KEY (account_hash, day)
);

CREATE INDEX idx_investment_networth_day
ON investment_networth_snapshots(day);

CREATE TABLE investment_networth_account_scopes (
    authorized_at INTEGER NOT NULL,
    account_hash  TEXT NOT NULL,
    PRIMARY KEY (authorized_at, account_hash)
);

-- +goose Down
DROP TABLE investment_networth_account_scopes;
DROP TABLE investment_networth_snapshots;
