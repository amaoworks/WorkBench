-- +goose Up
ALTER TABLE todo_tasks ADD COLUMN priority TEXT NOT NULL DEFAULT 'none'
    CONSTRAINT todo_tasks_priority_check CHECK (priority IN ('none', 'low', 'medium', 'high'));
ALTER TABLE todo_tasks ADD COLUMN category TEXT;
UPDATE todo_tasks
SET category = 'renewal'
WHERE id IN (SELECT task_id FROM todo_wallos_occurrences);

CREATE INDEX idx_todo_tasks_completed
ON todo_tasks(completed_at)
WHERE completed_at IS NOT NULL;

-- +goose Down
DROP INDEX idx_todo_tasks_completed;
ALTER TABLE todo_tasks DROP COLUMN category;
ALTER TABLE todo_tasks DROP COLUMN priority;
