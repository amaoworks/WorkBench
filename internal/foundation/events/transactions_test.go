package events

import (
	"context"
	"testing"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/auth"
)

func TestPublishTxChecksUserOnTransactionConnection(t *testing.T) {
	db := openTestDatabase(t)
	users, err := auth.New(context.Background(), db.SQL(), auth.Config{Mode: auth.ModeLocal, ListenAddress: "127.0.0.1:9090"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db.SQL())
	store.SetUserDirectory(users)
	db.SQL().SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(eventContext(contracts.LegacyUserID), 2*time.Second)
	defer cancel()
	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := store.PublishTx(ctx, tx, contracts.NewEvent{Topic: "todo.task.created", SourceModule: "todo", SchemaVersion: 1}); err != nil {
		t.Fatalf("publish with the only connection held by the transaction: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPublishTxRejectsUserDisabledInSameTransaction(t *testing.T) {
	db := openTestDatabase(t)
	users, err := auth.New(context.Background(), db.SQL(), auth.Config{Mode: auth.ModeLocal, ListenAddress: "127.0.0.1:9090"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db.SQL())
	store.SetUserDirectory(users)
	db.SQL().SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(eventContext(contracts.LegacyUserID), 2*time.Second)
	defer cancel()
	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE auth_users SET status='disabled' WHERE id=?`, contracts.LegacyUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishTx(ctx, tx, contracts.NewEvent{Topic: "todo.task.created", SourceModule: "todo", SchemaVersion: 1}); err == nil {
		t.Fatal("published an event for a disabled user")
	}
	if ctx.Err() != nil {
		t.Fatalf("user check waited for another connection: %v", ctx.Err())
	}
}
