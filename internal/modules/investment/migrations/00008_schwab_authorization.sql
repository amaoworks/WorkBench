-- +goose Up
ALTER TABLE investment_schwab ADD COLUMN reauthorization_required INTEGER NOT NULL DEFAULT 0 CHECK (reauthorization_required IN (0, 1));

-- +goose Down
ALTER TABLE investment_schwab DROP COLUMN reauthorization_required;
