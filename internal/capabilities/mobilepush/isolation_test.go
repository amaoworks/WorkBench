package mobilepush

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"workbench/internal/contracts"
)

func TestPushUsesOnlyRecipientOwnedActiveDevices(t *testing.T) {
	service, db := testService(t, "device-a")
	now := time.Now().UTC()
	for _, user := range []struct{ id, status string }{{"bob", "active"}, {"disabled", "disabled"}} {
		_, err := db.SQL().Exec(`INSERT INTO auth_users(id,username,display_name,password_hash,role,status,credential_version,must_change_password,created_at,updated_at) VALUES(?,?,?,'test','user',?,1,0,?,?)`, user.id, user.id, user.id, user.status, now.UnixMilli(), now.UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.SQL().Exec(`INSERT INTO auth_device_sessions(id,user_id,device_name,platform,credential_version,created_at,last_seen_at,expires_at) VALUES(?,?,?,'ios',1,?,?,?)`, user.id+"-device", user.id, user.id, now.UnixMilli(), now.UnixMilli(), now.Add(time.Hour).UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct{ id, session string }{{"owner-token", "device-a"}, {"bob-token", "bob-device"}, {"disabled-token", "disabled-device"}} {
		_, err := db.SQL().Exec(`INSERT INTO mobile_push_tokens(id,device_session_id,platform,token,created_at,updated_at) VALUES(?,?,'ios',?,?,?)`, row.id, row.session, row.id, now.UnixMilli(), now.UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
	}
	p := service.providers["ios"].(*stubProvider)
	note := contracts.Notification{RecipientID: "bob", External: true, CreatedAt: now, Title: "Private"}
	if err := service.Deliver(context.Background(), note); err != nil {
		t.Fatal(err)
	}
	if len(p.tokens) != 1 || p.tokens[0] != "bob-token" {
		t.Fatalf("cross-user push: %v", p.tokens)
	}
	if err := service.Deliver(pushContext(), note); err == nil {
		t.Fatal("accepted wrong principal")
	}
	note.RecipientID = "disabled"
	if err := service.Deliver(context.Background(), note); err != nil {
		t.Fatal(err)
	}
	if len(p.tokens) != 1 {
		t.Fatalf("disabled user pushed: %v", p.tokens)
	}
	if err := service.Deliver(context.Background(), contracts.Notification{External: true, CreatedAt: now}); err == nil {
		t.Fatal("accepted notification without recipient")
	}
	// Even a faulty device resolver cannot bind the caller to another user's session.
	service.deviceSessionID = func(*http.Request) string { return "bob-device" }
	recorder := httptest.NewRecorder()
	service.RegisterToken(recorder, pushRequest("PUT", "/api/mobile/push-token", strings.NewReader(`{"platform":"ios","pushToken":"owner-token"}`)))
	if recorder.Code != 401 {
		t.Fatalf("cross-user token register: %d %s", recorder.Code, recorder.Body.String())
	}
	var owner string
	if err := db.SQL().QueryRow(`SELECT device_session_id FROM mobile_push_tokens WHERE token='owner-token'`).Scan(&owner); err != nil || owner != "device-a" {
		t.Fatalf("failed registration stole token: %s %v", owner, err)
	}
	recorder = httptest.NewRecorder()
	service.DeleteToken(recorder, pushRequest("DELETE", "/api/mobile/push-token", strings.NewReader(`{"platform":"ios","pushToken":"bob-token"}`)))
	if recorder.Code != 204 {
		t.Fatalf("delete status: %d", recorder.Code)
	}
	var count int
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM mobile_push_tokens WHERE token='bob-token'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cross-user delete: %d %v", count, err)
	}
	// Event payload cannot read another recipient's notification by guessing its ID.
	raw, _ := json.Marshal(map[string]string{"notificationId": "foreign"})
	if err := service.Consumer().Handler(pushContext(), contracts.Event{UserID: "bob", Payload: raw}); err == nil {
		t.Fatal("accepted mismatched push event")
	}
}
