package modules

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"workbench/internal/contracts"
)

func TestIncompatibleDisableRetriesAfterRecovery(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("recovery-token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", true); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.protocol = 99
	remote.mu.Unlock()
	before, err := registry.Refresh(context.Background(), "demo_external")
	if err != nil || before.Health != contracts.HealthIncompatible {
		t.Fatalf("incompatible setup: %+v %v", before, err)
	}
	remote.setDown(true)
	disabled, err := registry.SetExternalEnabled(context.Background(), "demo_external", false)
	if err != nil || !disabled.Pending {
		t.Fatalf("offline disable: %+v %v", disabled, err)
	}
	remote.setDown(false)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		listed, _ := registry.GetListed("demo_external")
		if !listed.Pending && !remote.runningNow() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	listed, _ := registry.GetListed("demo_external")
	if remote.runningNow() || listed.Pending {
		t.Fatalf("remote recovered but queued disable never applied: desired=%v observed=%v pending=%v health=%s remoteRunning=%v", listed.Enabled, listed.ObservedEnabled, listed.Pending, listed.Health, remote.runningNow())
	}
	rec, _ := registry.snapshotExternal("demo_external")
	if rec.businessAllowed() || rec.Health != contracts.HealthIncompatible {
		t.Fatal("incompatible business reopened")
	}
}

func TestWebSocketRejectsOldConnectionSnapshot(t *testing.T) {
	registry := testRegistry(t)
	oldRemote := newFakeRemote("old-token")
	t.Cleanup(oldRemote.Close)
	attachDemo(t, registry, oldRemote)
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", true); err != nil {
		t.Fatal(err)
	}
	// A handler has already copied this record but has not started its dial yet.
	oldSnapshot, _ := registry.snapshotExternal("demo_external")
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", false); err != nil {
		t.Fatal(err)
	}
	newRemote := newFakeRemote("new-token")
	t.Cleanup(newRemote.Close)
	if _, err := registry.UpdateConnection(context.Background(), "demo_external", ConnectionInput{BaseURL: newRemote.URL(), ServiceToken: "new-token"}); err != nil {
		t.Fatal(err)
	}
	if result, err := registry.SetExternalEnabled(context.Background(), "demo_external", true); err != nil || result.Pending {
		t.Fatalf("enable new: %+v %v", result, err)
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		registry.proxyWebSocket(w, req, oldSnapshot, "/api/stream")
	}))
	defer host.Close()
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/api/modules/demo_external/proxy/stream", nil)
	if conn != nil {
		conn.Close()
	}
	if resp != nil {
		defer resp.Body.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stale connection should be rejected with 503: response=%v error=%v", resp, err)
	}
	oldRemote.mu.Lock()
	sockets := len(oldRemote.wsClients)
	oldRemote.mu.Unlock()
	if sockets != 0 {
		t.Fatalf("stale upstream was dialed: %d sockets", sockets)
	}
}
