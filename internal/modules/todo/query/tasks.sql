-- name: CreateTask :exec
INSERT INTO todo_tasks(id, title, description, due_at, priority, category, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTask :one
SELECT id, title, description, due_at, completed_at, created_at, updated_at, priority, category
FROM todo_tasks WHERE id = ?;

-- name: ListTasksPage :many
SELECT id, title, description, due_at, completed_at, created_at, updated_at, priority, category
FROM todo_tasks
WHERE
  (CAST(sqlc.arg(view) AS BIGINT) = 0
    OR (CAST(sqlc.arg(view) AS BIGINT) = 1 AND completed_at IS NULL AND due_at IS NOT NULL AND due_at < CAST(sqlc.arg(end_today) AS BIGINT))
    OR (CAST(sqlc.arg(view) AS BIGINT) = 2 AND completed_at IS NULL AND due_at IS NOT NULL AND due_at < CAST(sqlc.arg(end_week) AS BIGINT))
    OR (CAST(sqlc.arg(view) AS BIGINT) = 3 AND completed_at IS NULL AND (due_at IS NULL OR due_at >= CAST(sqlc.arg(end_week) AS BIGINT)))
    OR (CAST(sqlc.arg(view) AS BIGINT) = 4 AND completed_at IS NOT NULL)
    OR (CAST(sqlc.arg(view) AS BIGINT) = 5 AND completed_at IS NULL AND due_at IS NOT NULL AND due_at < CAST(sqlc.arg(now_ms) AS BIGINT)))
  AND (CAST(sqlc.arg(priority_filter) AS TEXT) = '' OR priority = CAST(sqlc.arg(priority_filter) AS TEXT))
  AND (CAST(sqlc.arg(category_filter) AS TEXT) = '' OR category = CAST(sqlc.arg(category_filter) AS TEXT))
  AND (CAST(sqlc.arg(has_cursor) AS BIGINT) = 0 OR
    (CASE WHEN completed_at IS NOT NULL THEN 1 ELSE 0 END,
      CASE priority WHEN 'high' THEN 0 WHEN 'medium' THEN 1 WHEN 'low' THEN 2 ELSE 3 END,
      COALESCE(due_at, 9223372036854775807), -created_at, id) >
    (CAST(sqlc.arg(done) AS BIGINT), CAST(sqlc.arg(priority_rank) AS BIGINT), CAST(sqlc.arg(due) AS BIGINT),
      -CAST(sqlc.arg(created) AS BIGINT), CAST(sqlc.arg(cursor_id) AS TEXT)))
ORDER BY completed_at IS NOT NULL,
  CASE priority WHEN 'high' THEN 0 WHEN 'medium' THEN 1 WHEN 'low' THEN 2 ELSE 3 END,
  COALESCE(due_at, 9223372036854775807), created_at DESC, id
LIMIT sqlc.arg(page_limit);

-- name: CountTaskGroups :one
SELECT
  CAST(COALESCE(SUM(CASE WHEN completed_at IS NULL AND due_at IS NOT NULL AND due_at < CAST(sqlc.arg(now_ms) AS BIGINT) THEN 1 ELSE 0 END), 0) AS BIGINT) AS overdue,
  CAST(COALESCE(SUM(CASE WHEN completed_at IS NULL AND due_at >= CAST(sqlc.arg(now_ms) AS BIGINT) AND due_at < CAST(sqlc.arg(end_today) AS BIGINT) THEN 1 ELSE 0 END), 0) AS BIGINT) AS today,
  CAST(COALESCE(SUM(CASE WHEN completed_at IS NULL AND due_at >= CAST(sqlc.arg(end_today) AS BIGINT) AND due_at < CAST(sqlc.arg(end_week) AS BIGINT) THEN 1 ELSE 0 END), 0) AS BIGINT) AS week,
  CAST(COALESCE(SUM(CASE WHEN completed_at IS NULL AND due_at >= CAST(sqlc.arg(end_week) AS BIGINT) THEN 1 ELSE 0 END), 0) AS BIGINT) AS later,
  CAST(COALESCE(SUM(CASE WHEN completed_at IS NULL AND due_at IS NULL THEN 1 ELSE 0 END), 0) AS BIGINT) AS none,
  CAST(COALESCE(SUM(CASE WHEN completed_at IS NOT NULL THEN 1 ELSE 0 END), 0) AS BIGINT) AS done
FROM todo_tasks
WHERE (CAST(sqlc.arg(priority_filter) AS TEXT) = '' OR priority = CAST(sqlc.arg(priority_filter) AS TEXT))
  AND (CAST(sqlc.arg(category_filter) AS TEXT) = '' OR category = CAST(sqlc.arg(category_filter) AS TEXT));

-- name: UpdateTask :exec
UPDATE todo_tasks
SET title = ?, description = ?, due_at = ?, completed_at = ?, priority = ?, category = ?, updated_at = ?
WHERE id = ?;

-- name: CountCompletedBetween :one
SELECT CAST(COUNT(*) AS BIGINT)
FROM todo_tasks
WHERE completed_at >= ? AND completed_at < ?;

-- name: DeleteTask :execrows
DELETE FROM todo_tasks WHERE id = ?;

-- name: TodoSummary :one
SELECT
    CAST(COALESCE(SUM(CASE WHEN completed_at IS NULL THEN 1 ELSE 0 END), 0) AS BIGINT) AS open,
    CAST(COALESCE(SUM(CASE WHEN completed_at IS NULL AND due_at < ? THEN 1 ELSE 0 END), 0) AS BIGINT) AS overdue,
    CAST(COALESCE(SUM(CASE WHEN completed_at IS NOT NULL THEN 1 ELSE 0 END), 0) AS BIGINT) AS completed
FROM todo_tasks;

-- name: ListDueTasks :many
SELECT id, title, due_at FROM todo_tasks
WHERE completed_at IS NULL AND due_at IS NOT NULL AND due_at <= sqlc.arg(scan_before)
  AND (CAST(sqlc.arg(has_cursor) AS BIGINT) = 0 OR
    (due_at, id) > (CAST(sqlc.arg(cursor_due) AS BIGINT), CAST(sqlc.arg(cursor_id) AS TEXT)))
ORDER BY due_at, id LIMIT 100;

-- name: ListAgendaTasks :many
SELECT id, title, description, due_at, completed_at, created_at, updated_at, priority, category
FROM todo_tasks
WHERE completed_at IS NULL AND due_at IS NOT NULL AND due_at < CAST(sqlc.arg(due_before) AS BIGINT)
ORDER BY due_at, id
LIMIT sqlc.arg(row_limit);

-- name: CountAgendaTasks :one
SELECT
    CAST(COUNT(*) AS BIGINT) AS total,
    CAST(COALESCE(SUM(CASE WHEN due_at < CAST(sqlc.arg(now_ms) AS BIGINT) THEN 1 ELSE 0 END), 0) AS BIGINT) AS overdue
FROM todo_tasks
WHERE completed_at IS NULL AND due_at IS NOT NULL AND due_at < CAST(sqlc.arg(due_before) AS BIGINT);
