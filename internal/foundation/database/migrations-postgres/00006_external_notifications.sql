-- +goose Up
ALTER TABLE notifications
ADD COLUMN external SMALLINT NOT NULL DEFAULT 0 CHECK (external IN (0, 1));

UPDATE notifications
SET external = 1
WHERE source_module = 'investment'
  AND idempotency_key LIKE 'investment:monitor:%';

-- +goose Down
ALTER TABLE notifications DROP COLUMN external;
