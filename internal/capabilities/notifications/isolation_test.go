package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workbench/internal/contracts"
	dbsqlc "workbench/internal/foundation/database/sqlc"
)

func userContext(id string) context.Context {
	return contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: id, Role: contracts.RoleUser})
}

func TestNotificationRecipientIsolation(t *testing.T) {
	service, db := openService(t)
	input := contracts.NewNotification{SourceModule: "todo", Severity: contracts.NotificationInfo, Title: "Private task", IdempotencyKey: "same-key"}
	if _, err := service.Create(context.Background(), input); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("unscoped create: %v", err)
	}
	a, err := service.Create(userContext("alice"), input)
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.Create(userContext("bob"), input)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID || a.RecipientID != "alice" || b.RecipientID != "bob" {
		t.Fatalf("recipient/idempotency collision: %+v %+v", a, b)
	}
	for _, ctx := range []context.Context{userContext("alice"), contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: "alice", Role: contracts.RoleAdmin})} {
		other := input
		other.RecipientID = "bob"
		if _, err := service.Create(ctx, other); err == nil {
			t.Fatal("accepted cross-user create")
		}
	}
	if err := service.MarkRead(userContext("alice"), []string{b.ID}); err != nil {
		t.Fatal(err)
	}
	if err := service.Archive(userContext("alice"), []string{b.ID}); err != nil {
		t.Fatal(err)
	}
	if err := service.MarkAllRead(userContext("alice"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if count, err := service.UnreadCount(userContext("bob")); err != nil || count != 1 {
		t.Fatalf("bob count: %d %v", count, err)
	}
	page, err := service.List(userContext("alice"), contracts.NotificationQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != a.ID {
		t.Fatalf("alice page: %+v %v", page, err)
	}
	page, err = service.List(userContext("bob"), contracts.NotificationQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != b.ID || page.Items[0].ArchivedAt != nil {
		t.Fatalf("bob page: %+v %v", page, err)
	}
	if _, err := service.List(context.Background(), contracts.NotificationQuery{}); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("unscoped list: %v", err)
	}
	if err := service.MarkRead(context.Background(), nil); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("unscoped mutation: %v", err)
	}
	rows, err := db.SQL().Query(`SELECT user_id FROM events_log WHERE topic='core.notification.created' ORDER BY user_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var owners []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			t.Fatal(err)
		}
		owners = append(owners, owner)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(owners) != 2 || owners[0] != "alice" || owners[1] != "bob" {
		t.Fatalf("event owners: %v", owners)
	}
}

func TestNotificationExplicitSystemRecipientRequiresActiveDirectory(t *testing.T) {
	service, db := openService(t)
	input := contracts.NewNotification{RecipientID: "alice", SourceModule: "core", Severity: contracts.NotificationInfo, Title: "Private", IdempotencyKey: "system"}
	if _, err := service.Create(context.Background(), input); err == nil {
		t.Fatal("accepted unchecked system recipient")
	}
	service.SetUserDirectory(activeUsers{})
	// A system-created notification forwards the same trusted recipient to its event.
	store := service.events.(interface{ SetUserDirectory(contracts.UserDirectory) })
	store.SetUserDirectory(activeUsers{})
	note, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := db.SQL().QueryRow(`SELECT user_id FROM events_log WHERE aggregate_id=?`, note.ID).Scan(&owner); err != nil || owner != "alice" {
		t.Fatalf("event owner: %s %v", owner, err)
	}
	input.RecipientID = "disabled"
	if _, err := service.Create(context.Background(), input); err == nil {
		t.Fatal("accepted inactive recipient")
	}
}

func TestHubNeverBroadcastsPrivateEvents(t *testing.T) {
	hub := NewHub()
	a, stopA := hub.subscribe("alice")
	defer stopA()
	b, stopB := hub.subscribe("bob")
	defer stopB()
	event := contracts.Event{ID: "private", UserID: "alice", Topic: "core.notification.created", Payload: json.RawMessage(`{"notificationId":"alice-note"}`)}
	if err := hub.Consumer().Handler(userContext("alice"), event); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a:
	default:
		t.Fatal("alice did not receive event")
	}
	select {
	case <-b:
		t.Fatal("bob received alice event")
	default:
	}
	event.UserID = ""
	if err := hub.Consumer().Handler(context.Background(), event); err == nil {
		t.Fatal("accepted unscoped event")
	}
	recorder := httptest.NewRecorder()
	hub.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/notifications/stream", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unscoped SSE status: %d", recorder.Code)
	}
}

func TestTelegramOwnTargetsAndAdminMapping(t *testing.T) {
	channel, _, now := setupTelegram(t)
	q := dbsqlc.New(channel.db)
	for _, row := range []struct{ id, chat string }{{"alice", "11111"}, {"bob", "22222"}} {
		if err := q.SaveTelegramRecipient(context.Background(), dbsqlc.SaveTelegramRecipientParams{UserID: row.id, Enabled: 1, ChatID: row.chat, ActivatedAt: now.Add(-time.Minute).UnixMilli(), BotGeneration: 0}); err != nil {
			t.Fatal(err)
		}
	}
	var chats []string
	channel.client.Transport = telegramTransport(func(r *http.Request) (*http.Response, error) {
		var body struct {
			ChatID string `json:"chat_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		chats = append(chats, body.ChatID)
		return telegramResponse(200, `{"ok":true}`), nil
	})
	note := telegramNotice(*now)
	note.RecipientID = "alice"
	if err := channel.Deliver(userContext("alice"), note); err != nil {
		t.Fatal(err)
	}
	// A notification without a mapped target must never use the owner's legacy chat.
	channel.SetUserDirectory(allUsers{})
	note.RecipientID = "unmapped"
	if err := channel.Deliver(userContext("unmapped"), note); err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0] != "11111" {
		t.Fatalf("delivery targets: %v", chats)
	}
	recorder := httptest.NewRecorder()
	channel.SaveRecipientSettings(recorder, httptest.NewRequest("PUT", "/api/settings/telegram/recipient", strings.NewReader(`{"enabled":true,"chatId":"22222"}`)).WithContext(userContext("alice")))
	if recorder.Code != 403 {
		t.Fatalf("user remapped recipient: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	channel.RecipientSettings(recorder, httptest.NewRequest("GET", "/api/settings/telegram/recipient", nil).WithContext(userContext("alice")))
	if strings.Contains(recorder.Body.String(), "private-token") || strings.Contains(recorder.Body.String(), "22222") || !strings.Contains(recorder.Body.String(), "11111") {
		t.Fatalf("recipient settings leaked: %s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	channel.Settings(recorder, httptest.NewRequest("GET", "/api/settings/telegram", nil).WithContext(userContext("alice")))
	if recorder.Code != 403 {
		t.Fatalf("ordinary user read bot settings: %d", recorder.Code)
	}
	// A changed bot must not silently deliver existing private targets.
	var cfg telegramConfig
	if err := channel.load(context.Background(), "telegram", &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Generation++
	if err := channel.save(context.Background(), "telegram", cfg); err != nil {
		t.Fatal(err)
	}
	note.RecipientID = "bob"
	if err := channel.Deliver(userContext("bob"), note); err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 {
		t.Fatalf("old mapping used new bot: %v", chats)
	}
	// Cross-user event payload IDs cannot route to another recipient's notification.
	err := channel.Consumer().Handler(userContext("alice"), contracts.Event{UserID: "bob", Payload: json.RawMessage(`{"notificationId":"anything"}`)})
	if err == nil {
		t.Fatal("accepted mismatched event principal")
	}
}

type changingUser struct{ active atomic.Bool }

func (u *changingUser) ActiveUserIDs(context.Context) ([]string, error) { return nil, nil }
func (u *changingUser) IsUserActive(context.Context, string) (bool, error) {
	return u.active.Load(), nil
}

func TestHubClosesDisabledUserStream(t *testing.T) {
	hub := NewHub()
	hub.heartbeat = time.Millisecond
	users := &changingUser{}
	users.active.Store(true)
	hub.SetUserDirectory(users)
	recorder := newStreamRecorder()
	ctx, cancel := context.WithCancel(userContext("alice"))
	defer cancel()
	done := make(chan struct{})
	go func() {
		hub.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/notifications/stream", nil).WithContext(ctx))
		close(done)
	}()
	waitForStreamText(t, recorder, "retry: 3000")
	users.active.Store(false)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disabled user's stream remained open")
	}
}

type allUsers struct{}

func (allUsers) ActiveUserIDs(context.Context) ([]string, error)    { return nil, nil }
func (allUsers) IsUserActive(context.Context, string) (bool, error) { return true, nil }
