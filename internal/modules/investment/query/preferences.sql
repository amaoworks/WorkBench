-- name: ListPreferences :many
SELECT name, content, revision FROM investment_preferences WHERE user_id=sqlc.arg(user_id) ORDER BY name;

-- name: GetPreference :one
SELECT name, content, revision, writer, base_revision, sequence
FROM investment_preferences WHERE user_id=sqlc.arg(user_id) AND name = sqlc.arg(name);

-- name: SavePreference :one
INSERT INTO investment_preferences(user_id,name, content, revision, writer, base_revision, sequence)
SELECT sqlc.arg(user_id), sqlc.arg(name), sqlc.arg(content), 1, sqlc.arg(writer), CAST(sqlc.arg(base_revision) AS BIGINT), CAST(sqlc.arg(sequence) AS BIGINT)
WHERE CAST(sqlc.arg(base_revision) AS BIGINT) = CAST(0 AS BIGINT) OR EXISTS (
    SELECT 1 FROM investment_preferences WHERE user_id=sqlc.arg(user_id) AND name = sqlc.arg(name)
)
ON CONFLICT(user_id,name) DO UPDATE SET
    content = excluded.content,
    revision = investment_preferences.revision + 1,
    writer = excluded.writer,
    base_revision = excluded.base_revision,
    sequence = excluded.sequence
WHERE investment_preferences.revision = excluded.base_revision OR (
    investment_preferences.writer = excluded.writer AND
    investment_preferences.base_revision = excluded.base_revision AND
    investment_preferences.sequence < excluded.sequence
)
RETURNING revision, sequence;
