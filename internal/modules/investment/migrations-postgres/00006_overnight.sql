-- +goose Up
ALTER TABLE investment_futu ADD COLUMN overnight_enabled BIGINT NOT NULL DEFAULT 0;
-- Preserve the previous overlay choice while separating it from the provider.
UPDATE investment_futu SET overnight_enabled = enabled;

-- +goose Down
ALTER TABLE investment_futu DROP COLUMN overnight_enabled;
