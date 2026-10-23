package events

import (
	"context"
	"fmt"
	"testing"
	"time"

	"workbench/internal/contracts"
)

func registrationConsumer(id string, topics ...string) contracts.EventConsumer {
	return contracts.EventConsumer{
		ID: contracts.ConsumerID(id), Module: "core", Topics: topics,
		Handler: func(context.Context, contracts.Event) error { return nil },
	}
}

func TestDispatchersRegisterIndependently(t *testing.T) {
	database := openTestDatabase(t)
	store := NewStore(database.SQL())
	ctx := context.Background()
	for _, id := range []string{"core.first", "core.second"} {
		dispatcher, err := NewDispatcher(database.SQL(), store, []contracts.EventConsumer{
			registrationConsumer(id, "core.test.created"),
		}, func(contracts.ModuleID) bool { return false }, testLogger())
		if err != nil {
			t.Fatal(err)
		}
		if err := dispatcher.Register(ctx); err != nil {
			t.Fatal(err)
		}
		// A second registration must not touch the database again.
		if _, err := database.SQL().Exec(`CREATE TRIGGER reject_registration BEFORE INSERT ON event_consumer_topics BEGIN SELECT RAISE(ABORT, 'registered twice'); END`); err != nil {
			t.Fatal(err)
		}
		if err := dispatcher.Register(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := database.SQL().Exec("DROP TRIGGER reject_registration"); err != nil {
			t.Fatal(err)
		}
	}
	id, err := store.Publish(ctx, contracts.NewEvent{Topic: "core.test.created", SourceModule: "core", SchemaVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM event_deliveries WHERE event_id = ?", id).Scan(&count); err != nil || count != 2 {
		t.Fatalf("deliveries = %d, error = %v; want both dispatchers, including disabled consumers", count, err)
	}
}

func TestConsumerRestartBackfillsHistoryWithoutResettingDeliveries(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	store := NewStore(database.SQL())
	publish := func(topic string) contracts.EventID {
		t.Helper()
		id, err := store.Publish(ctx, contracts.NewEvent{Topic: topic, SourceModule: "core", SchemaVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	register := func(consumers ...contracts.EventConsumer) *Dispatcher {
		t.Helper()
		dispatcher, err := NewDispatcher(database.SQL(), store, consumers, func(id contracts.ModuleID) bool { return true }, testLogger())
		if err != nil {
			t.Fatal(err)
		}
		if err := dispatcher.Register(ctx); err != nil {
			t.Fatal(err)
		}
		return dispatcher
	}
	created := publish("core.test.created")
	completed := publish("core.test.completed")
	first := register(registrationConsumer("core.retained", "core.test.created"), registrationConsumer("core.removed", "core.test.created"))
	if err := first.DispatchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.ResetConsumers(ctx); err != nil {
		t.Fatal(err)
	}
	store = NewStore(database.SQL())
	gap := publish("core.test.created")
	register(registrationConsumer("core.retained", "core.test.created", "core.test.completed"), registrationConsumer("core.new", "core.test.created"))
	var status string
	var attempts, count int
	if err := database.SQL().QueryRow("SELECT status, attempts FROM event_deliveries WHERE event_id = ? AND consumer_id = 'core.retained'", created).Scan(&status, &attempts); err != nil || status != "succeeded" || attempts != 1 {
		t.Fatalf("existing delivery = %s/%d, error = %v", status, attempts, err)
	}
	for _, event := range []contracts.EventID{created, completed, gap} {
		if err := database.SQL().QueryRow("SELECT COUNT(*) FROM event_deliveries WHERE event_id = ? AND consumer_id = 'core.retained'", event).Scan(&count); err != nil || count != 1 {
			t.Fatalf("history missing %s: %d, %v", event, count, err)
		}
	}
	fresh := publish("core.test.created")
	for _, event := range []contracts.EventID{gap, fresh} {
		if err := database.SQL().QueryRow("SELECT COUNT(*) FROM event_deliveries WHERE event_id = ?", event).Scan(&count); err != nil || count != 2 {
			t.Fatalf("new event deliveries = %d, error = %v; removed consumer must not receive new events", count, err)
		}
	}
	// Replacing one consumer's topics must remove obsolete topics without
	// affecting another dispatcher's registrations or historical deliveries.
	register(registrationConsumer("core.retained", "core.test.completed"))
	fresh = publish("core.test.created")
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM event_deliveries WHERE event_id = ?", fresh).Scan(&count); err != nil || count != 1 {
		t.Fatalf("topic replacement deliveries = %d, error = %v", count, err)
	}
}

func TestRegistrationFailureRollsBackAndCanBeRetried(t *testing.T) {
	database := openTestDatabase(t)
	store := NewStore(database.SQL())
	ctx := context.Background()
	if _, err := store.Publish(ctx, contracts.NewEvent{Topic: "core.test.created", SourceModule: "core", SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().Exec(`CREATE TRIGGER reject_registration BEFORE INSERT ON event_consumer_topics WHEN NEW.consumer_id = 'core.second' BEGIN SELECT RAISE(ABORT, 'registration failed'); END`); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewDispatcher(database.SQL(), store, []contracts.EventConsumer{
		registrationConsumer("core.first", "core.test.created"), registrationConsumer("core.second", "core.test.created"),
	}, func(contracts.ModuleID) bool { return false }, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Register(ctx); err == nil {
		t.Fatal("registration unexpectedly succeeded")
	}
	var count int
	if err := database.SQL().QueryRow("SELECT (SELECT COUNT(*) FROM event_consumer_topics) + (SELECT COUNT(*) FROM event_deliveries)").Scan(&count); err != nil || count != 0 {
		t.Fatalf("registration partially committed: %d, %v", count, err)
	}
	if _, err := database.SQL().Exec("DROP TRIGGER reject_registration"); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Register(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPublishRollsBackWhenDeliveryInsertFails(t *testing.T) {
	database := openTestDatabase(t)
	store := NewStore(database.SQL())
	ctx := context.Background()
	dispatcher, err := NewDispatcher(database.SQL(), store, []contracts.EventConsumer{registrationConsumer("core.failure", "core.test.created")}, func(contracts.ModuleID) bool { return true }, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().Exec(`CREATE TRIGGER reject_delivery BEFORE INSERT ON event_deliveries BEGIN SELECT RAISE(ABORT, 'delivery failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(ctx, contracts.NewEvent{Topic: "core.test.created", SourceModule: "core", SchemaVersion: 1}); err == nil {
		t.Fatal("Publish succeeded despite delivery insertion failure")
	}
	var count int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM events_log").Scan(&count); err != nil || count != 0 {
		t.Fatalf("event committed without delivery: %d, %v", count, err)
	}
}

func TestIdleDispatcherDoesNotAcquireSQLiteWriteLock(t *testing.T) {
	database := openTestDatabase(t)
	store := NewStore(database.SQL())
	ctx := context.Background()
	dispatcher, err := NewDispatcher(database.SQL(), store, []contracts.EventConsumer{registrationConsumer("core.idle", "core.test.created")}, func(contracts.ModuleID) bool { return true }, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Register(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := database.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := dispatcher.DispatchOnce(ctx); err != nil {
		t.Fatalf("idle poll contended with the writer: %v", err)
	}
}

func BenchmarkIdleDispatcherHistory(b *testing.B) {
	for _, history := range []int{0, 10000, 100000} {
		b.Run(fmt.Sprintf("events_%d", history), func(b *testing.B) {
			database := openTestDatabase(b)
			ctx := context.Background()
			if history > 0 {
				if _, err := database.SQL().Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < ?) INSERT INTO events_log(id,topic,schema_version,source_module,payload_json,occurred_at,available_at,created_at) SELECT CAST(i AS TEXT),'core.test.created',1,'core','{}',0,0,0 FROM n`, history); err != nil {
					b.Fatal(err)
				}
			}
			dispatcher, err := NewDispatcher(database.SQL(), NewStore(database.SQL()), []contracts.EventConsumer{registrationConsumer("core.idle", "core.test.created")}, func(contracts.ModuleID) bool { return true }, testLogger())
			if err != nil {
				b.Fatal(err)
			}
			if err := dispatcher.Register(ctx); err != nil {
				b.Fatal(err)
			}
			if _, err := database.SQL().Exec("UPDATE event_deliveries SET status = 'succeeded'"); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := dispatcher.DispatchOnce(ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
