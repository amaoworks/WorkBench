package app

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
	"workbench/internal/foundation/events"
)

func postgresEventsDatabase(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	cfg := postgresTestConfig(t)
	database, err := workbenchdb.Open(ctx, workbenchdb.Config{Driver: cfg.DBDriver, DSN: cfg.DatabaseURL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	return database.SQL()
}

func postgresEventDispatcher(t *testing.T, db *sql.DB, store *events.Store) *events.Dispatcher {
	t.Helper()
	dispatcher, err := events.NewDispatcher(db, store, []contracts.EventConsumer{{
		ID: "core.postgres", Module: "core", Topics: []string{"core.test.created"},
		Handler: func(context.Context, contracts.Event) error { return nil },
	}}, func(contracts.ModuleID) bool { return true }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher
}

func assertEventSerializationFailure(t *testing.T, err error) {
	t.Helper()
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "40001" {
		t.Fatalf("expected propagated serialization failure, got %v", err)
	}
}

func assertPostgresEventDelivery(t *testing.T, db *sql.DB, event contracts.EventID) {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM event_deliveries WHERE event_id = ? AND consumer_id = 'core.postgres'", event).Scan(&count); err != nil || count != 1 {
		t.Fatalf("event delivery count = %d, error = %v", count, err)
	}
}

func TestPostgresEventRegistrationInterleavings(t *testing.T) {
	event := contracts.NewEvent{Topic: "core.test.created", SourceModule: "core", SchemaVersion: 1}
	t.Run("publication_snapshot_precedes_registration", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		db := postgresEventsDatabase(t, ctx)
		store := events.NewStore(db)
		dispatcher := postgresEventDispatcher(t, db, store)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := store.PublishTx(ctx, tx, event); err != nil {
			t.Fatal(err)
		}
		// Registration cannot see the uncommitted event. Publishing cannot see
		// the new topic. Serializable must reject the resulting read/write cycle.
		if err := dispatcher.Register(ctx); err != nil {
			t.Fatal(err)
		}
		assertEventSerializationFailure(t, tx.Commit())
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events_log").Scan(&count); err != nil || count != 0 {
			t.Fatalf("aborted event survived: %d, %v", count, err)
		}
		id, err := store.Publish(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		assertPostgresEventDelivery(t, db, id)
	})

	t.Run("registration_snapshot_precedes_publication", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		db := postgresEventsDatabase(t, ctx)
		store := events.NewStore(db)
		dispatcher := postgresEventDispatcher(t, db, store)
		// Pause registration after inserting its uncommitted topic. The lock
		// belongs to this temporary schema's test only, not a live workspace.
		gate, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer gate.Close()
		if _, err := gate.ExecContext(ctx, "SELECT pg_advisory_lock(hashtext(current_schema()), 873921)"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = gate.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtext(current_schema()), 873921)")
		}()
		if _, err := db.ExecContext(ctx, `CREATE FUNCTION pause_event_registration() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext(current_schema()), 873921);
    RETURN NEW;
END;
$$`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `CREATE TRIGGER pause_event_registration AFTER INSERT ON event_consumer_topics FOR EACH ROW EXECUTE FUNCTION pause_event_registration()`); err != nil {
			t.Fatal(err)
		}
		registered := make(chan error, 1)
		go func() { registered <- dispatcher.Register(ctx) }()
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting bool
			if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND classid::BIGINT = (hashtext(current_schema())::BIGINT & 4294967295) AND objid = 873921 AND NOT granted)").Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case err := <-registered:
				t.Fatalf("registration did not pause: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
		}
		id, err := store.Publish(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gate.ExecContext(ctx, "SELECT pg_advisory_unlock(hashtext(current_schema()), 873921)"); err != nil {
			t.Fatal(err)
		}
		assertEventSerializationFailure(t, <-registered)
		// A failed Register must not mark the dispatcher registered. Retrying
		// explicitly with a new snapshot must backfill the committed event.
		if err := dispatcher.Register(ctx); err != nil {
			t.Fatal(err)
		}
		assertPostgresEventDelivery(t, db, id)
	})
}

func TestPostgresEventPublicationAndDeliveryRollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db := postgresEventsDatabase(t, ctx)
	store := events.NewStore(db)
	if err := postgresEventDispatcher(t, db, store).Register(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	id, err := store.PublishTx(ctx, tx, contracts.NewEvent{Topic: "core.test.created", SourceModule: "core", SchemaVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM event_deliveries WHERE event_id = ?", id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("transaction delivery count = %d, error = %v", count, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM events_log) + (SELECT COUNT(*) FROM event_deliveries)").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback left %d rows, error = %v", count, err)
	}
}
