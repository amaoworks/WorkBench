-- name: GetWatchlists :one
SELECT revision, writer, base_revision, sequence, content FROM investment_watchlists WHERE user_id = ?;

-- name: SaveWatchlists :one
INSERT INTO investment_watchlists(user_id,content,revision,writer,base_revision,sequence)
SELECT sqlc.arg(user_id),sqlc.arg(content),1,sqlc.arg(writer),CAST(sqlc.arg(base_revision) AS BIGINT),CAST(sqlc.arg(sequence) AS BIGINT)
WHERE CAST(sqlc.arg(base_revision) AS BIGINT)=CAST(0 AS BIGINT) OR EXISTS(SELECT 1 FROM investment_watchlists WHERE user_id=sqlc.arg(user_id))
ON CONFLICT(user_id) DO UPDATE SET content=excluded.content,revision=investment_watchlists.revision+1,
writer=excluded.writer,base_revision=excluded.base_revision,sequence=excluded.sequence
WHERE investment_watchlists.revision=excluded.base_revision OR (
 investment_watchlists.writer=excluded.writer AND investment_watchlists.base_revision=excluded.base_revision AND investment_watchlists.sequence<excluded.sequence)
RETURNING revision,sequence;
