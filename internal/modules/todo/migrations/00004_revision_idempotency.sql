-- +goose Up
ALTER TABLE todo_tasks ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;

CREATE TABLE todo_create_requests (
    request_key  TEXT PRIMARY KEY,
    request_hash TEXT NOT NULL,
    response_json TEXT NOT NULL
);

-- +goose Down
DROP TABLE todo_create_requests;
ALTER TABLE todo_tasks DROP COLUMN revision;
