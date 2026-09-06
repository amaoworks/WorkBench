-- name: ListQuotes :many
SELECT symbol, name, price_cents, change_bps, as_of FROM investment_quotes ORDER BY symbol;

-- name: SaveQuote :execrows
INSERT INTO investment_quotes(symbol, name, price_cents, change_bps, as_of) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(symbol) DO UPDATE SET name = excluded.name, price_cents = excluded.price_cents,
    change_bps = excluded.change_bps, as_of = excluded.as_of
WHERE excluded.as_of > investment_quotes.as_of;

-- name: GetSummary :one
SELECT content, created_at FROM investment_summary WHERE id = 1;

-- name: SaveSummary :exec
INSERT INTO investment_summary(id, content, created_at) VALUES (1, ?, ?)
ON CONFLICT(id) DO UPDATE SET content = excluded.content, created_at = excluded.created_at;
