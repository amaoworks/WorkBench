-- +goose Up
CREATE TABLE todo_wallos_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    config TEXT NOT NULL,
    last_sync INTEGER,
    last_error TEXT NOT NULL DEFAULT ''
);

-- Track each payment occurrence; sync may replace references to deleted tasks.
CREATE TABLE todo_wallos_occurrences (
    source TEXT NOT NULL,
    subscription_id TEXT NOT NULL,
    payment_date TEXT NOT NULL,
    task_id TEXT NOT NULL,
    PRIMARY KEY (source, subscription_id, payment_date)
);

-- +goose Down
DROP TABLE todo_wallos_occurrences;
DROP TABLE todo_wallos_settings;
