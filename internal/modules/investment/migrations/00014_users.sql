-- +goose Up
CREATE TABLE investment_schwab_app (
 id INTEGER PRIMARY KEY CHECK(id = 1), app_key TEXT NOT NULL DEFAULT '', app_secret TEXT NOT NULL DEFAULT '',
 callback_url TEXT NOT NULL DEFAULT '', version BIGINT NOT NULL DEFAULT 1
);
INSERT INTO investment_schwab_app(id,app_key,app_secret,callback_url) SELECT 1,app_key,app_secret,callback_url FROM investment_schwab WHERE id=1;
CREATE TABLE investment_schwab_connections (
 user_id TEXT PRIMARY KEY, access_token TEXT NOT NULL DEFAULT '', refresh_token TEXT NOT NULL DEFAULT '',
 token_expires_at BIGINT NOT NULL DEFAULT 0, streamer_info TEXT NOT NULL DEFAULT '',
 oauth_state TEXT NOT NULL DEFAULT '', oauth_state_expires_at BIGINT NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '', updated_at BIGINT NOT NULL DEFAULT 0,
 reauthorization_required BIGINT NOT NULL DEFAULT 0, authorized_at BIGINT NOT NULL DEFAULT 0,
 app_version BIGINT NOT NULL DEFAULT 1, connection_version BIGINT NOT NULL DEFAULT 1
);
INSERT INTO investment_schwab_connections(user_id,access_token,refresh_token,token_expires_at,streamer_info,last_error,updated_at,reauthorization_required,authorized_at)
SELECT 'owner',access_token,refresh_token,token_expires_at,streamer_info,last_error,updated_at,reauthorization_required,authorized_at FROM investment_schwab WHERE id=1;
DROP TABLE investment_schwab;
ALTER TABLE investment_price_rules ADD COLUMN user_id TEXT NOT NULL DEFAULT 'owner';
ALTER TABLE investment_price_triggers ADD COLUMN user_id TEXT NOT NULL DEFAULT 'owner';
CREATE INDEX investment_price_rules_user ON investment_price_rules(user_id);
CREATE INDEX investment_price_triggers_user_time ON investment_price_triggers(user_id,triggered_at,id);
ALTER TABLE investment_watchlists RENAME TO investment_watchlists_old;
CREATE TABLE investment_watchlists (
 user_id TEXT PRIMARY KEY, revision BIGINT NOT NULL DEFAULT 0, writer TEXT NOT NULL DEFAULT '',
 base_revision BIGINT NOT NULL DEFAULT 0, sequence BIGINT NOT NULL DEFAULT 0, content TEXT NOT NULL DEFAULT 'null'
);
INSERT INTO investment_watchlists SELECT 'owner',revision,writer,base_revision,sequence,content FROM investment_watchlists_old;
DROP TABLE investment_watchlists_old;
ALTER TABLE investment_price_monitor RENAME TO investment_price_monitor_old;
CREATE TABLE investment_price_monitor(user_id TEXT PRIMARY KEY, status TEXT NOT NULL DEFAULT 'waiting', checked_at BIGINT, last_error TEXT NOT NULL DEFAULT '');
INSERT INTO investment_price_monitor SELECT 'owner',status,checked_at,last_error FROM investment_price_monitor_old;
DROP TABLE investment_price_monitor_old;
ALTER TABLE investment_preferences RENAME TO investment_preferences_old;
CREATE TABLE investment_preferences (
 user_id TEXT NOT NULL, name TEXT NOT NULL, content TEXT NOT NULL CHECK(json_valid(content)), revision BIGINT NOT NULL DEFAULT 1,
 writer TEXT NOT NULL, base_revision BIGINT NOT NULL, sequence BIGINT NOT NULL, PRIMARY KEY(user_id,name)
);
INSERT INTO investment_preferences SELECT 'owner',name,content,revision,writer,base_revision,sequence FROM investment_preferences_old;
DROP TABLE investment_preferences_old;
ALTER TABLE investment_networth_snapshots RENAME TO investment_networth_snapshots_old;
CREATE TABLE investment_networth_snapshots (
 user_id TEXT NOT NULL, account_hash TEXT NOT NULL, day TEXT NOT NULL, net_worth REAL NOT NULL, cash REAL NOT NULL,
 invested REAL NOT NULL, day_pnl REAL NOT NULL, captured_at BIGINT NOT NULL, PRIMARY KEY(user_id,account_hash,day)
);
INSERT INTO investment_networth_snapshots SELECT 'owner',account_hash,day,net_worth,cash,invested,day_pnl,captured_at FROM investment_networth_snapshots_old;
DROP TABLE investment_networth_snapshots_old;
CREATE INDEX idx_investment_networth_day ON investment_networth_snapshots(user_id,day);
ALTER TABLE investment_networth_account_scopes RENAME TO investment_networth_account_scopes_old;
CREATE TABLE investment_networth_account_scopes(user_id TEXT NOT NULL, authorized_at BIGINT NOT NULL, account_hash TEXT NOT NULL, PRIMARY KEY(user_id,authorized_at,account_hash));
INSERT INTO investment_networth_account_scopes SELECT 'owner',authorized_at,account_hash FROM investment_networth_account_scopes_old;
DROP TABLE investment_networth_account_scopes_old;
CREATE TABLE investment_trade_audit (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, account_hash TEXT NOT NULL, method TEXT NOT NULL, path TEXT NOT NULL,
 outcome TEXT NOT NULL, http_status BIGINT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL
);
CREATE INDEX investment_trade_audit_user ON investment_trade_audit(user_id,created_at);
-- +goose Down
-- Restore a pre-upgrade backup to return to the single-user schema.
CREATE TABLE investment_downgrade_requires_backup (
 blocked INTEGER NOT NULL CONSTRAINT investment_restore_backup_required CHECK(blocked=0)
);
INSERT INTO investment_downgrade_requires_backup(blocked) VALUES(1);
