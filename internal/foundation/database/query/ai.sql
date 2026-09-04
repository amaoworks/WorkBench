-- name: InsertAIToolCall :execrows
INSERT INTO ai_tool_calls(
    id, conversation_id, message_id, tool_name, module, risk,
    arguments_json, idempotency_key, confirmation_at, status, created_at
) VALUES (
    sqlc.arg(id), NULLIF(sqlc.arg(conversation_id), ''), NULLIF(sqlc.arg(message_id), ''),
    sqlc.arg(tool_name), sqlc.arg(module), sqlc.arg(risk), sqlc.arg(arguments_json),
    NULLIF(sqlc.arg(idempotency_key), ''), sqlc.arg(confirmation_at), 'running', sqlc.arg(created_at)
)
ON CONFLICT(tool_name, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING;

-- name: FinishAIToolCall :exec
UPDATE ai_tool_calls
SET status = sqlc.arg(status), result_json = sqlc.arg(result_json),
    error = NULLIF(sqlc.arg(error), ''), completed_at = sqlc.arg(completed_at)
WHERE id = sqlc.arg(id);

-- name: GetAIToolCallResult :one
SELECT status, result_json, error FROM ai_tool_calls
WHERE tool_name = ? AND idempotency_key = ?;
