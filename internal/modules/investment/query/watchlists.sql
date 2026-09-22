-- name: GetWatchlists :one
SELECT revision, writer, base_revision, sequence, content FROM investment_watchlists WHERE id = 1;

-- name: SaveWatchlists :one
UPDATE investment_watchlists
SET content = sqlc.arg(content), revision = revision + 1,
    writer = sqlc.arg(writer), base_revision = sqlc.arg(base_revision), sequence = sqlc.arg(sequence)
WHERE id = 1 AND (
    revision = sqlc.arg(base_revision) OR
    (writer = sqlc.arg(writer) AND base_revision = sqlc.arg(base_revision) AND sequence < sqlc.arg(sequence))
)
RETURNING revision, sequence;
