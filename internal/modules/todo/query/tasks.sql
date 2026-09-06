-- name: CreateTask :exec
INSERT INTO todo_tasks(id, title, description, due_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetTask :one
SELECT id, title, description, due_at, completed_at, created_at, updated_at
FROM todo_tasks WHERE id = ?;

-- name: ListTasksPage :many
SELECT id, title, description, due_at, completed_at, created_at, updated_at
FROM todo_tasks
WHERE CAST(sqlc.arg(has_cursor) AS INTEGER) = 0 OR
  (completed_at IS NOT NULL, COALESCE(due_at, 9223372036854775807), -created_at, id) >
  (CAST(sqlc.arg(done) AS INTEGER), CAST(sqlc.arg(due) AS INTEGER), -CAST(sqlc.arg(created) AS INTEGER), CAST(sqlc.arg(cursor_id) AS TEXT))
ORDER BY completed_at IS NOT NULL, COALESCE(due_at, 9223372036854775807), created_at DESC, id
LIMIT sqlc.arg(page_limit);

-- name: UpdateTask :exec
UPDATE todo_tasks
SET title = ?, description = ?, due_at = ?, completed_at = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteTask :execrows
DELETE FROM todo_tasks WHERE id = ?;

-- name: TodoSummary :one
SELECT
    CAST(COALESCE(SUM(CASE WHEN completed_at IS NULL THEN 1 ELSE 0 END), 0) AS INTEGER) AS open,
    CAST(COALESCE(SUM(CASE WHEN completed_at IS NULL AND due_at < ? THEN 1 ELSE 0 END), 0) AS INTEGER) AS overdue,
    CAST(COALESCE(SUM(CASE WHEN completed_at IS NOT NULL THEN 1 ELSE 0 END), 0) AS INTEGER) AS completed
FROM todo_tasks;

-- name: ListDueTasks :many
SELECT id, title, due_at FROM todo_tasks
WHERE completed_at IS NULL AND due_at IS NOT NULL AND due_at <= ?
ORDER BY due_at LIMIT 100;
