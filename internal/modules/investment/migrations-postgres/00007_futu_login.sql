-- +goose Up
ALTER TABLE investment_futu ADD COLUMN account TEXT NOT NULL DEFAULT '';
ALTER TABLE investment_futu ADD COLUMN password_md5 TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE investment_futu DROP COLUMN password_md5;
ALTER TABLE investment_futu DROP COLUMN account;
