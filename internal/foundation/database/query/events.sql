-- name: InsertEvent :exec
INSERT INTO events_log(
    id, topic, schema_version, source_module, aggregate_id,
    payload_json, occurred_at, available_at, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(topic), sqlc.arg(schema_version), sqlc.arg(source_module),
    NULLIF(sqlc.arg(aggregate_id), ''), sqlc.arg(payload_json), sqlc.arg(occurred_at),
    sqlc.arg(available_at), sqlc.arg(created_at)
);

-- name: GetClaimableDelivery :one
SELECT e.id AS event_id, e.topic, e.schema_version, e.source_module,
       COALESCE(e.aggregate_id, '') AS aggregate_id, e.occurred_at,
       e.payload_json, d.attempts
FROM event_deliveries d
JOIN events_log e ON e.id = d.event_id
WHERE d.consumer_id = ?
  AND (
      (d.status IN ('pending', 'retry') AND COALESCE(d.next_attempt_at, 0) <= ?)
      OR (d.status = 'running' AND COALESCE(d.locked_until, 0) <= ?)
  )
ORDER BY e.available_at, e.created_at
LIMIT 1;

-- name: MarkDeliveryRunning :execrows
UPDATE event_deliveries
SET status = 'running', attempts = ?, locked_until = ?,
    started_at = COALESCE(started_at, ?), updated_at = ?
WHERE event_id = ? AND consumer_id = ?;

-- name: MarkDeliverySucceeded :exec
UPDATE event_deliveries
SET status = 'succeeded', completed_at = ?, locked_until = NULL,
    last_error = NULL, updated_at = ?
WHERE event_id = ? AND consumer_id = ?;

-- name: MarkDeliveryFailed :exec
UPDATE event_deliveries
SET status = sqlc.arg(status), next_attempt_at = NULLIF(sqlc.arg(next_attempt_at), 0), locked_until = NULL,
    last_error = sqlc.arg(last_error), updated_at = sqlc.arg(updated_at)
WHERE event_id = sqlc.arg(event_id) AND consumer_id = sqlc.arg(consumer_id);
