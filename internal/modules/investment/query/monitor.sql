-- name: ListPriceRules :many
SELECT * FROM investment_price_rules WHERE user_id=sqlc.arg(user_id) ORDER BY created_at,id;

-- name: GetPriceRule :one
SELECT * FROM investment_price_rules WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);

-- name: CountPriceRules :one
SELECT COUNT(*) FROM investment_price_rules WHERE user_id=sqlc.arg(user_id);

-- name: CreatePriceRule :exec
INSERT INTO investment_price_rules(user_id,id,symbol,direction,threshold_bps,enabled,created_at,updated_at)
VALUES(sqlc.arg(user_id),sqlc.arg(id),sqlc.arg(symbol),sqlc.arg(direction),sqlc.arg(threshold_bps),sqlc.arg(enabled),sqlc.arg(created_at),sqlc.arg(updated_at));

-- name: UpdatePriceRule :execrows
UPDATE investment_price_rules SET symbol=sqlc.arg(symbol),direction=sqlc.arg(direction),threshold_bps=sqlc.arg(threshold_bps),enabled=sqlc.arg(enabled),
version=version+1,updated_at=sqlc.arg(updated_at),price=NULL,previous_close=NULL,change_percent=NULL,quote_at=NULL,checked_at=NULL,last_error=''
WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);

-- name: DeletePriceRule :execrows
DELETE FROM investment_price_rules WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);

-- name: UpdatePriceRuleQuote :exec
UPDATE investment_price_rules SET price=sqlc.narg(price),previous_close=sqlc.narg(previous_close),change_percent=sqlc.narg(change_percent),quote_at=sqlc.narg(quote_at),checked_at=sqlc.narg(checked_at),last_error=sqlc.arg(last_error)
WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id) AND version=sqlc.arg(version) AND enabled=1;

-- name: GetPriceMonitor :one
SELECT status,checked_at,last_error FROM investment_price_monitor WHERE user_id=sqlc.arg(user_id);

-- name: UpdatePriceMonitor :exec
INSERT INTO investment_price_monitor(user_id,status,checked_at,last_error)
VALUES(sqlc.arg(user_id),sqlc.arg(status),sqlc.narg(checked_at),sqlc.arg(last_error))
ON CONFLICT(user_id) DO UPDATE SET status=excluded.status,checked_at=excluded.checked_at,last_error=excluded.last_error;

-- name: CreatePriceTrigger :execrows
INSERT INTO investment_price_triggers(user_id,id,rule_id,trading_date,symbol,direction,threshold_bps,price,previous_close,change_percent,quote_at,triggered_at,notification_id)
VALUES(sqlc.arg(user_id),sqlc.arg(id),sqlc.arg(rule_id),sqlc.arg(trading_date),sqlc.arg(symbol),sqlc.arg(direction),sqlc.arg(threshold_bps),sqlc.arg(price),sqlc.arg(previous_close),sqlc.arg(change_percent),sqlc.arg(quote_at),sqlc.arg(triggered_at),sqlc.arg(notification_id))
ON CONFLICT(rule_id,trading_date) DO NOTHING;

-- name: SetPriceTriggerNotification :exec
UPDATE investment_price_triggers SET notification_id=sqlc.arg(notification_id) WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);

-- name: ListPriceTriggers :many
SELECT * FROM investment_price_triggers
WHERE user_id=sqlc.arg(user_id) AND (triggered_at<sqlc.arg(before_time) OR (triggered_at=sqlc.arg(before_time) AND id<sqlc.arg(before_id)))
ORDER BY triggered_at DESC,id DESC LIMIT sqlc.arg(page_limit);
