-- +goose Up
ALTER TABLE investment_schwab ADD COLUMN authorized_at BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE investment_schwab DROP COLUMN authorized_at;
