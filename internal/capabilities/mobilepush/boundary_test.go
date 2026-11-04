package mobilepush

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
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

type blockedInvalidProvider struct {
	started chan string
	release chan struct{}
}

func (p *blockedInvalidProvider) Send(ctx context.Context, _ contracts.Notification, token string) (bool, error) {
	select {
	case p.started <- token:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	select {
	case <-p.release:
		return true, nil // The in-flight token was reported Unregistered.
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func registerTestToken(t *testing.T, s *Service, token string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	s.RegisterToken(recorder, pushRequest(http.MethodPut, "/api/mobile/push-token", strings.NewReader(`{"platform":"ios","pushToken":"`+token+`"}`)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("register status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func publishTestNotification(t *testing.T, db *database.Database, store *events.Store, id string, createdAt time.Time) contracts.EventID {
	t.Helper()
	_, err := db.SQL().Exec(`INSERT INTO notifications(id,source_module,external,severity,title,content,idempotency_key,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		id, "core", 1, "info", "Title", "Body", id, createdAt.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"notificationId": id})
	eventID, err := store.Publish(pushContext(), contracts.NewEvent{Topic: "core.notification.created", SchemaVersion: 1, SourceModule: "core", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	return eventID
}

func testDispatcher(t *testing.T, s *Service, db *database.Database, store *events.Store) *events.Dispatcher {
	t.Helper()
	d, err := events.NewDispatcher(db.SQL(), store, []contracts.EventConsumer{s.Consumer()}, func(contracts.ModuleID) bool { return true }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	d.SetUserDirectory(activePushUsers{})
	return d
}

func assertDelivery(t *testing.T, db *database.Database, eventID contracts.EventID, wantStatus string, wantAttempts int) {
	t.Helper()
	var status string
	var attempts int
	if err := db.SQL().QueryRow(`SELECT status, attempts FROM event_deliveries WHERE event_id = ? AND consumer_id = ?`, eventID, consumerID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || attempts != wantAttempts {
		t.Fatalf("delivery = %s/%d, want %s/%d", status, attempts, wantStatus, wantAttempts)
	}
}

func testEnabledConfig(t *testing.T) Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return Config{Enabled: true, APNs: &APNsConfig{TeamID: "TEAM", KeyID: "KEY", BundleID: "com.example.app", PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})}}
}

func TestInvalidInFlightTokenDoesNotDeleteReplacement(t *testing.T) {
	s, db := testService(t, "device-a")
	base := time.Now().UTC().Truncate(time.Millisecond)
	s.now = func() time.Time { return base }
	registerTestToken(t, s, "old-token")
	var oldID string
	if err := db.SQL().QueryRow(`SELECT id FROM mobile_push_tokens`).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	p := &blockedInvalidProvider{started: make(chan string), release: make(chan struct{})}
	s.providers["ios"] = p
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- s.Deliver(ctx, contracts.Notification{RecipientID: contracts.LegacyUserID, ID: "note", External: true, CreatedAt: base})
	}()
	select {
	case token := <-p.started:
		if token != "old-token" {
			t.Fatalf("in-flight token = %q", token)
		}
	case <-ctx.Done():
		t.Fatal("provider did not start")
	}
	// The replacement keeps the record ID while the old request is blocked.
	s.now = func() time.Time { return base.Add(time.Second) }
	registerTestToken(t, s, "new-token")
	close(p.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var id, token string
	if err := db.SQL().QueryRow(`SELECT id, token FROM mobile_push_tokens`).Scan(&id, &token); err != nil || id != oldID || token != "new-token" {
		t.Fatalf("remaining binding = %q/%q, err = %v", id, token, err)
	}
	fresh := &stubProvider{}
	s.providers["ios"] = fresh
	if err := s.Deliver(t.Context(), contracts.Notification{RecipientID: contracts.LegacyUserID, ID: "new-note", External: true, CreatedAt: base.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if len(fresh.tokens) != 1 || fresh.tokens[0] != "new-token" {
		t.Fatalf("new notification tokens = %v", fresh.tokens)
	}
}

func TestConsumerSkipsHistoryBeforeTokenRegistration(t *testing.T) {
	for _, mode := range []string{"initial", "replacement", "transfer"} {
		t.Run(mode, func(t *testing.T) {
			s, db := testService(t, "device-a")
			base := time.Now().UTC().Truncate(time.Millisecond)
			s.now = func() time.Time { return base }
			if mode == "replacement" {
				registerTestToken(t, s, "old-token")
			}
			if mode == "transfer" {
				s.deviceSessionID = func(*http.Request) string { return "device-b" }
				registerTestToken(t, s, "push-token")
				s.deviceSessionID = func(*http.Request) string { return "device-a" }
			}
			store := events.NewStore(db.SQL())
			oldEvent := publishTestNotification(t, db, store, "old-note", base.Add(time.Second))
			s.now = func() time.Time { return base.Add(2 * time.Second) }
			registerTestToken(t, s, "push-token")
			p := s.providers["ios"].(*stubProvider)
			d := testDispatcher(t, s, db, store)
			// Registration backfills the old notification event after the token exists.
			if err := d.DispatchOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(p.tokens) != 0 {
				t.Fatalf("history sent tokens = %v", p.tokens)
			}
			assertDelivery(t, db, oldEvent, "succeeded", 1)
			newEvent := publishTestNotification(t, db, store, "new-note", base.Add(3*time.Second))
			if err := d.DispatchOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(p.tokens) != 1 || p.tokens[0] != "push-token" {
				t.Fatalf("new notification tokens = %v", p.tokens)
			}
			assertDelivery(t, db, newEvent, "succeeded", 1)
		})
	}
}

func TestConsumerSkipsHistoryBeforeServiceActivation(t *testing.T) {
	for _, reenable := range []bool{false, true} {
		name := "first-enable"
		if reenable {
			name = "reenable"
		}
		t.Run(name, func(t *testing.T) {
			s, db := testService(t, "device-a")
			base := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
			s.now = func() time.Time { return base }
			registerTestToken(t, s, "push-token")
			if reenable {
				if err := s.syncActivation(t.Context()); err != nil {
					t.Fatal(err)
				}
				if _, err := New(db.SQL(), Config{}, s.deviceSessionID); err != nil {
					t.Fatal(err)
				}
			}
			store := events.NewStore(db.SQL())
			oldEvent := publishTestNotification(t, db, store, "old-note", base.Add(time.Second))
			enabled, err := New(db.SQL(), testEnabledConfig(t), s.deviceSessionID)
			if err != nil {
				t.Fatal(err)
			}
			if enabled.activatedAt <= base.Add(time.Second).UnixMilli() {
				t.Fatalf("activation = %d, want after old notification", enabled.activatedAt)
			}
			p := &stubProvider{}
			enabled.providers["ios"] = p
			d := testDispatcher(t, enabled, db, store)
			if err := d.DispatchOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(p.tokens) != 0 {
				t.Fatalf("pre-activation history sent tokens = %v", p.tokens)
			}
			assertDelivery(t, db, oldEvent, "succeeded", 1)
			newEvent := publishTestNotification(t, db, store, "new-note", time.Now().UTC())
			if err := d.DispatchOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(p.tokens) != 1 || p.tokens[0] != "push-token" {
				t.Fatalf("new notification tokens = %v", p.tokens)
			}
			assertDelivery(t, db, newEvent, "succeeded", 1)
		})
	}
}

func TestRestartAndSameTokenRegistrationPreservePendingRetry(t *testing.T) {
	s, db := testService(t, "device-a")
	base := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	s.now = func() time.Time { return base }
	if err := s.syncActivation(t.Context()); err != nil {
		t.Fatal(err)
	}
	registerTestToken(t, s, "push-token")
	p := s.providers["ios"].(*stubProvider)
	p.err = errors.New("temporary provider failure")
	store := events.NewStore(db.SQL())
	eventID := publishTestNotification(t, db, store, "retry-note", base.Add(time.Second))
	if err := testDispatcher(t, s, db, store).DispatchOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertDelivery(t, db, eventID, "retry", 1)

	restarted, err := New(db.SQL(), testEnabledConfig(t), s.deviceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.activatedAt != base.UnixMilli() {
		t.Fatalf("restart activation = %d, want %d", restarted.activatedAt, base.UnixMilli())
	}
	fresh := &stubProvider{}
	restarted.providers["ios"] = fresh
	// Mobile clients may register the unchanged token again after reconnecting.
	registerTestToken(t, restarted, "push-token")
	if _, err := db.SQL().Exec(`UPDATE event_deliveries SET next_attempt_at = 0 WHERE event_id = ? AND consumer_id = ?`, eventID, consumerID); err != nil {
		t.Fatal(err)
	}
	if err := testDispatcher(t, restarted, db, store).DispatchOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fresh.tokens) != 1 || fresh.tokens[0] != "push-token" {
		t.Fatalf("retried notification tokens = %v", fresh.tokens)
	}
	assertDelivery(t, db, eventID, "succeeded", 2)
}
