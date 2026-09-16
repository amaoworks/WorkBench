package investment

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
)

func TestRemovingDemoTablesPreservesExistingSchwabCredentials(t *testing.T) {
	ctx := context.Background()
	db, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{}
	for _, name := range []string{"migrations/00001_investment.sql", "migrations/00002_schwab.sql"} {
		raw, err := migrations.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		old[name] = &fstest.MapFile{Data: raw}
	}
	if err := db.MigrateModule(ctx, "investment", contracts.MigrationSet{FS: old, Dir: "migrations"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`INSERT INTO investment_quotes VALUES('DEMO','Demo',100,0,1); INSERT INTO investment_summary VALUES(1,'Demo summary',1); INSERT INTO investment_schwab(id,app_key,app_secret,access_token,refresh_token,updated_at) VALUES(1,'key','secret','access','refresh',1)`); err != nil {
		t.Fatal(err)
	}
	module, err := New(Dependencies{DB: db.SQL()})
	if err != nil {
		t.Fatal(err)
	}
	defer module.Close()
	if err := db.MigrateModule(ctx, "investment", module.Migrations()); err != nil {
		t.Fatal(err)
	}
	rec, err := module.loadSchwab(ctx)
	if err != nil || rec.AppSecret != "secret" || rec.AccessToken != "access" || rec.RefreshToken != "refresh" {
		t.Fatal("Schwab credentials lost during upgrade", err)
	}
	var tables int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name IN ('investment_quotes','investment_summary')").Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("demo tables remain: %d %v", tables, err)
	}
}

func TestOvernightMigrationPreservesExistingEnabledChoice(t *testing.T) {
	ctx := context.Background()
	db, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{}
	entries, _ := migrations.ReadDir("migrations")
	for _, entry := range entries {
		if entry.Name() > "00005_futu.sql" {
			continue
		}
		name := "migrations/" + entry.Name()
		raw, err := migrations.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		old[name] = &fstest.MapFile{Data: raw}
	}
	if err := db.MigrateModule(ctx, "investment", contracts.MigrationSet{FS: old, Dir: "migrations"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`UPDATE investment_futu SET enabled=1,host='existing-opend',allow_non_local=1`); err != nil {
		t.Fatal(err)
	}
	m, err := New(Dependencies{DB: db.SQL()})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := db.MigrateModule(ctx, "investment", m.Migrations()); err != nil {
		t.Fatal(err)
	}
	rec, err := m.loadFutu(ctx)
	if err != nil || !rec.Enabled || !rec.OvernightEnabled || rec.Host != "127.0.0.1" || rec.AllowNonLocal {
		t.Fatalf("migration lost settings: %+v %v", rec, err)
	}
}
