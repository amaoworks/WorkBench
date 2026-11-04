-- name: CreateTradeAudit :exec
INSERT INTO investment_trade_audit(id,user_id,account_hash,method,path,outcome,http_status,created_at,updated_at)
VALUES(sqlc.arg(id),sqlc.arg(user_id),sqlc.arg(account_hash),sqlc.arg(method),sqlc.arg(path),'started',0,sqlc.arg(created_at),sqlc.arg(created_at));

-- name: FinishTradeAudit :exec
UPDATE investment_trade_audit SET outcome=sqlc.arg(outcome),http_status=sqlc.arg(http_status),updated_at=sqlc.arg(updated_at)
WHERE id=sqlc.arg(id) AND user_id=sqlc.arg(user_id);
