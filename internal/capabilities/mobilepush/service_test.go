package mobilepush

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/database"
	"workbench/internal/foundation/events"
)

func pushContext() context.Context {
	return contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: contracts.LegacyUserID, Role: contracts.RoleUser})
}
func pushRequest(method, url string, body io.Reader) *http.Request {
	return httptest.NewRequest(method, url, body).WithContext(pushContext())
}

type activePushUsers struct{}

func (activePushUsers) ActiveUserIDs(context.Context) ([]string, error) {
	return []string{"owner", "alice", "bob"}, nil
}
func (activePushUsers) IsUserActive(_ context.Context, id string) (bool, error) {
	return id == "owner" || id == "alice" || id == "bob", nil
}
func (u activePushUsers) IsUserActiveTx(ctx context.Context, _ *sql.Tx, id string) (bool, error) {
	return u.IsUserActive(ctx, id)
}

type stubProvider struct {
	invalid bool
	err     error
	tokens  []string
}

func (p *stubProvider) Send(_ context.Context, _ contracts.Notification, token string) (bool, error) {
	p.tokens = append(p.tokens, token)
	return p.invalid, p.err
}

func testService(t *testing.T, sessionID string) (*Service, *database.Database) {
	t.Helper()
	db, err := database.Open(context.Background(), database.Config{Path: t.TempDir() + "/workbench.db"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.SQL().Exec(`INSERT INTO auth_users(id,username,display_name,password_hash,role,status,credential_version,must_change_password,created_at,updated_at) VALUES('owner','admin','Admin','test','admin','active',1,0,?,?)`, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"device-a", "device-b", "revoked"} {
		var revoked any
		if id == "revoked" {
			revoked = now.UnixMilli()
		}
		_, err := db.SQL().Exec(`INSERT INTO auth_device_sessions(id,device_name,platform,credential_version,created_at,last_seen_at,expires_at,revoked_at) VALUES(?,?,?,?,?,?,?,?)`,
			id, id, "ios", 1, now.UnixMilli(), now.UnixMilli(), now.Add(time.Hour).UnixMilli(), revoked)
		if err != nil {
			t.Fatal(err)
		}
	}
	p := &stubProvider{}
	s := &Service{db: db.SQL(), enabled: true, deviceSessionID: func(*http.Request) string { return sessionID }, now: time.Now, providers: map[string]provider{"ios": p}}
	return s, db
}

func TestRegisterTokenBindsResolverSessionAndTransfersToken(t *testing.T) {
	s, db := testService(t, "device-a")
	request := pushRequest(http.MethodPost, "/api/mobile/push-token", strings.NewReader(`{"platform":"ios","pushToken":"push-secret"}`))
	recorder := httptest.NewRecorder()
	s.RegisterToken(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("register status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var owner string
	if err := db.SQL().QueryRow(`SELECT device_session_id FROM mobile_push_tokens WHERE token = ?`, "push-secret").Scan(&owner); err != nil || owner != "device-a" {
		t.Fatalf("owner = %q, err = %v", owner, err)
	}

	s.deviceSessionID = func(*http.Request) string { return "device-b" }
	recorder = httptest.NewRecorder()
	s.RegisterToken(recorder, pushRequest(http.MethodPut, "/api/mobile/push-token", strings.NewReader(`{"platform":"ios","pushToken":"push-secret"}`)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("transfer status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := db.SQL().QueryRow(`SELECT device_session_id FROM mobile_push_tokens WHERE token = ?`, "push-secret").Scan(&owner); err != nil || owner != "device-b" {
		t.Fatalf("transferred owner = %q, err = %v", owner, err)
	}

	s.deviceSessionID = func(*http.Request) string { return "revoked" }
	recorder = httptest.NewRecorder()
	s.RegisterToken(recorder, pushRequest(http.MethodPost, "/api/mobile/push-token", strings.NewReader(`{"platform":"ios","pushToken":"other"}`)))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("revoked status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestRegisterTokenRejectsInputDeviceSessionID(t *testing.T) {
	s, db := testService(t, "device-a")
	recorder := httptest.NewRecorder()
	s.RegisterToken(recorder, pushRequest(http.MethodPost, "/api/mobile/push-token", strings.NewReader(`{"platform":"ios","pushToken":"push-secret","deviceSessionId":"device-b"}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var count int
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM mobile_push_tokens`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("token count = %d, err = %v", count, err)
	}
}

func TestDeleteTokenOnlyDeletesCurrentDeviceBinding(t *testing.T) {
	s, db := testService(t, "device-a")
	now := time.Now().UnixMilli()
	_, err := db.SQL().Exec(`INSERT INTO mobile_push_tokens(id,device_session_id,platform,token,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "token-id", "device-b", "ios", "other-device-token", now, now)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	s.DeleteToken(recorder, pushRequest(http.MethodDelete, "/api/mobile/push-token", strings.NewReader(`{"platform":"ios","pushToken":"other-device-token"}`)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", recorder.Code)
	}
	var count int
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM mobile_push_tokens`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("token count = %d, err = %v", count, err)
	}
}

func TestDeliverFiltersRevokedSessionsAndDeletesInvalidTokens(t *testing.T) {
	s, db := testService(t, "device-a")
	p := &stubProvider{invalid: true}
	s.providers["ios"] = p
	now := time.Now().UTC()
	for _, row := range []struct{ id, session, token string }{{"active-id", "device-a", "active-token"}, {"revoked-id", "revoked", "revoked-token"}} {
		_, err := db.SQL().Exec(`INSERT INTO mobile_push_tokens(id,device_session_id,platform,token,created_at,updated_at) VALUES(?,?,?,?,?,?)`, row.id, row.session, "ios", row.token, now.UnixMilli(), now.UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
	}
	err := s.Deliver(context.Background(), contracts.Notification{RecipientID: contracts.LegacyUserID, ID: "note", External: true, Title: "Title", Content: "Body", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.tokens) != 1 || p.tokens[0] != "active-token" {
		t.Fatalf("sent tokens = %v", p.tokens)
	}
	var count int
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM mobile_push_tokens WHERE id = 'active-id'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid active token count = %d, err = %v", count, err)
	}
}

func TestDeliverSkipsDisabledSourceModule(t *testing.T) {
	s, db := testService(t, "device-a")
	p := &stubProvider{}
	s.providers["ios"] = p
	s.allowed = func(module contracts.ModuleID) bool { return module != "todo" }
	now := time.Now().UTC()
	_, err := db.SQL().Exec(`INSERT INTO mobile_push_tokens(id,device_session_id,platform,token,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "active-id", "device-a", "ios", "active-token", now.UnixMilli(), now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(context.Background(), contracts.Notification{RecipientID: contracts.LegacyUserID, ID: "note", SourceModule: "todo", External: true, Title: "Title", Content: "Body", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if len(p.tokens) != 0 {
		t.Fatalf("disabled module sent tokens = %v", p.tokens)
	}
}

func TestConsumerUsesDurableEventDelivery(t *testing.T) {
	s, db := testService(t, "device-a")
	p := &stubProvider{}
	s.providers["ios"] = p
	now := time.Now().UTC()
	_, err := db.SQL().Exec(`INSERT INTO mobile_push_tokens(id,device_session_id,platform,token,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "token-id", "device-a", "ios", "push-token", now.UnixMilli(), now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.SQL().Exec(`INSERT INTO notifications(id,source_module,external,severity,title,content,idempotency_key,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		"note-id", "core", 1, "info", "Title", "Body", "mobile-push-test", now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	store := events.NewStore(db.SQL())
	payload, _ := json.Marshal(map[string]string{"notificationId": "note-id"})
	eventID, err := store.Publish(pushContext(), contracts.NewEvent{Topic: "core.notification.created", SchemaVersion: 1, SourceModule: "core", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := events.NewDispatcher(db.SQL(), store, []contracts.EventConsumer{s.Consumer()}, func(contracts.ModuleID) bool { return true }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.SetUserDirectory(activePushUsers{})
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.tokens) != 1 || p.tokens[0] != "push-token" {
		t.Fatalf("sent tokens = %v", p.tokens)
	}
	var status string
	if err := db.SQL().QueryRow(`SELECT status FROM event_deliveries WHERE event_id = ? AND consumer_id = ?`, eventID, consumerID).Scan(&status); err != nil || status != "succeeded" {
		t.Fatalf("delivery status = %q, err = %v", status, err)
	}
}
