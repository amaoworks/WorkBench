-- +goose Up
DELETE FROM notifications WHERE source_module = 'investment' AND idempotency_key LIKE 'investment:price:%';
DELETE FROM events_log WHERE topic = 'investment.price.updated';
DELETE FROM scheduled_job_runs WHERE job_id = 'investment.sync';
DELETE FROM scheduled_jobs WHERE id = 'investment.sync';

-- +goose Down
