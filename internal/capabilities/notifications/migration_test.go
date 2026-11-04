package notifications

import (
	"context"
	"os"
	"strings"
	"testing"

	"workbench/internal/contracts"
)

func notificationDownSQL(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../foundation/database/migrations/00011_user_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, found := strings.Cut(string(raw), "-- +goose Down")
	if !found {
		t.Fatal("notification rollback migration missing")
	}
	return down
}

func TestNotificationRollbackRejectsNonOwnerData(t *testing.T) {
	for _, fixture := range []string{
		`INSERT INTO auth_users(id,username,display_name,password_hash,role,status,credential_version,must_change_password,created_at,updated_at) VALUES('bob','bob','Bob','hash','user','active',1,0,1,1)`,
		`INSERT INTO notifications(id,recipient_id,source_module,severity,title,content,idempotency_key,created_at) VALUES('bob-note','bob','core','info','Private','Private','key',1)`,
		`INSERT INTO telegram_recipients(user_id,enabled,chat_id,activated_at,bot_generation) VALUES('bob',1,'12345',1,0)`,
		`INSERT INTO events_log(id,user_id,topic,schema_version,source_module,payload_json,occurred_at,available_at,created_at) VALUES('bob-event','bob','core.test.created',1,'core','{}',1,1,1)`,
	} {
		t.Run(strings.Fields(fixture)[2], func(t *testing.T) {
			_, db := openService(t)
			if _, err := db.SQL().Exec(fixture); err != nil {
				t.Fatal(err)
			}
			tx, err := db.SQL().BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(notificationDownSQL(t))
			if err == nil {
				_ = tx.Rollback()
				t.Fatal("rollback allowed non-owner private data")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			// Failed rollback retains the scoped schema and target table atomically.
			if _, err := db.SQL().Exec(`SELECT recipient_id FROM notifications LIMIT 1; SELECT user_id FROM telegram_recipients LIMIT 1`); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNotificationOwnerRollbackRestoresTelegramAndIdempotency(t *testing.T) {
	channel, service, _ := setupTelegram(t)
	_, err := service.Create(testContext(), contracts.NewNotification{SourceModule: "core", Severity: contracts.NotificationInfo, Title: "Owner", IdempotencyKey: "original-key"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := channel.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(notificationDownSQL(t)); err != nil {
		t.Fatal(err)
	}
	var key, chatID string
	var enabled int
	if err := tx.QueryRow(`SELECT idempotency_key FROM notifications`).Scan(&key); err != nil || key != "original-key" {
		t.Fatalf("owner idempotency changed: %s %v", key, err)
	}
	if err := tx.QueryRow(`SELECT json_extract(value,'$.chatId'),json_extract(value,'$.enabled') FROM workspace_settings WHERE section='telegram'`).Scan(&chatID, &enabled); err != nil || chatID != "-100123" || enabled != 1 {
		t.Fatalf("owner telegram target not restored: %s %d %v", chatID, enabled, err)
	}
	var status string
	if err := tx.QueryRow(`SELECT value FROM workspace_settings WHERE section='telegram_status'`).Scan(&status); err != nil || status != "{}" {
		t.Fatalf("owner status not restored: %s %v", status, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
