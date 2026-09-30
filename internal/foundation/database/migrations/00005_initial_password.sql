-- +goose Up
ALTER TABLE auth_credentials
ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1));

-- +goose Down
ALTER TABLE auth_credentials DROP COLUMN must_change_password;
