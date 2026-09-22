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
	// Reproduce an old workspace with active simulated reminders, events and
	// scheduler state, not just empty historical tables.
	if _, err := db.SQL().Exec(`
		INSERT INTO modules(id,name,version,contract_version,enabled,installed_at,updated_at) VALUES('investment','Investment','0.1',1,1,1,1);
		INSERT INTO events_log(id,topic,schema_version,source_module,payload_json,occurred_at,available_at,created_at) VALUES('demo-event','investment.price.updated',1,'investment','{}',1,1,1);
		INSERT INTO event_deliveries(event_id,consumer_id,status,updated_at) VALUES('demo-event','investment.price_alert','pending',1);
		INSERT INTO notifications(id,source_module,severity,title,content,idempotency_key,source_event_id,created_at) VALUES('demo-notice','investment','warning','Demo','Demo','investment:price:old','demo-event',1);
		INSERT INTO notifications(id,source_module,severity,title,content,idempotency_key,created_at) VALUES('real-todo','todo','info','Keep','Keep','todo:keep',1);
		INSERT INTO scheduled_jobs(id,module,schedule_kind,schedule_expr,timezone,timeout_ms,overlap_policy,misfire_policy,max_attempts,definition_hash,updated_at) VALUES('investment.sync','investment','interval','60000','UTC',30000,'skip','run_once',1,'old',1);
		INSERT INTO scheduled_job_runs(id,job_id,scheduled_at,attempt,status) VALUES('demo-run','investment.sync',1,1,'pending');
	`); err != nil {
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
	for _, query := range []string{
		"SELECT COUNT(*) FROM events_log WHERE topic='investment.price.updated'",
		"SELECT COUNT(*) FROM event_deliveries WHERE consumer_id='investment.price_alert'",
		"SELECT COUNT(*) FROM notifications WHERE id='demo-notice'",
		"SELECT COUNT(*) FROM scheduled_jobs WHERE id='investment.sync'",
		"SELECT COUNT(*) FROM scheduled_job_runs WHERE job_id='investment.sync'",
	} {
		var count int
		if err := db.SQL().QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("legacy data not cleaned: %s: %d %v", query, count, err)
		}
	}
	var kept int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM notifications WHERE id='real-todo'").Scan(&kept); err != nil || kept != 1 {
		t.Fatal("cleanup removed unrelated notification", err)
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
