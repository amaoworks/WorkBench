-- +goose Up
ALTER TABLE investment_networth_account_scopes ADD COLUMN account_label TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE investment_networth_account_scopes DROP COLUMN account_label;
