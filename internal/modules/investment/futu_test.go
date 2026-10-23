package investment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFutuReconnectsAfterOpenDRestart(t *testing.T) {
	fake := startFakeOpenD(t)
	_, portStr, _ := strings.Cut(fake.addr(), ":")
	port, _ := strconv.Atoi(portStr)
	m := openInvestmentModule(t)
	m.deps.FutuOpenDAddress = fake.addr()
	if err := m.storeFutu(context.Background(), futuRecord{Host: "127.0.0.1", Port: port, Enabled: true, OvernightEnabled: true}); err != nil {
		t.Fatal(err)
	}
	first, err := m.futu.ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the TCP closure caused by the managed OpenD process restarting.
	first.conn.Close()
	select {
	case <-first.stop:
	case <-time.After(time.Second):
		t.Fatal("client did not observe disconnect")
	}
	second, err := m.futu.ensureOverlay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("gateway reused the closed connection")
	}
	if loggedIn, err := second.globalState(context.Background()); err != nil || !loggedIn {
		t.Fatal("replacement connection is unusable", err)
	}
}

func TestFutuSettingsDefaultAndValidation(t *testing.T) {
	module := openInvestmentModule(t)
	got := httptest.NewRecorder()
	module.getFutu(got, httptest.NewRequest(http.MethodGet, "/api/modules/investment/futu", nil))
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"enabled":false`) || strings.Contains(got.Body.String(), `"host"`) || strings.Contains(got.Body.String(), `"port"`) {
		t.Fatalf("default: %s", got.Body.String())
	}
	bad := httptest.NewRecorder()
	module.saveFutu(bad, httptest.NewRequest(http.MethodPut, "/api/modules/investment/futu", strings.NewReader(`{"host":"futu-opend","port":11111,"enabled":true,"allowNonLocal":false}`)))
	if bad.Code != 400 {
		t.Fatalf("deployment fields accepted: %d %s", bad.Code, bad.Body.String())
	}
	unknown := httptest.NewRecorder()
	module.saveFutu(unknown, httptest.NewRequest(http.MethodPut, "/api/modules/investment/futu", strings.NewReader(`{"enabled":false,"allowHistoryKline":true}`)))
	if unknown.Code != 400 {
		t.Fatalf("unknown field: %d %s", unknown.Code, unknown.Body.String())
	}
}

func TestFutuConnectionComesFromDeploymentConfiguration(t *testing.T) {
	m := openInvestmentModule(t)
	if _, err := m.deps.DB.Exec(`UPDATE investment_futu SET host='old-host',port=9999,allow_non_local=1`); err != nil {
		t.Fatal(err)
	}
	m.deps.FutuOpenDAddress = "configured-opend:11112"
	m.deps.FutuAllowNonLocal = true
	rec, err := m.loadFutu(context.Background())
	if err != nil || rec.Host != "configured-opend" || rec.Port != 11112 || !rec.AllowNonLocal {
		t.Fatal("database connection overrode deployment configuration", err)
	}
	for _, address := range []string{"http://opend:11111", "127.0.0.1:0", "127.0.0.1:abc"} {
		m.deps.FutuOpenDAddress = address
		if _, _, err := m.futuConnection(); err == nil {
			t.Fatal("invalid OpenD deployment address accepted", address)
		}
	}
}

func TestFutuKlineDisabledConflict(t *testing.T) {
	module := openInvestmentModule(t)
	rec := httptest.NewRecorder()
	module.getFutuKline(rec, httptest.NewRequest(http.MethodGet, "/api/modules/investment/futu/kline?symbol=AAPL&resolution=1", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("disabled kline: %d %s", rec.Code, rec.Body.String())
	}
	ws := httptest.NewRecorder()
	module.serveFutuStream(ws, httptest.NewRequest(http.MethodGet, "/api/modules/investment/futu/quote/ws", nil))
	if ws.Code != http.StatusConflict {
		t.Fatalf("disabled ws: %d %s", ws.Code, ws.Body.String())
	}
}

func TestFutuKlineUsesCurrentWindowAndHistoryFallback(t *testing.T) {
	fake := startFakeOpenD(t)
	module := openInvestmentModule(t)
	save := httptest.NewRecorder()
	module.deps.FutuOpenDAddress = fake.addr()
	body := `{"enabled":true}`
	module.saveFutu(save, httptest.NewRequest(http.MethodPut, "/api/modules/investment/futu", strings.NewReader(body)))
	if save.Code != 200 {
		t.Fatalf("save: %d %s", save.Code, save.Body.String())
	}
	night := httptest.NewRecorder()
	module.saveOvernight(night, httptest.NewRequest(http.MethodPut, "/api/modules/investment/overnight", strings.NewReader(`{"enabled":true}`)))
	if night.Code != 200 {
		t.Fatal(night.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/modules/investment/futu/kline?symbol=AAPL&resolution=1", nil)
	rec := httptest.NewRecorder()
	module.getFutuKline(rec, req)
	if rec.Code != 200 {
		t.Fatalf("kline: %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Candles []futuBar `json:"candles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Candles) != 1 || payload.Candles[0].Close != 101 {
		t.Fatalf("current window should drop RTH: %+v", payload.Candles)
	}
	fake.mu.Lock()
	hist := fake.historyN
	fake.mu.Unlock()
	if hist != 0 {
		t.Fatalf("current-night request must not burn 3103, got %d", hist)
	}

	from := time.Date(2026, 1, 14, 20, 0, 0, 0, nyZone).Unix()
	to := time.Date(2026, 1, 16, 4, 0, 0, 0, nyZone).Unix()
	rangeURL := "/api/modules/investment/futu/kline?symbol=AAPL&resolution=1&from=" + strconv.FormatInt(from, 10) + "&to=" + strconv.FormatInt(to, 10)
	rangeRec := httptest.NewRecorder()
	module.getFutuKline(rangeRec, httptest.NewRequest(http.MethodGet, rangeURL, nil))
	if rangeRec.Code != 200 {
		t.Fatalf("range kline: %d %s", rangeRec.Code, rangeRec.Body.String())
	}
	if err := json.Unmarshal(rangeRec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Candles) != 2 {
		t.Fatalf("history fallback should merge previous night: %+v", payload.Candles)
	}
	fake.mu.Lock()
	hist = fake.historyN
	fake.mu.Unlock()
	if hist != 1 {
		t.Fatalf("3103 calls=%d", hist)
	}

	ten := httptest.NewRecorder()
	module.getFutuKline(ten, httptest.NewRequest(http.MethodGet, "/api/modules/investment/futu/kline?symbol=AAPL&resolution=10", nil))
	if ten.Code != 200 || !strings.Contains(ten.Body.String(), `"candles":[]`) {
		t.Fatalf("resolution 10: %s", ten.Body.String())
	}
}

func TestFutuCommandLeaseGenerationChangesAfterGatewayReset(t *testing.T) {
	fake := startFakeOpenD(t)
	module := openInvestmentModule(t)
	module.deps.FutuOpenDAddress = fake.addr()
	provider := httptest.NewRecorder()
	module.saveFutu(provider, httptest.NewRequest(http.MethodPut, "/api/modules/investment/futu", strings.NewReader(`{"enabled":true}`)))
	if provider.Code != http.StatusOK {
		t.Fatal(provider.Body.String())
	}
	night := httptest.NewRecorder()
	module.saveOvernight(night, httptest.NewRequest(http.MethodPut, "/api/modules/investment/overnight", strings.NewReader(`{"enabled":true}`)))
	if night.Code != http.StatusOK {
		t.Fatal(night.Body.String())
	}
	command := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		browser := openFutuBrowser(t, module)
		body, _ := json.Marshal(map[string]string{"command": "ADD", "symbol": "AAPL", "resolution": "5", "owner": browser.owner, "generation": browser.generation})
		module.futuCommand(rec, httptest.NewRequest(http.MethodPost, "/api/modules/investment/futu/quote/ws/command", strings.NewReader(string(body))))
		if rec.Code != http.StatusOK {
			t.Fatalf("ADD: %d %s", rec.Code, rec.Body.String())
		}
		var response struct {
			Success    bool   `json:"success"`
			Applied    bool   `json:"applied"`
			Generation string `json:"generation"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || !response.Success || !response.Applied || response.Generation == "" {
			t.Fatalf("online owner ADD must be applied and report its lease generation: %s (%v)", rec.Body.String(), err)
		}
		return response.Generation
	}
	first := command()
	module.futu.reset()
	second := command()
	if first == second {
		t.Fatalf("gateway reset reused the lease generation %q", first)
	}
	other := newFutuGateway(module.futu.deps)
	other.mu.Lock()
	otherGeneration := other.generationTokenLocked()
	other.mu.Unlock()
	if otherGeneration == second {
		t.Fatalf("new gateway instance reused lease generation %q", second)
	}
}

func TestFutuHTTPRejectsDelayedOldGenerationUnsubWithoutChangingReferences(t *testing.T) {
	fake := startFakeOpenD(t)
	module := openInvestmentModule(t)
	module.deps.FutuOpenDAddress = fake.addr()
	provider := httptest.NewRecorder()
	module.saveFutu(provider, httptest.NewRequest(http.MethodPut, "/api/modules/investment/futu", strings.NewReader(`{"enabled":true}`)))
	if provider.Code != http.StatusOK {
		t.Fatal(provider.Body.String())
	}
	night := httptest.NewRecorder()
	module.saveOvernight(night, httptest.NewRequest(http.MethodPut, "/api/modules/investment/overnight", strings.NewReader(`{"enabled":true}`)))
	if night.Code != http.StatusOK {
		t.Fatal(night.Body.String())
	}

	type commandResult struct {
		Success    bool   `json:"success"`
		Applied    bool   `json:"applied"`
		Generation string `json:"generation"`
	}
	command := func(name, generation, owner string) commandResult {
		t.Helper()
		body := map[string]any{"command": name, "symbol": "AAPL", "resolution": "5", "owner": owner}
		if generation != "" {
			body["generation"] = generation
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		module.futuCommand(recorder, httptest.NewRequest(http.MethodPost, "/api/modules/investment/futu/quote/ws/command", strings.NewReader(string(encoded))))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, recorder.Code, recorder.Body.String())
		}
		var result commandResult
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || !result.Success || result.Generation == "" {
			t.Fatalf("invalid %s response: %s (%v)", name, recorder.Body.String(), err)
		}
		return result
	}

	// Tab A's lease belongs to generation 1. Reset, then let tabs B and C
	// establish independent references in generation 2 before A's delayed
	// UNSUB reaches the HTTP handler.
	browserA := openFutuBrowser(t, module)
	tabA := command("ADD", browserA.generation, browserA.owner)
	if !tabA.Applied {
		t.Fatal("online owner ADD was not applied")
	}
	module.futu.reset()
	browserB := openFutuBrowser(t, module)
	tabB := command("ADD", browserB.generation, browserB.owner)
	if !tabB.Applied || tabB.Generation == tabA.Generation {
		t.Fatalf("tab B did not establish a new generation: A=%+v B=%+v", tabA, tabB)
	}
	browserC := openFutuBrowser(t, module)
	if tabC := command("ADD", tabB.Generation, browserC.owner); !tabC.Applied || tabC.Generation != tabB.Generation {
		t.Fatalf("tab C ADD: %+v", tabC)
	}

	if stale := command("UNSUB", tabA.Generation, browserA.owner); stale.Applied || stale.Generation != tabB.Generation {
		t.Fatalf("delayed old UNSUB must be reported unapplied with the current token: %+v", stale)
	}
	if unversioned := command("UNSUB", "", browserB.owner); unversioned.Applied || unversioned.Generation != tabB.Generation {
		t.Fatalf("unversioned UNSUB must not release a current lease: %+v", unversioned)
	}

	_, subType, _ := futuKLTypes("5")
	module.futu.mu.Lock()
	defer module.futu.mu.Unlock()
	for _, sub := range []int{subTypeBasic, subType} {
		lease := module.futu.refs[subKey{symbol: "AAPL", sub: sub}]
		if lease == nil || lease.count != 2 || lease.generation != module.futu.generation {
			t.Fatalf("old UNSUB changed generation-2 subscription %d: generation=%d lease=%+v", sub, module.futu.generation, lease)
		}
	}
}

func TestFutuKlineStrictHistoryRejectsPartialCurrentWindow(t *testing.T) {
	fake := startFakeOpenD(t)
	fake.mu.Lock()
	fake.historyFail = true
	fake.mu.Unlock()
	module := openInvestmentModule(t)
	module.deps.FutuOpenDAddress = fake.addr()
	provider := httptest.NewRecorder()
	module.saveFutu(provider, httptest.NewRequest(http.MethodPut, "/api/modules/investment/futu", strings.NewReader(`{"enabled":true}`)))
	if provider.Code != http.StatusOK {
		t.Fatal(provider.Body.String())
	}
	night := httptest.NewRecorder()
	module.saveOvernight(night, httptest.NewRequest(http.MethodPut, "/api/modules/investment/overnight", strings.NewReader(`{"enabled":true}`)))
	if night.Code != http.StatusOK {
		t.Fatal(night.Body.String())
	}

	from := time.Date(2026, 1, 14, 20, 0, 0, 0, nyZone).Unix()
	to := time.Date(2026, 1, 16, 4, 0, 0, 0, nyZone).Unix()
	// An old edge bar alone cannot prove that the middle of the requested range exists.
	if _, err := module.deps.DB.Exec(`INSERT INTO investment_futu_bars(symbol,resolution,time_ms,open,high,low,close,volume) VALUES(?,?,?,?,?,?,?,?)`,
		"AAPL", "1", from*1000, 100, 100, 100, 100, 1); err != nil {
		t.Fatal(err)
	}
	url := "/api/modules/investment/futu/kline?symbol=AAPL&resolution=1&requireHistory=true&from=" + strconv.FormatInt(from, 10) + "&to=" + strconv.FormatInt(to, 10)
	rec := httptest.NewRecorder()
	module.getFutuKline(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "futu_unavailable") {
		t.Fatalf("incomplete strict history must fail instead of returning current-window bars: %d %s", rec.Code, rec.Body.String())
	}
	fake.mu.Lock()
	historyN, historyBegin, historyEnd := fake.historyN, fake.historyBegin, fake.historyEnd
	fake.mu.Unlock()
	if historyN != 1 || historyBegin != "2026-01-14" || historyEnd != "2026-01-16" {
		t.Fatalf("strict request must retry the entire date range despite an old edge bar, requests=%d range=%s..%s", historyN, historyBegin, historyEnd)
	}
}

func TestFutuResetDuringOldHoldPreservesNewGenerationTabReferences(t *testing.T) {
	fake := startFakeOpenD(t)
	fake.subStarted = make(chan struct{}, 1)
	fake.releaseSub = make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	oldClient, err := dialOpend(ctx, fake.addr(), "workbench-old-lease-test")
	if err != nil {
		t.Fatal(err)
	}
	gateway := &futuGateway{client: oldClient, generation: 1, refs: make(map[subKey]*subLease)}
	key := subKey{symbol: "AAPL", sub: subTypeKL5Min}
	oldHold := make(chan error, 1)
	go func() { oldHold <- gateway.hold(ctx, oldClient, key.symbol, key.sub, true) }()
	select {
	case <-fake.subStarted:
	case <-ctx.Done():
		t.Fatal("old-generation hold never reached the controlled OpenD subscription")
	}
	gateway.reset()
	select {
	case err := <-oldHold:
		if err == nil {
			t.Fatal("hold interrupted by reset unexpectedly succeeded")
		}
	case <-ctx.Done():
		t.Fatal("hold did not notice the reset")
	}
	close(fake.releaseSub)

	currentClient, err := dialOpend(ctx, fake.addr(), "workbench-new-lease-test")
	if err != nil {
		t.Fatal(err)
	}
	gateway.mu.Lock()
	gateway.client = currentClient
	gateway.mu.Unlock()
	for range 2 { // two independent browser tabs restore one reference apiece
		if err := gateway.hold(ctx, currentClient, key.symbol, key.sub, false); err != nil {
			t.Fatal(err)
		}
	}
	gateway.mu.Lock()
	lease := gateway.refs[key]
	generation := gateway.generation
	gateway.mu.Unlock()
	if generation != 2 || lease == nil || lease.generation != generation || lease.count != 2 {
		t.Fatalf("reset-era old hold contaminated new references: generation=%d lease=%+v", generation, lease)
	}
	gateway.reset()
}

func TestFutuExpirySerializesUnsubscribeBeforeSameGenerationNewHold(t *testing.T) {
	fake := startFakeOpenD(t)
	fake.unsubStarted = make(chan struct{}, 1)
	fake.releaseUnsub = make(chan struct{})
	var releaseOnce sync.Once
	releaseUnsub := func() { releaseOnce.Do(func() { close(fake.releaseUnsub) }) }
	defer releaseUnsub()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := dialOpend(ctx, fake.addr(), "workbench-expiry-order-test")
	if err != nil {
		t.Fatal(err)
	}
	gateway := &futuGateway{client: client, generation: 1, refs: make(map[subKey]*subLease)}
	key := subKey{symbol: "AAPL", sub: subTypeKL5Min}
	lease := &subLease{since: time.Now(), generation: 1}
	gateway.refs[key] = lease
	client.mu.Lock()
	initialSerial := client.serial
	client.mu.Unlock()

	expiryDone := make(chan struct{})
	go func() {
		gateway.expireSub(client, 1, key, lease, key.symbol, key.sub)
		close(expiryDone)
	}()
	select {
	case <-fake.unsubStarted:
	case <-ctx.Done():
		t.Fatal("lease expiry did not reach controlled UNSUB")
	}
	unsubSerial := func() uint32 {
		client.mu.Lock()
		defer client.mu.Unlock()
		return client.serial
	}()
	if unsubSerial != initialSerial+1 {
		t.Fatalf("UNSUB request serial=%d want %d", unsubSerial, initialSerial+1)
	}

	newHold := make(chan error, 1)
	go func() { newHold <- gateway.hold(ctx, client, key.symbol, key.sub, true) }()
	// The in-flight network UNSUB must finish before a same-generation SUB is
	// sent, while the gateway mutex remains available to unrelated work.
	mutexAvailable := make(chan struct{})
	go func() { gateway.mu.Lock(); gateway.mu.Unlock(); close(mutexAvailable) }()
	select {
	case <-mutexAvailable:
	case <-ctx.Done():
		t.Fatal("gateway mutex was held while the timer awaited OpenD")
	}
	timer := time.NewTimer(40 * time.Millisecond)
	select {
	case err := <-newHold:
		t.Fatalf("new hold overtook the pending timer UNSUB: %v", err)
	case <-timer.C:
	}
	client.mu.Lock()
	serialBeforeUnsubCompletes := client.serial
	client.mu.Unlock()
	if serialBeforeUnsubCompletes != unsubSerial {
		t.Fatalf("new SUB was sent before UNSUB completed: serial=%d want %d", serialBeforeUnsubCompletes, unsubSerial)
	}

	releaseUnsub()
	select {
	case <-expiryDone:
	case <-ctx.Done():
		t.Fatal("timer UNSUB did not finish")
	}
	select {
	case err := <-newHold:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("new hold did not run after the serialized UNSUB")
	}
	fake.mu.Lock()
	actions := append([]bool(nil), fake.subActions...)
	fake.mu.Unlock()
	if len(actions) != 2 || actions[0] || !actions[1] {
		t.Fatalf("same-generation sub operations ran in wrong order: %v", actions)
	}
	gateway.mu.Lock()
	currentLease := gateway.refs[key]
	gateway.mu.Unlock()
	if currentLease == nil || currentLease.count != 1 || currentLease.generation != 1 {
		t.Fatalf("timer removed the new same-generation lease: %+v", currentLease)
	}
	gateway.reset()
}

func TestFutuStaleClientCannotHoldReleaseOrExpireNewGenerationLeases(t *testing.T) {
	newClient := func() *opendClient {
		return &opendClient{conn: discardConn{}, pending: make(map[uint32]chan opendReply), stop: make(chan struct{})}
	}
	oldClient := newClient()
	gateway := &futuGateway{client: oldClient, generation: 1, refs: make(map[subKey]*subLease)}
	ctx := context.Background()
	key := subKey{symbol: "AAPL", sub: subTypeKL5Min}

	// Two old-generation tabs hold the same lease. Both can release their own
	// references; the final release leaves a timer bound to that exact lease.
	if err := gateway.hold(ctx, oldClient, key.symbol, key.sub, false); err != nil {
		t.Fatal(err)
	}
	if err := gateway.hold(ctx, oldClient, key.symbol, key.sub, false); err != nil {
		t.Fatal(err)
	}
	oldLease := gateway.refs[key]
	if err := gateway.release(ctx, oldClient, key.symbol, key.sub); err != nil {
		t.Fatal(err)
	}
	if err := gateway.release(ctx, oldClient, key.symbol, key.sub); err != nil {
		t.Fatal(err)
	}
	if oldLease.count != 0 || oldLease.idle == nil {
		t.Fatalf("old lease was not scheduled for release: %+v", oldLease)
	}
	oldGeneration := gateway.generation

	gateway.reset()
	currentClient := newClient()
	gateway.mu.Lock()
	gateway.client = currentClient
	gateway.mu.Unlock()
	if err := gateway.hold(ctx, currentClient, key.symbol, key.sub, false); err != nil {
		t.Fatal(err)
	}
	if err := gateway.hold(ctx, currentClient, key.symbol, key.sub, false); err != nil {
		t.Fatal(err)
	}
	currentLease := gateway.refs[key]
	if currentLease.count != 2 {
		t.Fatalf("new generation should have two independent tab references, got %d", currentLease.count)
	}

	if err := gateway.hold(ctx, oldClient, key.symbol, key.sub, false); err == nil {
		t.Fatal("stale client hold unexpectedly succeeded")
	}
	if err := gateway.release(ctx, oldClient, key.symbol, key.sub); err == nil {
		t.Fatal("stale client release unexpectedly succeeded")
	}
	gateway.expireSub(oldClient, oldGeneration, key, oldLease, key.symbol, key.sub)
	if gateway.refs[key] != currentLease || currentLease.count != 2 {
		t.Fatalf("old hold/release/timer corrupted current-generation lease: refs=%p current=%p count=%d", gateway.refs[key], currentLease, currentLease.count)
	}
	gateway.reset()
}

func TestFutuDisconnectStopsOverlay(t *testing.T) {
	fake := startFakeOpenD(t)
	module := openInvestmentModule(t)
	module.deps.FutuOpenDAddress = fake.addr()
	save := httptest.NewRecorder()
	module.saveFutu(save, httptest.NewRequest(http.MethodPut, "/api/modules/investment/futu", strings.NewReader(`{"enabled":true}`)))
	if save.Code != 200 {
		t.Fatal(save.Body.String())
	}
	off := httptest.NewRecorder()
	module.disconnectFutu(off, httptest.NewRequest(http.MethodPost, "/api/modules/investment/futu/disconnect", strings.NewReader("{}")))
	if off.Code != 200 {
		t.Fatal(off.Body.String())
	}
	got := httptest.NewRecorder()
	module.getFutu(got, httptest.NewRequest(http.MethodGet, "/api/modules/investment/futu", nil))
	if !strings.Contains(got.Body.String(), `"enabled":false`) {
		t.Fatal(got.Body.String())
	}
}
