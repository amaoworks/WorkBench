-- +goose Up
ALTER TABLE todo_tasks ADD COLUMN user_id TEXT NOT NULL DEFAULT 'owner';
CREATE INDEX idx_todo_tasks_user_due ON todo_tasks(user_id, due_at, id);
CREATE INDEX idx_todo_tasks_user_updated ON todo_tasks(user_id, updated_at DESC);

ALTER TABLE todo_create_requests RENAME TO todo_create_requests_legacy;
CREATE TABLE todo_create_requests (
    user_id TEXT NOT NULL,
    request_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    response_json TEXT NOT NULL,
    PRIMARY KEY (user_id, request_key)
);
INSERT INTO todo_create_requests SELECT 'owner', request_key, request_hash, response_json FROM todo_create_requests_legacy;
DROP TABLE todo_create_requests_legacy;

ALTER TABLE todo_wallos_settings RENAME TO todo_wallos_settings_legacy;
CREATE TABLE todo_wallos_settings (
    user_id TEXT PRIMARY KEY,
    config TEXT NOT NULL,
    last_sync BIGINT,
    last_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO todo_wallos_settings SELECT 'owner', config, last_sync, last_error FROM todo_wallos_settings_legacy;
DROP TABLE todo_wallos_settings_legacy;

ALTER TABLE todo_wallos_occurrences RENAME TO todo_wallos_occurrences_legacy;
CREATE TABLE todo_wallos_occurrences (
    user_id TEXT NOT NULL,
    source TEXT NOT NULL,
    subscription_id TEXT NOT NULL,
    payment_date TEXT NOT NULL,
    task_id TEXT NOT NULL,
    PRIMARY KEY (user_id, source, subscription_id, payment_date)
);
INSERT INTO todo_wallos_occurrences SELECT 'owner', source, subscription_id, payment_date, task_id FROM todo_wallos_occurrences_legacy;
DROP TABLE todo_wallos_occurrences_legacy;

-- +goose Down
ALTER TABLE todo_wallos_occurrences RENAME TO todo_wallos_occurrences_owned;
CREATE TABLE todo_wallos_occurrences (
    source TEXT NOT NULL, subscription_id TEXT NOT NULL, payment_date TEXT NOT NULL, task_id TEXT NOT NULL,
    PRIMARY KEY (source, subscription_id, payment_date)
);
INSERT INTO todo_wallos_occurrences SELECT source, subscription_id, payment_date, task_id FROM todo_wallos_occurrences_owned;
DROP TABLE todo_wallos_occurrences_owned;
ALTER TABLE todo_wallos_settings RENAME TO todo_wallos_settings_owned;
CREATE TABLE todo_wallos_settings (
    id BIGINT PRIMARY KEY CHECK (id = 1), config TEXT NOT NULL, last_sync BIGINT, last_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO todo_wallos_settings SELECT 1, config, last_sync, last_error FROM todo_wallos_settings_owned;
DROP TABLE todo_wallos_settings_owned;
ALTER TABLE todo_create_requests RENAME TO todo_create_requests_owned;
CREATE TABLE todo_create_requests (request_key TEXT PRIMARY KEY, request_hash TEXT NOT NULL, response_json TEXT NOT NULL);
INSERT INTO todo_create_requests SELECT request_key, request_hash, response_json FROM todo_create_requests_owned;
DROP TABLE todo_create_requests_owned;
DROP INDEX idx_todo_tasks_user_due;
DROP INDEX idx_todo_tasks_user_updated;
ALTER TABLE todo_tasks DROP COLUMN user_id;
