-- name: ListPriceRules :many
SELECT * FROM investment_price_rules ORDER BY created_at, id;

-- name: GetPriceRule :one
SELECT * FROM investment_price_rules WHERE id = ?;

-- name: CountPriceRules :one
SELECT COUNT(*) FROM investment_price_rules;

-- name: CreatePriceRule :exec
INSERT INTO investment_price_rules(id, symbol, direction, threshold_bps, enabled, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?);

-- name: UpdatePriceRule :execrows
UPDATE investment_price_rules SET symbol = ?, direction = ?, threshold_bps = ?, enabled = ?,
    version = version + 1, updated_at = ?, price = NULL, previous_close = NULL,
    change_percent = NULL, quote_at = NULL, checked_at = NULL, last_error = ''
WHERE id = ?;

-- name: DeletePriceRule :execrows
DELETE FROM investment_price_rules WHERE id = ?;

-- name: UpdatePriceRuleQuote :exec
UPDATE investment_price_rules SET price = ?, previous_close = ?, change_percent = ?, quote_at = ?, checked_at = ?, last_error = ?
WHERE id = ? AND version = ? AND enabled = 1;

-- name: GetPriceMonitor :one
SELECT status, checked_at, last_error FROM investment_price_monitor WHERE id = 1;

-- name: UpdatePriceMonitor :exec
UPDATE investment_price_monitor SET status = ?, checked_at = ?, last_error = ? WHERE id = 1;

-- name: CreatePriceTrigger :execrows
INSERT INTO investment_price_triggers(id, rule_id, trading_date, symbol, direction, threshold_bps,
    price, previous_close, change_percent, quote_at, triggered_at, notification_id)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(rule_id, trading_date) DO NOTHING;

-- name: SetPriceTriggerNotification :exec
UPDATE investment_price_triggers SET notification_id = ? WHERE id = ?;

-- name: ListPriceTriggers :many
SELECT * FROM investment_price_triggers
WHERE triggered_at < sqlc.arg(before_time) OR (triggered_at = sqlc.arg(before_time) AND id < sqlc.arg(before_id))
ORDER BY triggered_at DESC, id DESC LIMIT sqlc.arg(page_limit);
