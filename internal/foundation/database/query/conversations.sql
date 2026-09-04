-- name: InsertConversation :exec
INSERT INTO ai_conversations(id, title, created_at, updated_at)
VALUES (?, ?, ?, ?);

-- name: TouchConversation :execrows
UPDATE ai_conversations SET updated_at = ?
WHERE id = ? AND archived_at IS NULL;

-- name: InsertUserMessage :exec
INSERT INTO ai_messages(id, conversation_id, role, content_json, created_at)
VALUES (?, ?, 'user', ?, ?);

-- name: InsertAssistantMessage :exec
INSERT INTO ai_messages(id, conversation_id, role, content_json, provider, model, created_at)
VALUES (?, ?, 'assistant', ?, 'openai', ?, ?);

-- name: ListRecentConversationMessages :many
SELECT role, content_json FROM (
    SELECT role, content_json, created_at, id FROM ai_messages
    WHERE conversation_id = ? AND role IN ('user', 'assistant')
    ORDER BY created_at DESC, id DESC LIMIT ?
) ORDER BY created_at, id;
