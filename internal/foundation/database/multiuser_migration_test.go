package database

import (
	"context"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMultiUserCoreUpgradePreservesOwnerDataAndRevokesLegacySessions(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, Config{Path: filepath.Join(t.TempDir(), "legacy.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	legacy := fstest.MapFS{}
	entries, err := fs.ReadDir(coreMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil || version > 9 {
			continue
		}
		path := "migrations/" + entry.Name()
		data, err := fs.ReadFile(coreMigrations, path)
		if err != nil {
			t.Fatal(err)
		}
		legacy[path] = &fstest.MapFile{Data: data}
	}
	if err := migrate(ctx, database.SQL(), legacy, "migrations", "goose_core_version"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO auth_credentials(id,password_hash,updated_at,must_change_password) VALUES(1,'preserved-password-hash',123456,0)`,
		`INSERT INTO sessions(token,data,expiry) VALUES('legacy-session',X'01',2000000000000)`,
		`INSERT INTO workspace_settings(section,value) VALUES('appearance','{"theme":"dark","motion":"reduced","palette":"claude"}')`,
		`INSERT INTO workspace_settings(section,value) VALUES('dashboard','[]')`,
		`INSERT INTO workspace_settings(section,value) VALUES('ai','{"enabled":false,"apiKey":"preserved-secret"}')`,
		`INSERT INTO events_log(id,topic,schema_version,source_module,payload_json,occurred_at,available_at,created_at) VALUES('legacy-event','todo.task.created',1,'todo','{}',1,1,1)`,
		`INSERT INTO notifications(id,source_module,severity,title,content,idempotency_key,created_at) VALUES('legacy-note','todo','info','Private reminder','body','legacy-key',1)`,
		`INSERT INTO ai_conversations(id,title,created_at,updated_at) VALUES('legacy-chat','Private chat',1,1)`,
		`INSERT INTO ai_messages(id,conversation_id,role,content_json,created_at) VALUES('legacy-message','legacy-chat','user','"Private message"',1)`,
	} {
		if _, err := database.SQL().ExecContext(ctx, statement); err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	var username, hash, role, status string
	var version, mustChange int64
	if err := database.SQL().QueryRowContext(ctx, `SELECT username,password_hash,role,status,credential_version,must_change_password FROM auth_users WHERE id='owner'`).Scan(&username, &hash, &role, &status, &version, &mustChange); err != nil {
		t.Fatal(err)
	}
	if username != "admin" || hash != "preserved-password-hash" || role != "admin" || status != "active" || version != 123456 || mustChange != 0 {
		t.Fatalf("invalid migrated owner: %s %s %s %d %d", username, role, status, version, mustChange)
	}
	for _, statement := range []string{
		`SELECT COUNT(*) FROM sessions`,
		`SELECT COUNT(*) FROM auth_device_sessions`,
		`SELECT COUNT(*) FROM auth_browser_sessions`,
		`SELECT COUNT(*) FROM auth_browser_grants`,
		`SELECT COUNT(*) FROM workspace_settings WHERE section IN ('appearance','dashboard')`,
	} {
		var count int
		if err := database.SQL().QueryRowContext(ctx, statement).Scan(&count); err != nil || count != 0 {
			t.Fatalf("legacy state not revoked/moved (%s): count=%d error=%v", statement, count, err)
		}
	}
	for _, statement := range []string{
		`SELECT COUNT(*) FROM user_settings WHERE user_id='owner' AND section='appearance' AND value LIKE '%dark%'`,
		`SELECT COUNT(*) FROM user_settings WHERE user_id='owner' AND section='dashboard'`,
		`SELECT COUNT(*) FROM workspace_settings WHERE section='ai' AND value LIKE '%preserved-secret%'`,
		`SELECT COUNT(*) FROM notifications WHERE id='legacy-note' AND recipient_id='owner'`,
		`SELECT COUNT(*) FROM ai_conversations WHERE id='legacy-chat' AND user_id='owner'`,
		`SELECT COUNT(*) FROM ai_messages WHERE id='legacy-message' AND conversation_id='legacy-chat'`,
		`SELECT COUNT(*) FROM events_log WHERE id='legacy-event' AND user_id='owner'`,
	} {
		var count int
		if err := database.SQL().QueryRowContext(ctx, statement).Scan(&count); err != nil || count != 1 {
			t.Fatalf("owner data not preserved (%s): count=%d error=%v", statement, count, err)
		}
	}
}
