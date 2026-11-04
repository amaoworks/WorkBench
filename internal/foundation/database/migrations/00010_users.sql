-- +goose Up
CREATE TABLE auth_users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'user')),
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
    credential_version INTEGER NOT NULL,
    must_change_password INTEGER NOT NULL CHECK (must_change_password IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
INSERT INTO auth_users(id, username, display_name, password_hash, role, status, credential_version, must_change_password, created_at, updated_at)
SELECT 'owner', 'admin', '管理员', password_hash, 'admin', 'active', updated_at, must_change_password, updated_at, updated_at FROM auth_credentials WHERE id = 1;
DROP TABLE auth_credentials;
DELETE FROM auth_browser_grants;
DELETE FROM auth_refresh_tokens;
DELETE FROM auth_browser_sessions;
DELETE FROM auth_device_sessions;
DELETE FROM sessions;
ALTER TABLE sessions ADD COLUMN user_id TEXT NOT NULL DEFAULT 'owner';
ALTER TABLE auth_device_sessions ADD COLUMN user_id TEXT NOT NULL DEFAULT 'owner';
ALTER TABLE auth_browser_sessions ADD COLUMN user_id TEXT NOT NULL DEFAULT 'owner';
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_device_sessions_user ON auth_device_sessions(user_id);
CREATE INDEX idx_browser_sessions_user ON auth_browser_sessions(user_id);

-- +goose Down
CREATE TABLE auth_rollback_guard (other_users INTEGER NOT NULL CHECK (other_users = 0));
INSERT INTO auth_rollback_guard SELECT COUNT(*) FROM auth_users WHERE id <> 'owner';
DROP TABLE auth_rollback_guard;
CREATE TABLE auth_credentials (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    password_hash TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1))
);
INSERT INTO auth_credentials(id, password_hash, updated_at, must_change_password)
SELECT 1, password_hash, credential_version, must_change_password FROM auth_users WHERE id = 'owner';
DELETE FROM auth_browser_grants;
DELETE FROM auth_refresh_tokens;
DELETE FROM auth_browser_sessions;
DELETE FROM auth_device_sessions;
DELETE FROM sessions;
DROP INDEX idx_browser_sessions_user;
DROP INDEX idx_device_sessions_user;
DROP INDEX idx_sessions_user;
ALTER TABLE auth_browser_sessions DROP COLUMN user_id;
ALTER TABLE auth_device_sessions DROP COLUMN user_id;
ALTER TABLE sessions DROP COLUMN user_id;
DROP TABLE auth_users;
