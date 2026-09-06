package investment

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"workbench/internal/capabilities/ai"
	"workbench/internal/capabilities/notifications"
	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
	"workbench/internal/foundation/events"
)

type fixedQuotes struct{}

func (fixedQuotes) Snapshot(context.Context) ([]Quote, error) {
	return []Quote{{Symbol: "TEST", Name: "Test", PriceCents: 10000, ChangeBPS: 300, AsOf: time.Unix(1000, 0).UTC()}}, nil
}

type brokenEvents struct{}

func (brokenEvents) Publish(context.Context, contracts.NewEvent) (contracts.EventID, error) {
	return "", errors.New("unavailable")
}
func (brokenEvents) PublishTx(context.Context, *sql.Tx, contracts.NewEvent) (contracts.EventID, error) {
	return "", errors.New("unavailable")
}

func TestSyncIsAtomicAndRepeatedSnapshotsDoNotPublishAgain(t *testing.T) {
	ctx := context.Background()
	db, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	store := events.NewStore(db.SQL())
	notices, err := notifications.NewService(db.SQL(), store)
	if err != nil {
		t.Fatal(err)
	}
	module, err := New(Dependencies{DB: db.SQL(), Events: brokenEvents{}, Notifications: notices, AI: ai.NewTextService(), Quotes: fixedQuotes{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateModule(ctx, "investment", module.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := module.Sync(ctx); err == nil {
		t.Fatal("expected publisher failure")
	}
	overview, err := module.readOverview(ctx)
	if err != nil || len(overview.Items) != 0 {
		t.Fatal("business write survived failed event")
	}
	module.deps.Events = store
	for i := 0; i < 2; i++ {
		if err := module.Sync(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM events_log WHERE source_module='investment'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("events=%d error=%v", count, err)
	}
	var event contracts.Event
	var payload string
	if err := db.SQL().QueryRow("SELECT id,payload_json FROM events_log WHERE source_module='investment'").Scan(&event.ID, &payload); err != nil {
		t.Fatal(err)
	}
	event.Payload = []byte(payload)
	event.SchemaVersion = 1
	for i := 0; i < 2; i++ {
		if err := module.priceAlert(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM notifications WHERE source_module='investment'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("notices=%d error=%v", count, err)
	}
}
