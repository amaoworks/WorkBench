package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	ctx := context.Background()
	database, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	registry, err := InitializeWith(ctx, database, []contracts.Module{testModule{id: "todo"}}, Options{
		Timeouts: Timeouts{Control: 200 * time.Millisecond, ProbeInterval: time.Hour, RetryMax: 200 * time.Millisecond, MaxProbes: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	return registry
}

func attachDemo(t *testing.T, registry *Registry, remote *fakeRemote) ListedModule {
	t.Helper()
	listed, err := registry.Attach(context.Background(), ConnectionInput{BaseURL: remote.URL(), ServiceToken: remote.token})
	if err != nil {
		t.Fatal(err)
	}
	if listed.ID != "demo_external" || listed.Kind != KindExternal || listed.Enabled {
		t.Fatalf("attach listing = %+v", listed)
	}
	if listed.BaseURL != remote.URL() || !listed.HasServiceToken {
		t.Fatalf("connection fields = %+v", listed)
	}
	if got, _ := json.Marshal(listed); bytes.Contains(got, []byte(remote.token)) {
		t.Fatal("service token leaked in listing")
	}
	return listed
}

func TestAttachUnknownIDShowsNavigationAndDoesNotBlockList(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("secret-token-xyz")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)

	remote.mu.Lock()
	remote.delay = 5 * time.Second
	remote.mu.Unlock()
	start := time.Now()
	items := registry.List()
	if time.Since(start) > 300*time.Millisecond {
		t.Fatal("List waited on remote")
	}
	var found ListedModule
	for _, item := range items {
		if item.ID == "demo_external" {
			found = item
		}
	}
	if found.Kind != KindExternal || len(found.Navigation) != 1 || found.Navigation[0].Route != "/apps/demo_external/overview" {
		t.Fatalf("catalog = %+v", found)
	}
	if found.HasSettings != true || found.SettingsEntry != "/settings/index.html" {
		t.Fatalf("settings = %+v", found)
	}
}

func TestFailedPersistDoesNotPublishCatalog(t *testing.T) {
	registry := testRegistry(t)
	_ = registry.db.Close()
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	_, err := registry.Attach(context.Background(), ConnectionInput{BaseURL: remote.URL(), ServiceToken: "token"})
	if err == nil {
		t.Fatal("attach succeeded against closed database")
	}
	for _, item := range registry.List() {
		if item.ID == "demo_external" {
			t.Fatal("failed persist published catalog")
		}
	}
}

func TestDuplicateAndReservedIDsRejected(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	if _, err := registry.Attach(context.Background(), ConnectionInput{BaseURL: remote.URL(), ServiceToken: "token"}); err == nil {
		t.Fatal("duplicate attach succeeded")
	}
	remote.id = "todo"
	if _, err := registry.Attach(context.Background(), ConnectionInput{BaseURL: remote.URL(), ServiceToken: "token"}); err == nil {
		t.Fatal("builtin id accepted")
	}
	remote.id = "core"
	if _, err := registry.Attach(context.Background(), ConnectionInput{BaseURL: remote.URL(), ServiceToken: "token"}); err == nil {
		t.Fatal("core id accepted")
	}
}

func TestUnsupportedProtocolKeepsLastGoodManifest(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	remote.protocol = 99
	listed, err := registry.Refresh(context.Background(), "demo_external")
	if err != nil {
		t.Fatal(err)
	}
	if listed.Health != contracts.HealthIncompatible || listed.ProtocolVersion != 1 {
		t.Fatalf("incompatible refresh = %+v", listed)
	}
	if listed.Navigation[0].Route != "/apps/demo_external/overview" {
		t.Fatal("last-good manifest was overwritten")
	}
}

func TestEnableConfirmAndDisableGate(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)

	result, err := registry.SetExternalEnabled(context.Background(), "demo_external", true)
	if err != nil || result.Pending || !result.Module.Enabled {
		t.Fatalf("enable: %+v %v", result, err)
	}
	if result.Module.ObservedEnabled == nil || !*result.Module.ObservedEnabled {
		t.Fatal("enable not confirmed")
	}
	rec, _ := registry.snapshotExternal("demo_external")
	if !rec.businessAllowed() {
		t.Fatal("business still gated after confirm")
	}

	result, err = registry.SetExternalEnabled(context.Background(), "demo_external", false)
	if err != nil || result.Module.Enabled {
		t.Fatalf("disable: %+v %v", result, err)
	}
	rec, _ = registry.snapshotExternal("demo_external")
	if rec.businessAllowed() {
		t.Fatal("business remained open after disable")
	}
	if remote.runningNow() {
		t.Fatal("example counter still running")
	}
}

func TestEnableTimeoutReturnsPending(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	remote.setDelay(time.Second)
	attachDemo(t, registry, remote)
	result, err := registry.SetExternalEnabled(context.Background(), "demo_external", true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Pending || result.Module.ObservedEnabled != nil && *result.Module.ObservedEnabled {
		t.Fatalf("timeout should be pending: %+v", result.Module)
	}
	rec, _ := registry.snapshotExternal("demo_external")
	if rec.businessAllowed() {
		t.Fatal("pending enable opened business")
	}
}

func TestStaleGenerationDoesNotOverwrite(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	hold := make(chan struct{})
	remote.mu.Lock()
	remote.hold = hold
	remote.mu.Unlock()
	attachDemo(t, registry, remote)

	var first EnableResult
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		first, err = registry.SetExternalEnabled(context.Background(), "demo_external", true)
		if err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(30 * time.Millisecond)
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", false); err != nil {
		t.Fatal(err)
	}
	third, err := registry.SetExternalEnabled(context.Background(), "demo_external", true)
	if err != nil {
		t.Fatal(err)
	}
	close(hold)
	wg.Wait()
	if third.Module.Generation < first.Module.Generation {
		t.Fatalf("highest generation lost: first=%d third=%d", first.Module.Generation, third.Module.Generation)
	}
	listed, _ := registry.GetListed("demo_external")
	if listed.Generation != third.Module.Generation {
		t.Fatalf("late reply overwrote state: %+v", listed)
	}
}

func TestConnectionUpdateRequiresDisableAndNewToken(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", true); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.UpdateConnection(context.Background(), "demo_external", ConnectionInput{BaseURL: remote.URL(), ServiceToken: "token"}); err == nil {
		t.Fatal("update while enabled succeeded")
	}
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", false); err != nil {
		t.Fatal(err)
	}
	other := newFakeRemote("other-token")
	t.Cleanup(other.Close)
	if _, err := registry.UpdateConnection(context.Background(), "demo_external", ConnectionInput{BaseURL: other.URL()}); err == nil {
		t.Fatal("origin change without token succeeded")
	}
	listed, err := registry.UpdateConnection(context.Background(), "demo_external", ConnectionInput{BaseURL: other.URL(), ServiceToken: "other-token"})
	if err != nil {
		t.Fatal(err)
	}
	if listed.Enabled {
		t.Fatal("connection update auto-enabled")
	}
	if listed.BaseURL != other.URL() {
		t.Fatal("origin not replaced")
	}
}

func TestUnregisterRemovesRecordAndDoesNotResurrect(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	if err := registry.Unregister(context.Background(), "demo_external"); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.GetListed("demo_external"); ok {
		t.Fatal("unregistered module still listed")
	}
	remote.mu.Lock()
	remote.generation = 9
	remote.enabled = true
	remote.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	if _, ok := registry.GetListed("demo_external"); ok {
		t.Fatal("async task resurrected module")
	}
}

func TestProxyAuthBoundsAndCookieStrip(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", true); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/modules/demo_external/ui/index.html", nil)
	req.SetPathValue("id", "demo_external")
	req.SetPathValue("*", "index.html")
	req.AddCookie(&http.Cookie{Name: "workbench_session", Value: "secret-cookie"})
	req.Header.Set("Authorization", "Bearer browser")
	rec := httptest.NewRecorder()
	registry.UIHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ui status = %d %s", rec.Code, rec.Body.String())
	}
	if remote.seenCookie != "" {
		t.Fatalf("cookie forwarded: %q", remote.seenCookie)
	}
	if remote.seenAuth != "Bearer token" {
		t.Fatalf("auth = %q", remote.seenAuth)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/modules/demo_external/proxy/../_workbench/manifest", nil)
	req.SetPathValue("id", "demo_external")
	req.SetPathValue("*", "../_workbench/manifest")
	rec = httptest.NewRecorder()
	registry.APIProxyHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("traversal status = %d", rec.Code)
	}

	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", false); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/modules/demo_external/proxy/counter", nil)
	req.SetPathValue("id", "demo_external")
	req.SetPathValue("*", "counter")
	rec = httptest.NewRecorder()
	registry.APIProxyHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "module_disabled") {
		t.Fatalf("disabled proxy = %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/modules/demo_external/settings/index.html", nil)
	req.SetPathValue("id", "demo_external")
	req.SetPathValue("*", "index.html")
	rec = httptest.NewRecorder()
	registry.SettingsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings while disabled = %d", rec.Code)
	}
}

func TestProxyRejectsUnsafeRedirectAndNonLocal(t *testing.T) {
	registry := testRegistry(t)
	if _, err := registry.Attach(context.Background(), ConnectionInput{BaseURL: "http://8.8.8.8", ServiceToken: "x"}); err == nil {
		t.Fatal("non-local attach succeeded")
	}
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	remote.redirect = "https://evil.example/"
	attachDemo(t, registry, remote)
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", true); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/modules/demo_external/ui/index.html", nil)
	req.SetPathValue("id", "demo_external")
	req.SetPathValue("*", "index.html")
	rec := httptest.NewRecorder()
	registry.UIHandler().ServeHTTP(rec, req)
	if rec.Header().Get("Location") != "" {
		t.Fatalf("unsafe redirect leaked: %s", rec.Header().Get("Location"))
	}
}

func TestWebSocketClosedOnDisable(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", true); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.SetPathValue("id", "demo_external")
		req.SetPathValue("*", "stream")
		registry.APIProxyHandler().ServeHTTP(w, req)
	}))
	t.Cleanup(host.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/api/modules/demo_external/proxy/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(time.Second)
	for registry.ActiveProxySockets("demo_external") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("proxy websocket was not registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, _, _ = conn.ReadMessage()
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", false); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("websocket remained open after disable")
	}
}

func TestStartupDoesNotClobberExternalOrUseStaleHealth(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data.db")
	database, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	registry, err := InitializeWith(ctx, database, []contracts.Module{testModule{id: "todo"}}, Options{
		Timeouts: Timeouts{Control: 200 * time.Millisecond, ProbeInterval: time.Hour, RetryMax: time.Second, MaxProbes: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	remote := newFakeRemote("token")
	attachDemo(t, registry, remote)
	if _, err := registry.SetExternalEnabled(ctx, "demo_external", true); err != nil {
		t.Fatal(err)
	}
	_ = registry.Close()
	_ = database.Close()
	remote.Close()

	database, err = workbenchdb.Open(ctx, workbenchdb.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	reloaded, err := InitializeWith(ctx, database, []contracts.Module{testModule{id: "todo"}}, Options{
		Timeouts: Timeouts{Control: 50 * time.Millisecond, ProbeInterval: time.Hour, RetryMax: time.Second, MaxProbes: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	listed, ok := reloaded.GetListed("demo_external")
	if !ok || !listed.Enabled {
		t.Fatal("registration/intent lost")
	}
	if listed.Health == contracts.HealthReady || listed.Health == contracts.HealthDegraded {
		t.Fatalf("stale health used: %s", listed.Health)
	}
	rec, _ := reloaded.snapshotExternal("demo_external")
	if rec.businessAllowed() {
		t.Fatal("stale health opened business")
	}
}

func proxyRequest(handler http.Handler, path, id, rest string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetPathValue("id", id)
	req.SetPathValue("*", rest)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestRestartReconfirmsEnabledModuleWithoutWaitingProbeInterval(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data.db")
	database, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	registry, err := InitializeWith(ctx, database, []contracts.Module{testModule{id: "todo"}}, Options{
		Timeouts: Timeouts{Control: 200 * time.Millisecond, ProbeInterval: time.Hour, RetryMax: 200 * time.Millisecond, MaxProbes: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	if _, err := registry.SetExternalEnabled(ctx, "demo_external", true); err != nil {
		t.Fatal(err)
	}
	if got := proxyRequest(registry.APIProxyHandler(), "/api/modules/demo_external/proxy/counter", "demo_external", "counter"); got.Code != http.StatusOK {
		t.Fatalf("enabled proxy = %d %s", got.Code, got.Body.String())
	}
	_ = registry.Close()
	_ = database.Close()

	database, err = workbenchdb.Open(ctx, workbenchdb.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	start := time.Now()
	reloaded, err := InitializeWith(ctx, database, []contracts.Module{testModule{id: "todo"}}, Options{
		Timeouts: Timeouts{Control: 200 * time.Millisecond, ProbeInterval: time.Hour, RetryMax: 200 * time.Millisecond, MaxProbes: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	deadline := time.Now().Add(2 * time.Second)
	var last *httptest.ResponseRecorder
	for {
		last = proxyRequest(reloaded.APIProxyHandler(), "/api/modules/demo_external/proxy/counter", "demo_external", "counter")
		if last.Code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("proxy did not reopen after restart reconfirm: %d %s", last.Code, last.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("reconfirm waited %s, would have been gated on ProbeInterval", elapsed)
	}
}

func TestOfflineDisableDoesNotClaimSuccessAndReconcilesAfterRecovery(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", true); err != nil {
		t.Fatal(err)
	}
	if got := proxyRequest(registry.APIProxyHandler(), "/api/modules/demo_external/proxy/counter", "demo_external", "counter"); got.Code != http.StatusOK {
		t.Fatalf("enabled proxy = %d %s", got.Code, got.Body.String())
	}

	remote.setDown(true)
	result, err := registry.SetExternalEnabled(context.Background(), "demo_external", false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Pending {
		t.Fatalf("offline disable claimed success: %+v", result.Module)
	}
	if result.Module.Enabled {
		t.Fatal("disable intent was not saved")
	}
	if result.Module.ObservedEnabled != nil && !*result.Module.ObservedEnabled && result.Module.ObservedGeneration == result.Module.Generation {
		t.Fatal("offline disable claimed remote stop success")
	}
	got := proxyRequest(registry.APIProxyHandler(), "/api/modules/demo_external/proxy/counter", "demo_external", "counter")
	if got.Code != http.StatusServiceUnavailable || !strings.Contains(got.Body.String(), "module_disabled") {
		t.Fatalf("offline disable proxy = %d %s", got.Code, got.Body.String())
	}

	remote.setDown(false)
	deadline := time.Now().Add(2 * time.Second)
	for {
		listed, _ := registry.GetListed("demo_external")
		if !listed.Pending && listed.ObservedEnabled != nil && !*listed.ObservedEnabled && listed.ObservedGeneration == listed.Generation {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("recover did not confirm disable: %+v", listed)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestConfigWhileDisabled(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	body, err := registry.PutConfig(context.Background(), "demo_external", json.RawMessage(`{"label":"saved"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"saved"`)) {
		t.Fatalf("put config = %s", body)
	}
	got, err := registry.GetConfig(context.Background(), "demo_external")
	if err != nil || !bytes.Contains(got, []byte(`"saved"`)) {
		t.Fatalf("get config = %s %v", got, err)
	}
}

func TestUpgradePreservesBuiltinEnabled(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data.db")
	database, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	registry, err := Initialize(ctx, database, []contracts.Module{testModule{id: "todo"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.SetEnabled(ctx, "todo", false); err != nil {
		t.Fatal(err)
	}
	_ = registry.Close()
	_ = database.Close()

	database, err = workbenchdb.Open(ctx, workbenchdb.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	reloaded, err := Initialize(ctx, database, []contracts.Module{testModule{id: "todo"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	if reloaded.IsEnabled("todo") {
		t.Fatal("builtin enabled flag lost after upgrade")
	}
}

func TestIncompatibleStaysClosedUntilManifestRevalidated(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", true); err != nil {
		t.Fatal(err)
	}
	if got := proxyRequest(registry.APIProxyHandler(), "/api/modules/demo_external/proxy/counter", "demo_external", "counter"); got.Code != http.StatusOK {
		t.Fatalf("enabled proxy = %d %s", got.Code, got.Body.String())
	}
	remote.protocol = 99
	listed, err := registry.Refresh(context.Background(), "demo_external")
	if err != nil {
		t.Fatal(err)
	}
	if listed.Health != contracts.HealthIncompatible {
		t.Fatalf("refresh health = %s", listed.Health)
	}
	if got := proxyRequest(registry.APIProxyHandler(), "/api/modules/demo_external/proxy/counter", "demo_external", "counter"); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("incompatible proxy = %d %s", got.Code, got.Body.String())
	}

	registry.Probe(context.Background(), "demo_external")
	listed, _ = registry.GetListed("demo_external")
	if listed.Health != contracts.HealthIncompatible {
		t.Fatalf("probe cleared incompatible: %+v", listed)
	}
	if got := proxyRequest(registry.APIProxyHandler(), "/api/modules/demo_external/proxy/counter", "demo_external", "counter"); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("probe reopened business: %d %s", got.Code, got.Body.String())
	}

	remote.protocol = 1
	listed, err = registry.Refresh(context.Background(), "demo_external")
	if err != nil {
		t.Fatal(err)
	}
	if listed.Health == contracts.HealthIncompatible {
		t.Fatal("valid refresh left incompatible")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := proxyRequest(registry.APIProxyHandler(), "/api/modules/demo_external/proxy/counter", "demo_external", "counter")
		if got.Code == http.StatusOK {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("valid refresh did not reopen: %d %s", got.Code, got.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDisableAbortsInFlightWebSocketHandshake(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	hold := make(chan struct{})
	remote.wsHold = hold
	attachDemo(t, registry, remote)
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", true); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.SetPathValue("id", "demo_external")
		req.SetPathValue("*", "stream")
		registry.APIProxyHandler().ServeHTTP(w, req)
	}))
	t.Cleanup(host.Close)

	errc := make(chan error, 1)
	go func() {
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/api/modules/demo_external/proxy/stream", nil)
		if err != nil {
			errc <- err
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if _, _, err := conn.ReadMessage(); err == nil {
			errc <- errHandshakeCompleted
			return
		}
		errc <- err
	}()
	deadline := time.Now().Add(time.Second)
	for !remote.wsHeld.Load() {
		if time.Now().After(deadline) {
			t.Fatal("upstream handshake did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", false); err != nil {
		t.Fatal(err)
	}
	close(hold)
	select {
	case err := <-errc:
		if err == errHandshakeCompleted {
			t.Fatal("websocket completed after disable")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handshake did not finish after disable")
	}
	if registry.ActiveProxySockets("demo_external") != 0 {
		t.Fatal("disabled module kept proxy sockets")
	}
}

var errHandshakeCompleted = errors.New("handshake completed")

func TestProxyCloseReleasesIdleUpstreamConnections(t *testing.T) {
	registry := testRegistry(t)
	var mu sync.Mutex
	open := map[net.Conn]http.ConnState{}
	var seen atomic.Int32
	remote := startFakeRemote("token", func(conn net.Conn, state http.ConnState) {
		if state == http.StateNew {
			seen.Add(1)
		}
		mu.Lock()
		defer mu.Unlock()
		switch state {
		case http.StateClosed, http.StateHijacked:
			delete(open, conn)
		default:
			open[conn] = state
		}
	})
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got := proxyRequest(registry.APIProxyHandler(), "/api/modules/demo_external/proxy/counter", "demo_external", "counter")
		if got.Code != http.StatusOK {
			t.Fatalf("proxy %d = %d %s", i, got.Code, got.Body.String())
		}
	}
	if seen.Load() == 0 {
		t.Fatal("server did not observe proxy connections")
	}
	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(open)
		mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle upstream connections left after Close: %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReattachAfterUnregisterCanEnable(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", true); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.SetExternalEnabled(context.Background(), "demo_external", false); err != nil {
		t.Fatal(err)
	}
	if err := registry.Unregister(context.Background(), "demo_external"); err != nil {
		t.Fatal(err)
	}
	attachDemo(t, registry, remote)
	result, err := registry.SetExternalEnabled(context.Background(), "demo_external", true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Pending {
		t.Fatalf("reattach enable pending: %+v", result.Module)
	}
	if got := proxyRequest(registry.APIProxyHandler(), "/api/modules/demo_external/proxy/counter", "demo_external", "counter"); got.Code != http.StatusOK {
		t.Fatalf("reattach proxy = %d %s", got.Code, got.Body.String())
	}
}
