-- name: GetWallosSettings :one
SELECT config, last_sync, last_error FROM todo_wallos_settings WHERE id = 1;

-- name: SaveWallosSettings :exec
INSERT INTO todo_wallos_settings(id, config) VALUES (1, ?)
ON CONFLICT(id) DO UPDATE SET config = excluded.config, last_sync = NULL, last_error = '';

-- name: SetWallosSyncStatus :exec
UPDATE todo_wallos_settings SET last_sync = ?, last_error = ? WHERE id = 1;

-- name: ClaimWallosOccurrence :execrows
INSERT INTO todo_wallos_occurrences(source, subscription_id, payment_date, task_id)
VALUES (?, ?, ?, ?) ON CONFLICT(source, subscription_id, payment_date)
DO UPDATE SET task_id = excluded.task_id
WHERE NOT EXISTS (SELECT 1 FROM todo_tasks WHERE id = todo_wallos_occurrences.task_id);

-- name: GetWallosOccurrenceTask :one
SELECT task_id FROM todo_wallos_occurrences
WHERE source = ? AND subscription_id = ? AND payment_date = ?;

-- name: UpdateWallosTaskContent :execrows
UPDATE todo_tasks SET title = sqlc.arg(title), description = sqlc.arg(description), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND (title != sqlc.arg(title) OR description != sqlc.arg(description));
