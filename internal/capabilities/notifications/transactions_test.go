package notifications

import (
	"context"
	"testing"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/auth"
	"workbench/internal/foundation/events"
)

func TestCreateChecksUserOnTransactionConnection(t *testing.T) {
	service, db := openService(t)
	users, err := auth.New(context.Background(), db.SQL(), auth.Config{Mode: auth.ModeLocal, ListenAddress: "127.0.0.1:9090"})
	if err != nil {
		t.Fatal(err)
	}
	service.SetUserDirectory(users)
	service.events.(*events.Store).SetUserDirectory(users)
	db.SQL().SetMaxOpenConns(1)
	for _, scoped := range []bool{true, false} {
		ctx := context.Background()
		if scoped {
			ctx = testContext()
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		note, err := service.Create(ctx, contracts.NewNotification{
			RecipientID: contracts.LegacyUserID, SourceModule: "todo", Severity: contracts.NotificationInfo,
			Title: "Task due", IdempotencyKey: "single-connection",
		})
		cancel()
		if err != nil {
			t.Fatalf("create scoped=%v with one connection: %v", scoped, err)
		}
		if note.RecipientID != contracts.LegacyUserID {
			t.Fatalf("unexpected recipient: %+v", note)
		}
	}
}

func TestCreateTxRejectsUserDisabledInSameTransaction(t *testing.T) {
	service, db := openService(t)
	users, err := auth.New(context.Background(), db.SQL(), auth.Config{Mode: auth.ModeLocal, ListenAddress: "127.0.0.1:9090"})
	if err != nil {
		t.Fatal(err)
	}
	service.SetUserDirectory(users)
	service.events.(*events.Store).SetUserDirectory(users)
	db.SQL().SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(testContext(), 2*time.Second)
	defer cancel()
	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE auth_users SET status='disabled' WHERE id=?`, contracts.LegacyUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTx(ctx, tx, contracts.NewNotification{
		SourceModule: "todo", Severity: contracts.NotificationInfo, Title: "Task due", IdempotencyKey: "disabled-user",
	}); err == nil {
		t.Fatal("created a notification for a disabled user")
	}
	if ctx.Err() != nil {
		t.Fatalf("user check waited for another connection: %v", ctx.Err())
	}
}
