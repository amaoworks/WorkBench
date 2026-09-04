-- +goose Up
CREATE INDEX idx_ai_conversations_updated
ON ai_conversations(updated_at DESC, id DESC);

CREATE INDEX idx_ai_tool_calls_status
ON ai_tool_calls(status, created_at DESC);

-- +goose Down
DROP INDEX idx_ai_tool_calls_status;
DROP INDEX idx_ai_conversations_updated;
