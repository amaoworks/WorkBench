-- +goose Up
CREATE TABLE todo_tasks (
    id           TEXT PRIMARY KEY,
    title        TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    due_at       INTEGER,
    completed_at INTEGER,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE INDEX idx_todo_tasks_due
ON todo_tasks(due_at)
WHERE completed_at IS NULL AND due_at IS NOT NULL;

CREATE INDEX idx_todo_tasks_updated
ON todo_tasks(updated_at DESC);

-- +goose Down
DROP TABLE todo_tasks;

