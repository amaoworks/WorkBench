-- name: InsertConversation :exec
INSERT INTO ai_conversations(id, user_id, title, created_at, updated_at)
VALUES (?, ?, ?, ?, ?);

-- name: TouchConversation :execrows
UPDATE ai_conversations SET updated_at = ?
WHERE id = ? AND user_id = ? AND archived_at IS NULL;

-- name: ListConversations :many
SELECT id, title, created_at, updated_at
FROM ai_conversations
WHERE user_id = sqlc.arg(user_id) AND archived_at IS NULL
  AND (
    sqlc.arg(cursor_id) = ''
    OR updated_at < sqlc.arg(cursor_updated_at)
    OR (updated_at = sqlc.arg(cursor_updated_at) AND id < sqlc.arg(cursor_id))
  )
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(page_size);

-- name: GetActiveConversation :one
SELECT id, title, created_at, updated_at
FROM ai_conversations
WHERE id = ? AND user_id = ? AND archived_at IS NULL;

-- name: RenameConversation :execrows
UPDATE ai_conversations
SET title = sqlc.arg(title), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND archived_at IS NULL;

-- name: ArchiveConversation :execrows
UPDATE ai_conversations
SET archived_at = sqlc.arg(archived_at), updated_at = sqlc.arg(archived_at)
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND archived_at IS NULL;

-- name: InsertUserMessage :execrows
INSERT INTO ai_messages(id, conversation_id, role, content_json, created_at)
SELECT sqlc.arg(id), c.id, 'user', sqlc.arg(content_json), sqlc.arg(created_at)
FROM ai_conversations c
WHERE c.id = sqlc.arg(conversation_id) AND c.user_id = sqlc.arg(user_id) AND c.archived_at IS NULL;

-- name: InsertAssistantMessage :execrows
INSERT INTO ai_messages(id, conversation_id, role, content_json, provider, model, created_at)
SELECT sqlc.arg(id), c.id, 'assistant', sqlc.arg(content_json), 'openai', sqlc.narg(model), sqlc.arg(created_at)
FROM ai_conversations c
WHERE c.id = sqlc.arg(conversation_id) AND c.user_id = sqlc.arg(user_id) AND c.archived_at IS NULL;

-- name: ListRecentConversationMessages :many
SELECT role, content_json FROM (
    SELECT m.role, m.content_json, m.created_at, m.id
    FROM ai_messages m JOIN ai_conversations c ON c.id = m.conversation_id
    WHERE m.conversation_id = sqlc.arg(conversation_id) AND c.user_id = sqlc.arg(user_id)
      AND c.archived_at IS NULL AND m.role IN ('user', 'assistant')
    ORDER BY m.created_at DESC, m.id DESC LIMIT sqlc.arg(page_size)
) AS recent_messages ORDER BY created_at, id;

-- name: ListConversationMessagesPage :many
SELECT role, content_json, created_at, id FROM (
    SELECT m.role, m.content_json, m.created_at, m.id
    FROM ai_messages AS m
    JOIN ai_conversations AS c ON c.id = m.conversation_id
    WHERE m.conversation_id = sqlc.arg(conversation_id)
      AND c.user_id = sqlc.arg(user_id) AND c.archived_at IS NULL
      AND m.role IN ('user', 'assistant')
      AND (
        sqlc.arg(cursor_id) = ''
        OR m.created_at < sqlc.arg(cursor_created_at)
        OR (m.created_at = sqlc.arg(cursor_created_at) AND m.id < sqlc.arg(cursor_id))
      )
    ORDER BY m.created_at DESC, m.id DESC
    LIMIT sqlc.arg(page_size)
) AS recent_messages
ORDER BY created_at, id;
