-- name: RecoverInterruptedJobRuns :exec
UPDATE scheduled_job_runs
SET status = 'failed', finished_at = ?, error = 'workbench restarted during execution'
WHERE status IN ('pending', 'running');

-- name: GetScheduledJobNextRun :one
SELECT next_run_at FROM scheduled_jobs WHERE id = ?;

-- name: UpsertScheduledJob :exec
INSERT INTO scheduled_jobs(
    id, module, schedule_kind, schedule_expr, timezone, timeout_ms,
    overlap_policy, misfire_policy, max_attempts, enabled,
    definition_hash, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    module = excluded.module,
    schedule_kind = excluded.schedule_kind,
    schedule_expr = excluded.schedule_expr,
    timezone = excluded.timezone,
    timeout_ms = excluded.timeout_ms,
    overlap_policy = excluded.overlap_policy,
    misfire_policy = excluded.misfire_policy,
    max_attempts = excluded.max_attempts,
    definition_hash = excluded.definition_hash,
    updated_at = excluded.updated_at;

-- name: InsertScheduledJobRun :exec
INSERT INTO scheduled_job_runs(
    id, job_id, scheduled_at, started_at, attempt, status
) VALUES (?, ?, ?, ?, ?, 'running');

-- name: FinishScheduledJobRun :exec
UPDATE scheduled_job_runs
SET status = sqlc.arg(status), finished_at = sqlc.arg(finished_at),
    error = NULLIF(sqlc.arg(error), '')
WHERE id = sqlc.arg(id);

-- name: MarkScheduledJobLastRun :exec
UPDATE scheduled_jobs SET last_run_at = ?, updated_at = ? WHERE id = ?;

-- name: GetScheduledJobMaxAttempt :one
SELECT CAST(COALESCE(MAX(attempt), 0) AS INTEGER) FROM scheduled_job_runs
WHERE job_id = ? AND scheduled_at = ?;

-- name: ClearScheduledJobNextRun :exec
UPDATE scheduled_jobs SET next_run_at = NULL, updated_at = ? WHERE id = ?;

-- name: SetScheduledJobNextRun :exec
UPDATE scheduled_jobs SET next_run_at = ?, updated_at = ? WHERE id = ?;
