package todo

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
)

func TestOwnershipMigrationBackfillsLegacyData(t *testing.T) {
	ctx := context.Background()
	db, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "legacy.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	legacy := fstest.MapFS{}
	for _, name := range []string{"00001_todo.sql", "00002_wallos.sql", "00003_priority_category.sql", "00004_revision_idempotency.sql"} {
		data, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		legacy["migrations/"+name] = &fstest.MapFile{Data: data}
	}
	if err := db.MigrateModule(ctx, "todo", contracts.MigrationSet{FS: legacy, Dir: "migrations"}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO todo_tasks(id, title, created_at, updated_at) VALUES ('legacy-task', 'Existing task', 1, 1)`,
		`INSERT INTO todo_create_requests(request_key, request_hash, response_json) VALUES ('same', 'hash', '{"id":"legacy-task"}')`,
		`INSERT INTO todo_wallos_settings(id, config, last_sync, last_error) VALUES (1, '{"apiKey":"legacy-secret"}', 123, 'old status')`,
		`INSERT INTO todo_wallos_occurrences(source, subscription_id, payment_date, task_id) VALUES ('source', '1', '2026-09-10', 'legacy-task')`,
	} {
		if _, err := db.SQL().ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MigrateModule(ctx, "todo", contracts.MigrationSet{FS: migrations, Dir: "migrations"}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"todo_tasks", "todo_create_requests", "todo_wallos_settings", "todo_wallos_occurrences"} {
		var owner string
		if err := db.SQL().QueryRowContext(ctx, "SELECT user_id FROM "+table).Scan(&owner); err != nil || owner != contracts.LegacyUserID {
			t.Fatalf("%s owner = %q, %v", table, owner, err)
		}
	}
	var config, lastError string
	var lastSync int64
	if err := db.SQL().QueryRowContext(ctx, "SELECT config, last_sync, last_error FROM todo_wallos_settings WHERE user_id = 'owner'").Scan(&config, &lastSync, &lastError); err != nil || config != `{"apiKey":"legacy-secret"}` || lastSync != 123 || lastError != "old status" {
		t.Fatalf("legacy configuration lost: %q %d %q %v", config, lastSync, lastError, err)
	}
	// Natural keys are unique within an owner, not across owners.
	for _, statement := range []string{
		`INSERT INTO todo_create_requests(user_id, request_key, request_hash, response_json) VALUES ('bob', 'same', 'new-hash', '{}')`,
		`INSERT INTO todo_wallos_settings(user_id, config) VALUES ('bob', '{}')`,
		`INSERT INTO todo_wallos_occurrences(user_id, source, subscription_id, payment_date, task_id) VALUES ('bob', 'source', '1', '2026-09-10', 'bob-task')`,
	} {
		if _, err := db.SQL().ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL().ExecContext(ctx, statement); err == nil {
			t.Fatalf("duplicate owner key accepted: %s", statement)
		}
	}
}
