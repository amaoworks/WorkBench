-- name: UpsertNetWorthSnapshot :exec
INSERT INTO investment_networth_snapshots(
  account_hash, day, net_worth, cash, invested, day_pnl, captured_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(account_hash, day) DO UPDATE SET
  net_worth = excluded.net_worth,
  cash = excluded.cash,
  invested = excluded.invested,
  day_pnl = excluded.day_pnl,
  captured_at = excluded.captured_at;

-- name: AddNetWorthAccountScope :exec
INSERT INTO investment_networth_account_scopes(authorized_at, account_hash)
VALUES (?, ?)
ON CONFLICT(authorized_at, account_hash) DO NOTHING;

-- name: DeleteNetWorthAccountScope :exec
DELETE FROM investment_networth_account_scopes
WHERE authorized_at = ?;

-- name: ListNetWorthSnapshots :many
SELECT s.account_hash, s.day, s.net_worth, s.cash, s.invested, s.day_pnl, s.captured_at
FROM investment_networth_snapshots s
JOIN investment_networth_account_scopes a ON a.account_hash = s.account_hash
WHERE a.authorized_at = CAST(sqlc.arg(authorized_at) AS BIGINT)
  AND (CAST(sqlc.arg(start_day) AS TEXT) = '' OR s.day >= CAST(sqlc.arg(start_day) AS TEXT))
ORDER BY s.day, s.account_hash;

-- name: ListNetWorthAccounts :many
SELECT account_hash
FROM investment_networth_account_scopes
WHERE authorized_at = CAST(sqlc.arg(authorized_at) AS BIGINT)
ORDER BY account_hash;
