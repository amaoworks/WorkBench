package events

import (
	"context"
	"database/sql"
	"testing"

	"workbench/internal/contracts"
)

type eventUsers map[string]bool

func (u eventUsers) ActiveUserIDs(context.Context) ([]string, error) {
	var ids []string
	for id, active := range u {
		if active {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
func (u eventUsers) IsUserActive(_ context.Context, id string) (bool, error) { return u[id], nil }
func (u eventUsers) IsUserActiveTx(ctx context.Context, _ *sql.Tx, id string) (bool, error) {
	return u.IsUserActive(ctx, id)
}
func eventContext(id string) context.Context {
	return contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: id, Role: contracts.RoleAdmin})
}

func TestPrivateEventRestoresPersistedUnprivilegedPrincipal(t *testing.T) {
	db := openTestDatabase(t)
	store := NewStore(db.SQL())
	users := eventUsers{"alice": true, "bob": true, "disabled": true}
	store.SetUserDirectory(users)
	var received []string
	dispatcher, err := NewDispatcher(db.SQL(), store, []contracts.EventConsumer{{ID: "todo.private", Module: "todo", Topics: []string{"todo.task.created"}, Handler: func(ctx context.Context, e contracts.Event) error {
		principal, authenticated := contracts.CurrentPrincipal(ctx)
		if e.UserID == "" {
			if authenticated {
				t.Fatal("system event inherited caller identity")
			}
			received = append(received, "system")
			return nil
		}
		if !authenticated || principal.UserID != e.UserID || principal.Role != contracts.RoleUser {
			t.Fatalf("invalid trusted principal: %+v %+v", principal, e)
		}
		received = append(received, e.UserID)
		return nil
	}}}, func(contracts.ModuleID) bool { return true }, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.SetUserDirectory(users)
	input := contracts.NewEvent{Topic: "todo.task.created", SchemaVersion: 1, SourceModule: "todo"}
	for _, id := range []string{"alice", "bob", "disabled"} {
		if _, err := store.Publish(eventContext(id), input); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Publish(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	input.UserID = "bob"
	if _, err := store.Publish(eventContext("alice"), input); err == nil {
		t.Fatal("admin published another user's event")
	}
	users["disabled"] = false
	input.UserID = "disabled"
	if _, err := store.Publish(context.Background(), input); err == nil {
		t.Fatal("accepted inactive explicit event owner")
	}
	if err := dispatcher.DispatchOnce(eventContext("unrelated-admin")); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, id := range received {
		seen[id]++
	}
	if len(received) != 3 || seen["alice"] != 1 || seen["bob"] != 1 || seen["system"] != 1 {
		t.Fatalf("received: %v", received)
	}
	var state string
	if err := db.SQL().QueryRow(`SELECT d.status FROM event_deliveries d JOIN events_log e ON e.id=d.event_id WHERE e.user_id='disabled'`).Scan(&state); err != nil || state != "dead" {
		t.Fatalf("disabled event status: %s %v", state, err)
	}
}

func TestPrivateEventNeedsDirectoryForRehydration(t *testing.T) {
	db := openTestDatabase(t)
	store := NewStore(db.SQL())
	_, err := store.Publish(eventContext("alice"), contracts.NewEvent{Topic: "todo.task.created", SchemaVersion: 1, SourceModule: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	d, err := NewDispatcher(db.SQL(), store, []contracts.EventConsumer{{ID: "todo.private", Module: "todo", Topics: []string{"todo.task.created"}, Handler: func(context.Context, contracts.Event) error { called = true; return nil }}}, func(contracts.ModuleID) bool { return true }, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("rehydrated identity without active directory")
	}
}
