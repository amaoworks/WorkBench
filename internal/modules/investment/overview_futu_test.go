package investment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func overviewWatchlist(t *testing.T, m *Module, ctx context.Context, symbols ...string) {
	t.Helper()
	encoded, err := json.Marshal(append([]string{"###分类"}, symbols...))
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"revision":0,"writer":"overview-test","sequence":1,"state":{"lists":[{"id":"active","title":"自选","symbols":%s},{"id":"inactive","title":"其他","symbols":["NVDA"]}],"activeId":"active"}}`, encoded)
	response := httptest.NewRecorder()
	m.saveWatchlists(response, httptest.NewRequest("PUT", "/", strings.NewReader(body)).WithContext(ctx))
	requireStatus(t, response, http.StatusOK)
}

type overviewOpenD struct {
	ln           net.Listener
	mu           sync.Mutex
	basicQotList []map[string]any
	quoteN       int
	quoteError   string
}

func (f *overviewOpenD) addr() string { return f.ln.Addr().String() }

func (f *overviewOpenD) serve(conn net.Conn) {
	defer conn.Close()
	for {
		proto, serial, _, err := readOpendPacket(conn)
		if err != nil {
			return
		}
		f.mu.Lock()
		payload := map[string]any{}
		retType, retMsg := retSucceed, ""
		switch proto {
		case protoInitConnect:
			payload = map[string]any{"keepAliveInterval": 30, "serverVer": 1010, "loginUserID": 1, "connID": 1, "connAESKey": "0123456789abcdef"}
		case protoKeepAlive:
			payload = map[string]any{"time": time.Now().Unix()}
		case protoGetGlobalState:
			payload = map[string]any{"qotLogined": true}
		case protoQotGetStaticInfo:
			list := []any{}
			for _, row := range f.basicQotList {
				secType := row["secType"]
				if secType == nil {
					secType = 3
				}
				list = append(list, map[string]any{"basic": map[string]any{"security": row["security"], "secType": secType, "name": row["name"], "exchType": 5}})
			}
			payload = map[string]any{"staticInfoList": list}
		case protoQotGetBasicQot:
			f.quoteN++
			payload = map[string]any{"basicQotList": f.basicQotList}
			if f.quoteError != "" {
				retType, retMsg = -1, f.quoteError
			}
		case protoQotGetSubInfo:
			payload = map[string]any{"remainQuota": 90, "totalQuota": 100}
		}
		response, _ := json.Marshal(map[string]any{"retType": retType, "retMsg": retMsg, "errCode": 0, "s2c": payload})
		f.mu.Unlock()
		if writeOpendPacket(conn, proto, serial, response) != nil {
			return
		}
	}
}

func overviewFutuFixture(t *testing.T, m *Module, now time.Time) *overviewOpenD {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &overviewOpenD{ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go fake.serve(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	m.deps.FutuOpenDAddress = fake.addr()
	m.futu.deps.load = m.loadFutu
	if err := m.storeFutu(testContext(context.Background()), futuRecord{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.basicQotList = []map[string]any{
		{"security": map[string]any{"market": marketUS, "code": "AAPL"}, "name": "Apple", "curPrice": 105.0, "lastClosePrice": 100.0, "updateTimestamp": now.Add(-2 * time.Minute).Unix()},
		{"security": map[string]any{"market": marketUS, "code": "MSFT"}, "name": "Microsoft", "curPrice": 210.0, "lastClosePrice": 200.0, "updateTimestamp": now.Add(-2 * time.Minute).Unix()},
	}
	fake.mu.Unlock()
	return fake
}

func TestOverviewFutuQuotesWithoutPersonalSchwab(t *testing.T) {
	for _, reauth := range []bool{false, true} {
		t.Run(fmt.Sprintf("reauth=%t", reauth), func(t *testing.T) {
			m := openInvestmentModule(t)
			now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
			m.now = func() time.Time { return now }
			var schwabCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				schwabCalls.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer upstream.Close()
			m.schwabAPI = upstream.URL
			if reauth {
				seedSchwab(t, m, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://workbench.test/oauth/schwab", RefreshToken: "expired", ReauthorizationRequired: true})
			}
			fake := overviewFutuFixture(t, m, now)
			overviewWatchlist(t, m, testContext(context.Background()), "NASDAQ:AAPL")
			got := m.overviewQuotes(testContext(context.Background()))
			if got.Status != "ok" || got.Provider != "futu" || len(got.Items) != 1 || got.Items[0].Symbol != "AAPL" || got.Items[0].Price != 105 || got.Items[0].ChangePct == nil || *got.Items[0].ChangePct != 5 || !got.Items[0].Watched || got.Items[0].Held || len(got.Items[0].Spark) != 0 {
				t.Fatalf("Futu watchlist: %+v", got)
			}
			if got.UpdatedAt == nil || !got.UpdatedAt.Equal(now.Add(-2*time.Minute)) {
				t.Fatalf("quote timestamp must come from upstream, got %+v", got.UpdatedAt)
			}
			fake.mu.Lock()
			firstCalls := fake.quoteN
			fake.mu.Unlock()
			m.overviewQuotes(testContext(context.Background()))
			fake.mu.Lock()
			quoteCalls := fake.quoteN
			fake.mu.Unlock()
			if quoteCalls == 0 || quoteCalls != firstCalls || schwabCalls.Load() != 0 {
				t.Fatalf("Futu first=%d cached=%d Schwab=%d", firstCalls, quoteCalls, schwabCalls.Load())
			}
			snapshot := m.overviewSnapshot(testContext(context.Background()))
			if snapshot.Status == "ok" || snapshot.NetWorth != 0 || snapshot.Accounts != 0 || len(snapshot.Symbols) != 0 || schwabCalls.Load() != 0 {
				t.Fatalf("shared quotes must not create personal assets: %+v", snapshot)
			}
			response := httptest.NewRecorder()
			m.getOverviewQuotes(response, testRequest("GET", "/", nil))
			if !strings.Contains(response.Body.String(), `"provider":"futu"`) || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(response.Body.String())
			}
		})
	}
}

func TestOverviewFutuMixedWatchlistKeepsEquitiesAndUpstreamErrors(t *testing.T) {
	m := openInvestmentModule(t)
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	fake := overviewFutuFixture(t, m, now)
	fake.mu.Lock()
	fake.basicQotList[1]["security"] = map[string]any{"market": marketUS, "code": "SPY"}
	fake.basicQotList[1]["secType"] = 4
	fake.mu.Unlock()
	ctx := testContext(context.Background())
	overviewWatchlist(t, m, ctx, "NASDAQ:AAPL", "/ES", "$SPX", "AMEX:SPY")
	got := m.overviewQuotes(ctx)
	if got.Status != "ok" || got.Provider != "futu" || len(got.Items) != 2 || got.Items[0].Symbol != "AAPL" || got.Items[1].Symbol != "SPY" {
		t.Fatalf("mixed watchlist: %+v", got)
	}
	for _, item := range got.Items {
		if !item.Watched || item.Held || item.Price <= 0 {
			t.Fatalf("equity/ETF quote: %+v", item)
		}
	}
	// The public quote endpoint still rejects unsupported input.
	if _, err := m.futuQuotes(ctx, []string{"AAPL", "/ES", "$SPX"}); err == nil {
		t.Fatal("public Futu quotes accepted unsupported symbols")
	}
	fake.mu.Lock()
	fake.quoteError = "行情权限不足，订阅额度已用完"
	fake.mu.Unlock()
	now = now.Add(overviewQuotesTTL + time.Second)
	got = m.overviewQuotes(ctx)
	if got.Status != "error" || len(got.Items) != 0 || !strings.Contains(got.Message, "行情权限不足，订阅额度已用完") {
		t.Fatalf("mixed watchlist hid upstream failure: %+v", got)
	}
}

func TestOverviewFutuUnsupportedWatchlistDoesNotFetch(t *testing.T) {
	m := openInvestmentModule(t)
	m.deps.FutuOpenDAddress = "127.0.0.1:1"
	ctx := testContext(context.Background())
	if err := m.storeFutu(ctx, futuRecord{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	overviewWatchlist(t, m, ctx, "/ES", "$SPX")
	got := m.overviewQuotes(ctx)
	if got.Status != "ok" || got.Provider != "futu" || len(got.Items) != 0 || got.UpdatedAt != nil {
		t.Fatalf("unsupported watchlist must not request upstream: %+v", got)
	}
}

func TestOverviewFutuEmptyWatchlistDoesNotFetch(t *testing.T) {
	m := openInvestmentModule(t)
	m.deps.FutuOpenDAddress = "127.0.0.1:1"
	if err := m.storeFutu(testContext(context.Background()), futuRecord{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got := m.overviewQuotes(testContext(context.Background()))
	if got.Status != "ok" || got.Provider != "futu" || len(got.Items) != 0 || got.UpdatedAt != nil {
		t.Fatalf("empty personal watchlist: %+v", got)
	}
}

func TestOverviewQuoteProviderSwitchClearsCachedPrices(t *testing.T) {
	m := openInvestmentModule(t)
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	fake := overviewFutuFixture(t, m, now)
	overviewWatchlist(t, m, testContext(context.Background()), "AAPL")
	var schwabQuotes atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer own-token" {
			t.Errorf("unexpected token %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/trader/v1/accounts":
			_, _ = io.WriteString(w, `[{"securitiesAccount":{"currentBalances":{"liquidationValue":1000},"positions":[{"marketValue":1000,"instrument":{"assetType":"EQUITY","symbol":"AAPL"}}]}}]`)
		case "/marketdata/v1/quotes":
			price := 200 + schwabQuotes.Add(1)
			_, _ = fmt.Fprintf(w, `{"AAPL":{"quote":{"lastPrice":%d},"regular":{"regularMarketLastPrice":%d}}}`, price, price)
		case "/marketdata/v1/pricehistory":
			_, _ = io.WriteString(w, `{"candles":[{"close":199},{"close":200}]}`)
		default:
			t.Errorf("unexpected Schwab request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	m.schwabAPI = upstream.URL
	seedSchwab(t, m, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://workbench.test/oauth/schwab", AccessToken: "own-token", RefreshToken: "own-refresh", TokenExpiresAt: now.Add(time.Hour), AuthorizedAt: now})
	assertProvider := func(provider string, price float64) {
		t.Helper()
		got := m.overviewQuotes(testContext(context.Background()))
		if got.Status != "ok" || got.Provider != provider || len(got.Items) != 1 || got.Items[0].Price != price || got.Items[0].Held != (provider == "schwab") {
			t.Fatalf("provider %s: %+v", provider, got)
		}
		if provider == "futu" && len(got.Items[0].Spark) != 0 {
			t.Fatal("Schwab trend leaked into Futu quote")
		}
	}
	setReauth := func(required bool) {
		t.Helper()
		rec, err := m.loadSchwab(testContext(context.Background()))
		if err != nil {
			t.Fatal(err)
		}
		rec.ReauthorizationRequired = required
		if err := m.storeSchwab(testContext(context.Background()), rec); err != nil {
			t.Fatal(err)
		}
	}
	assertProvider("schwab", 201)
	fake.mu.Lock()
	futuCalls := fake.quoteN
	fake.mu.Unlock()
	if futuCalls != 0 {
		t.Fatal("connected Schwab user must not request shared Futu quotes")
	}
	setReauth(true)
	assertProvider("futu", 105)
	setReauth(false)
	assertProvider("schwab", 202)
	setReauth(true)
	assertProvider("futu", 105)
	now = now.Add(time.Second)
	if err := m.storeFutu(testContext(context.Background()), futuRecord{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if got := m.overviewQuotes(testContext(context.Background())); got.Provider != "none" || got.Status != "reauth" || len(got.Items) != 0 {
		t.Fatalf("disabled source returned cached prices: %+v", got)
	}
	fake.mu.Lock()
	fake.basicQotList[0]["curPrice"] = 106.0
	fake.mu.Unlock()
	now = now.Add(time.Second)
	if err := m.storeFutu(testContext(context.Background()), futuRecord{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	assertProvider("futu", 106)
}

func TestOverviewSharedFutuKeepsWatchlistsAndSchwabCachesPrivate(t *testing.T) {
	root, router, _ := multiInvestment(t)
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	root.now = func() time.Time { return now }
	var schwabCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		schwabCalls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()
	root.schwabAPI = upstream.URL
	owner := root.userModule("owner")
	owner.now, owner.schwabAPI = root.now, upstream.URL
	seedSchwab(t, owner, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://workbench.test/oauth/schwab", AccessToken: "private-admin-token", RefreshToken: "private-admin-refresh", AuthorizedAt: now, TokenExpiresAt: now.Add(time.Hour)})
	fake := overviewFutuFixture(t, owner, now)
	root.deps.FutuOpenDAddress = fake.addr()
	// A warm administrator cache is not a source of other users' symbols or assets.
	owner.overview.snapshot = &overviewSnapshot{Status: "ok", NetWorth: 900000, Symbols: []string{"SECRET"}, UpdatedAt: &now}
	owner.overview.quotes = map[string]overviewQuotesEntry{"regular:SECRET": {at: now, items: []overviewQuote{{Symbol: "SECRET", Price: 999, Held: true}}}}
	for user, symbol := range map[string]string{"alice": "AAPL", "bob": "MSFT"} {
		m := root.userModule(user)
		overviewWatchlist(t, m, investorContext(user), symbol)
	}
	for user, symbol := range map[string]string{"alice": "AAPL", "bob": "MSFT"} {
		response := investorRequest(router, investorContext(user), "GET", "/api/modules/investment/overview/quotes", "")
		requireStatus(t, response, http.StatusOK)
		var got overviewQuotes
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Provider != "futu" || got.Status != "ok" || len(got.Items) != 1 || got.Items[0].Symbol != symbol || got.Items[0].Held {
			t.Fatalf("%s watchlist isolation: %s", user, response.Body.String())
		}
		assets := investorRequest(router, investorContext(user), "GET", "/api/modules/investment/overview/snapshot", "")
		if strings.Contains(assets.Body.String(), "SECRET") || strings.Contains(assets.Body.String(), "900000") {
			t.Fatalf("administrator assets leaked: %s", assets.Body.String())
		}
	}
	if schwabCalls.Load() != 0 {
		t.Fatalf("unbound users used a Schwab token: calls=%d", schwabCalls.Load())
	}
	requireStatus(t, investorRequest(router, context.Background(), "GET", "/api/modules/investment/overview/quotes", ""), http.StatusUnauthorized)
	// Even direct calls cannot use another user's module cache with a mismatched principal.
	if got := root.userModule("alice").overviewQuotes(investorContext("bob")); got.Status != "error" || got.Provider != "none" || len(got.Items) != 0 {
		t.Fatalf("mismatched identity: %+v", got)
	}
}

func TestOverviewFutuFailureDoesNotPresentOldPricesAsLive(t *testing.T) {
	m := openInvestmentModule(t)
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	fake := overviewFutuFixture(t, m, now)
	overviewWatchlist(t, m, testContext(context.Background()), "AAPL")
	if got := m.overviewQuotes(testContext(context.Background())); got.Status != "ok" || len(got.Items) != 1 {
		t.Fatalf("initial quotes: %+v", got)
	}
	fake.mu.Lock()
	fake.quoteError = "行情权限不足，订阅额度已用完"
	fake.mu.Unlock()
	now = now.Add(overviewQuotesTTL + time.Second)
	got := m.overviewQuotes(testContext(context.Background()))
	if got.Provider != "futu" || got.Status != "error" || !strings.Contains(got.Message, "行情权限不足，订阅额度已用完") || len(got.Items) != 0 || got.UpdatedAt != nil {
		t.Fatalf("upstream permission/quota error must be preserved: %+v", got)
	}
	_ = fake.ln.Close()
	m.futu.reset()
	now = now.Add(time.Second)
	got = m.overviewQuotes(testContext(context.Background()))
	if got.Provider != "futu" || got.Status != "error" || got.Message == "" || len(got.Items) != 0 || got.UpdatedAt != nil {
		t.Fatalf("failed upstream must not reuse cached prices: %+v", got)
	}
}
