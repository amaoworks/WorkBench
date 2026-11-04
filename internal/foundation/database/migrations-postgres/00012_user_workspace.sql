-- +goose Up
CREATE TABLE user_settings (
    user_id TEXT NOT NULL,
    section TEXT NOT NULL,
    value TEXT NOT NULL CHECK (value::json IS NOT NULL),
    PRIMARY KEY (user_id, section)
);
INSERT INTO user_settings(user_id, section, value)
SELECT 'owner', section, value FROM workspace_settings WHERE section IN ('appearance', 'dashboard');
DELETE FROM workspace_settings WHERE section IN ('appearance', 'dashboard');

ALTER TABLE ai_conversations ADD COLUMN user_id TEXT NOT NULL DEFAULT 'owner';
CREATE INDEX idx_ai_conversations_user ON ai_conversations(user_id, updated_at DESC, id DESC);
ALTER TABLE ai_tool_calls ADD COLUMN user_id TEXT NOT NULL DEFAULT 'owner';
DROP INDEX idx_ai_tool_calls_idempotency;
CREATE UNIQUE INDEX idx_ai_tool_calls_idempotency
ON ai_tool_calls(user_id, tool_name, idempotency_key)
WHERE idempotency_key IS NOT NULL;

-- +goose Down
CREATE TABLE user_workspace_rollback_guard (other_users BIGINT NOT NULL CHECK (other_users = 0));
INSERT INTO user_workspace_rollback_guard
SELECT (SELECT COUNT(*) FROM auth_users WHERE id <> 'owner')
     + (SELECT COUNT(*) FROM ai_conversations WHERE user_id <> 'owner')
     + (SELECT COUNT(*) FROM ai_tool_calls WHERE user_id <> 'owner')
     + (SELECT COUNT(*) FROM user_settings WHERE user_id <> 'owner');
DROP TABLE user_workspace_rollback_guard;
DROP INDEX idx_ai_tool_calls_idempotency;
ALTER TABLE ai_tool_calls DROP COLUMN user_id;
CREATE UNIQUE INDEX idx_ai_tool_calls_idempotency
ON ai_tool_calls(tool_name, idempotency_key)
WHERE idempotency_key IS NOT NULL;
DROP INDEX idx_ai_conversations_user;
ALTER TABLE ai_conversations DROP COLUMN user_id;
INSERT INTO workspace_settings(section, value)
SELECT section, value FROM user_settings WHERE user_id = 'owner'
ON CONFLICT(section) DO UPDATE SET value = excluded.value;
DROP TABLE user_settings;
