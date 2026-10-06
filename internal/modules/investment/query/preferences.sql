-- name: ListPreferences :many
SELECT name, content, revision FROM investment_preferences ORDER BY name;

-- name: GetPreference :one
SELECT name, content, revision, writer, base_revision, sequence
FROM investment_preferences WHERE name = ?;

-- name: SavePreference :one
INSERT INTO investment_preferences(name, content, revision, writer, base_revision, sequence)
SELECT sqlc.arg(name), sqlc.arg(content), 1, sqlc.arg(writer), sqlc.arg(base_revision), sqlc.arg(sequence)
WHERE sqlc.arg(base_revision) = CAST(0 AS BIGINT) OR EXISTS (
    SELECT 1 FROM investment_preferences WHERE name = sqlc.arg(name)
)
ON CONFLICT(name) DO UPDATE SET
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
