package database

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"workbench/internal/contracts"
)

func TestOpenMigrateAndBackup(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data.db")
	database, err := Open(ctx, Config{Path: dbPath})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := database.MigrateCore(ctx); err != nil {
		t.Fatalf("MigrateCore() error = %v", err)
	}

	var journalMode string
	if err := database.SQL().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	_, err = database.SQL().ExecContext(ctx, `
		INSERT INTO modules(id, name, version, contract_version, enabled, installed_at, updated_at)
		VALUES ('todo', 'Todo', '0.1.0', 1, 1, 1, 1)`)
	if err != nil {
		t.Fatalf("insert module: %v", err)
	}

	backupPath := filepath.Join(t.TempDir(), "backup.db")
	if err := database.Backup(ctx, backupPath); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}
	if err := IntegrityCheck(ctx, backupPath); err != nil {
		t.Fatalf("IntegrityCheck() error = %v", err)
	}
}

func TestOpenRejectsSecondProcessLock(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "data.db")
	first, err := Open(context.Background(), Config{Path: dbPath})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	second, err := Open(ctx, Config{Path: dbPath})
	if err == nil {
		_ = second.Close()
		t.Fatal("second Open() succeeded, want lock error")
	}
}

func TestModuleMigrationsUseIndependentVersionTable(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatalf("MigrateCore() error = %v", err)
	}

	migrations := fstest.MapFS{
		"migrations/00001_todo.sql": &fstest.MapFile{Data: []byte(`
-- +goose Up
CREATE TABLE todo_test(id TEXT PRIMARY KEY);
-- +goose Down
DROP TABLE todo_test;
`)},
	}
	set := contracts.MigrationSet{FS: fs.FS(migrations), Dir: "migrations"}
	if err := database.MigrateModule(ctx, "todo", set); err != nil {
		t.Fatalf("MigrateModule() error = %v", err)
	}

	var table string
	err = database.SQL().QueryRowContext(ctx, `
		SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'goose_module_todo_version'`).Scan(&table)
	if err != nil {
		t.Fatalf("module version table missing: %v", err)
	}
}

func TestMigrateCoreUpgradesVersionOneDatabaseWithoutDataLoss(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	initial, err := fs.ReadFile(coreMigrations, "migrations/00001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	versionOne := fstest.MapFS{
		"migrations/00001_initial.sql": &fstest.MapFile{Data: initial},
	}
	if err := migrate(ctx, database.SQL(), versionOne, "migrations", "goose_core_version"); err != nil {
		t.Fatalf("install version-one fixture: %v", err)
	}
	if _, err := database.SQL().Exec(`
		INSERT INTO ai_conversations(id, title, created_at, updated_at)
		VALUES ('conversation-one', 'preserve me', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatalf("upgrade core database: %v", err)
	}
	var title string
	if err := database.SQL().QueryRow("SELECT title FROM ai_conversations WHERE id = 'conversation-one'").Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "preserve me" {
		t.Fatalf("conversation title after upgrade = %q", title)
	}
	for _, index := range []string{"idx_ai_conversations_updated", "idx_ai_tool_calls_status"} {
		var count int
		if err := database.SQL().QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("upgraded index %q count = %d, want 1", index, count)
		}
	}
}
