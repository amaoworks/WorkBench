-- name: UpsertNetWorthSnapshot :exec
INSERT INTO investment_networth_snapshots(user_id,account_hash,day,net_worth,cash,invested,day_pnl,captured_at)
VALUES(sqlc.arg(user_id),sqlc.arg(account_hash),sqlc.arg(day),sqlc.arg(net_worth),sqlc.arg(cash),sqlc.arg(invested),sqlc.arg(day_pnl),sqlc.arg(captured_at))
ON CONFLICT(user_id,account_hash,day) DO UPDATE SET net_worth=excluded.net_worth,cash=excluded.cash,invested=excluded.invested,day_pnl=excluded.day_pnl,captured_at=excluded.captured_at;

-- name: AddNetWorthAccountScope :exec
INSERT INTO investment_networth_account_scopes(user_id,authorized_at,account_hash)
VALUES(sqlc.arg(user_id),sqlc.arg(authorized_at),sqlc.arg(account_hash))
ON CONFLICT(user_id,authorized_at,account_hash) DO NOTHING;

-- name: DeleteNetWorthAccountScope :exec
DELETE FROM investment_networth_account_scopes WHERE user_id=sqlc.arg(user_id) AND authorized_at=sqlc.arg(authorized_at);

-- name: ListNetWorthSnapshots :many
SELECT s.* FROM investment_networth_snapshots s
JOIN investment_networth_account_scopes a ON a.account_hash=s.account_hash AND a.user_id=s.user_id
WHERE s.user_id=sqlc.arg(user_id) AND a.authorized_at=CAST(sqlc.arg(authorized_at) AS BIGINT)
AND (CAST(sqlc.arg(start_day) AS TEXT)='' OR s.day>=CAST(sqlc.arg(start_day) AS TEXT))
ORDER BY s.day,s.account_hash;

-- name: ListNetWorthAccounts :many
SELECT account_hash FROM investment_networth_account_scopes
WHERE user_id=sqlc.arg(user_id) AND authorized_at=CAST(sqlc.arg(authorized_at) AS BIGINT)
ORDER BY account_hash;
