package investment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type futuBrowser struct {
	conn       *websocket.Conn
	owner      string
	generation string
}

type futuCommandResult struct {
	Success    bool   `json:"success"`
	Applied    bool   `json:"applied"`
	Generation string `json:"generation"`
	Owner      string `json:"owner"`
}

func newFutuBrowserFixture(t *testing.T) (*Module, *fakeOpenD) {
	t.Helper()
	fake := startFakeOpenD(t)
	module := openInvestmentModule(t)
	module.deps.FutuOpenDAddress = fake.addr()
	if err := module.storeFutu(context.Background(), futuRecord{Enabled: true, OvernightEnabled: true}); err != nil {
		t.Fatal(err)
	}
	return module, fake
}

func openFutuBrowser(t *testing.T, module *Module) futuBrowser {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(module.serveFutuStream))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var ready struct {
		Stream struct {
			Status     string `json:"status"`
			Generation string `json:"generation"`
			Owner      string `json:"owner"`
		} `json:"stream"`
	}
	if err := conn.ReadJSON(&ready); err != nil {
		t.Fatal(err)
	}
	if ready.Stream.Status != "ready" || ready.Stream.Generation == "" || ready.Stream.Owner == "" {
		t.Fatalf("invalid ready: %+v", ready)
	}
	return futuBrowser{conn: conn, owner: ready.Stream.Owner, generation: ready.Stream.Generation}
}

func futuBrowserCommand(t *testing.T, module *Module, browser futuBrowser, command, resolution string) futuCommandResult {
	t.Helper()
	encoded, err := json.Marshal(map[string]string{"command": command, "symbol": "AAPL", "resolution": resolution, "generation": browser.generation, "owner": browser.owner})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	module.futuCommand(recorder, httptest.NewRequest(http.MethodPost, "/api/modules/investment/futu/quote/ws/command", strings.NewReader(string(encoded))))
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", command, recorder.Code, recorder.Body.String())
	}
	var result futuCommandResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || !result.Success || result.Generation == "" || result.Owner != browser.owner {
		t.Fatalf("invalid command response: %s (%v)", recorder.Body.String(), err)
	}
	return result
}

func assertFutuBrowserCounts(t *testing.T, gateway *futuGateway, basic, kline int) {
	t.Helper()
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	for sub, want := range map[int]int{subTypeBasic: basic, subTypeKL5Min: kline} {
		lease := gateway.refs[subKey{symbol: "AAPL", sub: sub}]
		if lease == nil && want == 0 {
			continue
		}
		if lease == nil || lease.count != want || lease.generation != gateway.generation {
			t.Fatalf("sub %d: want count=%d generation=%d, got %+v", sub, want, gateway.generation, lease)
		}
	}
}

func waitFutuBrowserRemoved(t *testing.T, gateway *futuGateway, owner string, basic, kline int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		gateway.mu.Lock()
		online := false
		for client := range gateway.clients {
			online = online || client.owner == owner
		}
		countsMatch := true
		for sub, want := range map[int]int{subTypeBasic: basic, subTypeKL5Min: kline} {
			lease := gateway.refs[subKey{symbol: "AAPL", sub: sub}]
			countsMatch = countsMatch && ((lease == nil && want == 0) || (lease != nil && lease.count == want))
		}
		gateway.mu.Unlock()
		if !online && countsMatch {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("disconnected owner's references were not reclaimed")
}

func TestFutuBrowserOwnersAreIdempotentAndDisconnectIndependently(t *testing.T) {
	module, _ := newFutuBrowserFixture(t)
	a, b := openFutuBrowser(t, module), openFutuBrowser(t, module)
	if a.owner == b.owner || a.generation != b.generation {
		t.Fatalf("tabs must have independent owners in one generation: A=%+v B=%+v", a, b)
	}
	for _, browser := range []futuBrowser{a, b} {
		for range 2 {
			if result := futuBrowserCommand(t, module, browser, "ADD", "5"); !result.Applied {
				t.Fatal("ADD was not applied")
			}
		}
	}
	for range 2 {
		futuBrowserCommand(t, module, a, "ADD", "")
	}
	assertFutuBrowserCounts(t, module.futu, 3, 2)
	module.futu.mu.Lock()
	opend := module.futu.client
	module.futu.mu.Unlock()
	if err := module.futu.hold(context.Background(), opend, "AAPL", subTypeBasic, true); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		futuBrowserCommand(t, module, a, "UNSUB", "5")
	}
	assertFutuBrowserCounts(t, module.futu, 3, 1)
	_ = a.conn.Close() // no UNSUB for the quote-only reference
	waitFutuBrowserRemoved(t, module.futu, a.owner, 2, 1)
	_ = b.conn.Close() // no UNSUB for either BASIC or K-line
	waitFutuBrowserRemoved(t, module.futu, b.owner, 1, 0)

	c := openFutuBrowser(t, module)
	if c.owner == a.owner || c.owner == b.owner || c.generation != a.generation {
		t.Fatalf("reconnection must replace only the owner: A=%+v B=%+v C=%+v", a, b, c)
	}
	futuBrowserCommand(t, module, c, "ADD", "5")
	for _, stale := range []futuBrowser{a, b, {owner: c.owner, generation: "old-generation"}, {owner: c.owner}} {
		for _, command := range []string{"ADD", "UNSUB"} {
			if result := futuBrowserCommand(t, module, stale, command, "5"); result.Applied {
				t.Fatalf("stale owner/generation applied: %+v", result)
			}
		}
	}
	assertFutuBrowserCounts(t, module.futu, 2, 1)
	module.futu.mu.Lock()
	sameOpenD := module.futu.client == opend
	module.futu.mu.Unlock()
	if !sameOpenD {
		t.Fatal("browser disconnect reset the shared OpenD connection")
	}
	_ = c.conn.Close()
	waitFutuBrowserRemoved(t, module.futu, c.owner, 1, 0)
	if err := module.futu.release(context.Background(), opend, "AAPL", subTypeBasic); err != nil {
		t.Fatal(err)
	}
	assertFutuBrowserCounts(t, module.futu, 0, 0)
}

func TestFutuBrowserConcurrentDuplicateCommands(t *testing.T) {
	module, _ := newFutuBrowserFixture(t)
	browser := openFutuBrowser(t, module)
	for _, command := range []string{"ADD", "UNSUB"} {
		var wg sync.WaitGroup
		for range 16 {
			wg.Go(func() {
				_, applied, err := module.futu.browserCommand(context.Background(), command, browser.owner, browser.generation, "AAPL", "5")
				if err != nil || !applied {
					t.Errorf("%s: applied=%v err=%v", command, applied, err)
				}
			})
		}
		wg.Wait()
		count := 0
		if command == "ADD" {
			count = 1
		}
		assertFutuBrowserCounts(t, module.futu, count, count)
	}
}

func TestFutuBrowserDisconnectSerializesInFlightAdd(t *testing.T) {
	module, fake := newFutuBrowserFixture(t)
	fake.subStarted = make(chan struct{}, 1)
	fake.releaseSub = make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(fake.releaseSub) }) }
	defer release()
	browser := openFutuBrowser(t, module)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, applied, err := module.futu.browserCommand(ctx, "ADD", browser.owner, browser.generation, "AAPL", "5")
		if err != nil || applied {
			t.Errorf("disconnected in-flight ADD: applied=%v err=%v", applied, err)
		}
	}()
	select {
	case <-fake.subStarted:
	case <-ctx.Done():
		t.Fatal("ADD never reached OpenD")
	}
	_ = browser.conn.Close()
	// The owner is invalidated before cleanup waits on the in-flight command.
	deadline := time.Now().Add(time.Second)
	for {
		module.futu.mu.Lock()
		online := false
		for client := range module.futu.clients {
			online = online || client.owner == browser.owner
		}
		module.futu.mu.Unlock()
		if !online {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnect held the gateway mutex behind the network ADD")
		}
		time.Sleep(time.Millisecond)
	}
	if result := futuBrowserCommand(t, module, browser, "UNSUB", "5"); result.Applied {
		t.Fatal("disconnected owner accepted a new command")
	}
	release()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("in-flight ADD did not finish")
	}
	waitFutuBrowserRemoved(t, module.futu, browser.owner, 0, 0)
	assertFutuBrowserCounts(t, module.futu, 0, 0)
}

func TestFutuBrowserUnsupportedResolutionDoesNotRecordLease(t *testing.T) {
	module, _ := newFutuBrowserFixture(t)
	browser := openFutuBrowser(t, module)
	for _, resolution := range []string{"10", "unsupported"} {
		for _, command := range []string{"ADD", "UNSUB"} {
			if result := futuBrowserCommand(t, module, browser, command, resolution); !result.Applied {
				t.Fatalf("unsupported resolution should remain a safe no-op: %+v", result)
			}
		}
	}
	module.futu.mu.Lock()
	var client *futuWSClient
	for candidate := range module.futu.clients {
		if candidate.owner == browser.owner {
			client = candidate
		}
	}
	module.futu.mu.Unlock()
	client.opMu.Lock()
	defer client.opMu.Unlock()
	if len(client.subs) != 0 {
		t.Fatalf("unsupported resolution recorded owner leases: %+v", client.subs)
	}
	assertFutuBrowserCounts(t, module.futu, 0, 0)
}

func TestFutuBrowserRepeatedRemoveAfterResetPreservesNewOwner(t *testing.T) {
	module, _ := newFutuBrowserFixture(t)
	old := openFutuBrowser(t, module)
	futuBrowserCommand(t, module, old, "ADD", "5")
	module.futu.mu.Lock()
	var oldClient *futuWSClient
	for candidate := range module.futu.clients {
		if candidate.owner == old.owner {
			oldClient = candidate
		}
	}
	module.futu.mu.Unlock()
	module.futu.reset()
	current := openFutuBrowser(t, module)
	futuBrowserCommand(t, module, current, "ADD", "5")
	module.futu.removeWS(oldClient)
	module.futu.removeWS(oldClient)
	assertFutuBrowserCounts(t, module.futu, 1, 1)
	if result := futuBrowserCommand(t, module, old, "UNSUB", "5"); result.Applied {
		t.Fatal("old reset owner released a current owner's references")
	}
}

func TestFutuHTTPRequiresBrowserOwner(t *testing.T) {
	module, _ := newFutuBrowserFixture(t)
	for _, command := range []string{"ADD", "UNSUB"} {
		recorder := httptest.NewRecorder()
		module.futuCommand(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"command":"`+command+`","symbol":"AAPL","resolution":"5"}`)))
		if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"futu_client_required"`) {
			t.Fatalf("missing owner: %d %s", recorder.Code, recorder.Body.String())
		}
	}
	assertFutuBrowserCounts(t, module.futu, 0, 0)
}
