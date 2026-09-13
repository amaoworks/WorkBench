-- name: GetFutu :one
SELECT host, port, enabled, allow_non_local, last_error, updated_at
FROM investment_futu WHERE id = 1;

-- name: UpsertFutu :exec
INSERT INTO investment_futu(id, host, port, enabled, allow_non_local, last_error, updated_at)
VALUES (1, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    host = excluded.host,
    port = excluded.port,
    enabled = excluded.enabled,
    allow_non_local = excluded.allow_non_local,
    last_error = excluded.last_error,
    updated_at = excluded.updated_at;

-- name: ListFutuBars :many
SELECT symbol, resolution, time_ms, open, high, low, close, volume
FROM investment_futu_bars
WHERE symbol = ? AND resolution = ? AND time_ms >= ? AND time_ms < ?
ORDER BY time_ms;

-- name: OldestFutuBarTime :one
SELECT time_ms FROM investment_futu_bars
WHERE symbol = ? AND resolution = ?
ORDER BY time_ms ASC LIMIT 1;

-- name: UpsertFutuBar :exec
INSERT INTO investment_futu_bars(symbol, resolution, time_ms, open, high, low, close, volume)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(symbol, resolution, time_ms) DO UPDATE SET
    open = excluded.open,
    high = excluded.high,
    low = excluded.low,
    close = excluded.close,
    volume = excluded.volume;
