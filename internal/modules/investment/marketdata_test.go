package investment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestMarketDataRoutingRequiresUserAndNeverSharesSchwabGrant(t *testing.T) {
	root, router, _ := multiInvestment(t)
	prefix := "/api/modules/investment"
	for _, path := range []string{"/market-data", "/futu/quotes?symbols=AAPL", "/futu/symbols?query=AAPL", "/futu/kline?symbol=AAPL&resolution=1&session=regular", "/futu/quote/ws?mode=main"} {
		requireStatus(t, investorRequest(router, context.Background(), "GET", prefix+path, ""), 401)
	}
	status := func(user, provider string) {
		t.Helper()
		response := investorRequest(router, investorContext(user), "GET", prefix+"/market-data", "")
		requireStatus(t, response, 200)
		var got marketDataView
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Provider != provider {
			t.Fatalf("%s source = %+v, want %s", user, got, provider)
		}
		if strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "token-owner") {
			t.Fatal("credentials in market status")
		}
	}
	status("alice", "none")
	if err := root.storeFutu(investorContext("owner"), futuRecord{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	status("alice", "futu")
	owner := root.userModule("owner")
	seedSchwab(t, owner, schwabRecord{AppKey: "shared-key", AppSecret: "secret", CallbackURL: "https://example.test/oauth/schwab", AccessToken: "token-owner", RefreshToken: "refresh-owner", TokenExpiresAt: time.Now().Add(time.Hour)})
	status("owner", "schwab")
	status("alice", "futu")
	rec, _ := owner.loadSchwab(investorContext("owner"))
	rec.ReauthorizationRequired = true
	if err := owner.storeSchwab(investorContext("owner"), rec); err != nil {
		t.Fatal(err)
	}
	status("owner", "futu")
}

func mainFutuFixture(t *testing.T) (*Module, *fakeOpenD) {
	t.Helper()
	m, fake := openInvestmentModule(t), startFakeOpenD(t)
	m.deps.FutuOpenDAddress = fake.addr()
	if err := m.storeFutu(testContext(context.Background()), futuRecord{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return m, fake
}

func TestFutuMainHistorySessionsAndCacheAreSeparateFromNight(t *testing.T) {
	m, fake := mainFutuFixture(t)
	from := time.Date(2026, 1, 15, 0, 0, 0, 0, nyZone)
	fake.klList = []map[string]any{}
	fake.histList = []map[string]any{klJSON(from.Add(8*time.Hour), 90), klJSON(from.Add(10*time.Hour), 100), klJSON(from.Add(17*time.Hour), 110), klJSON(from.Add(21*time.Hour), 120)}
	get := func(session string, status int) []futuBar {
		t.Helper()
		r := httptest.NewRecorder()
		url := "/futu/kline?symbol=AAPL&resolution=5&session=" + session + "&from=" + strconv.FormatInt(from.Unix(), 10) + "&to=" + strconv.FormatInt(from.Add(24*time.Hour).Unix(), 10)
		m.getFutuKline(r, testRequest("GET", url, nil))
		requireStatus(t, r, status)
		var data struct{ Candles []futuBar }
		if status == 200 {
			if err := json.Unmarshal(r.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
		}
		return data.Candles
	}
	if bars := get("regular", 200); len(bars) != 1 || bars[0].Close != 100 {
		t.Fatal(bars)
	}
	if fake.historySession != sessionRTH {
		t.Fatal("regular used non-RTH history")
	}
	if bars := get("extended", 200); len(bars) != 3 {
		t.Fatal(bars)
	}
	if fake.historySession != sessionETH {
		t.Fatal("extended used wrong upstream session")
	}
	get("night", 409)
	get("equity24h", 409)
	var nightCount int
	if err := m.deps.DB.QueryRow(`SELECT COUNT(*) FROM investment_futu_bars WHERE resolution='5'`).Scan(&nightCount); err != nil || nightCount != 0 {
		t.Fatal("main polluted old night cache", nightCount, err)
	}
	rec, _ := m.loadFutu(testContext(context.Background()))
	rec.OvernightEnabled = true
	if err := m.storeFutu(testContext(context.Background()), rec); err != nil {
		t.Fatal(err)
	}
	if bars := get("equity24h", 200); len(bars) != 4 {
		t.Fatal(bars)
	}
	if fake.historySession != sessionALL {
		t.Fatal("24H history did not request ALL")
	}
	if bars := get("regular", 200); len(bars) != 1 {
		t.Fatal("24H contaminated regular cache", bars)
	}
	fake.remain = 0
	r := httptest.NewRecorder()
	m.getFutuKline(r, testRequest("GET", "/futu/kline?symbol=MSFT&resolution=1&session=regular&from=1768478400&to=1768564800", nil))
	requireStatus(t, r, 503)
	if !strings.Contains(r.Body.String(), "额度不足") {
		t.Fatal(r.Body.String())
	}
}

func TestFutuMainDailyWeeklyMonthlyAndUnsupportedSymbols(t *testing.T) {
	m, fake := mainFutuFixture(t)
	day := time.Date(2026, 1, 15, 0, 0, 0, 0, nyZone)
	fake.klList = []map[string]any{klJSON(day, 100)}
	fake.histList = []map[string]any{klJSON(day, 100)}
	for _, tc := range []struct {
		resolution string
		kind, sub  int
	}{{"1D", 2, 6}, {"1W", 3, 12}, {"1M", 4, 13}} {
		kind, sub, ok := futuKLTypes(tc.resolution)
		if !ok || kind != tc.kind || sub != tc.sub || resolutionForKLType(kind) != tc.resolution {
			t.Fatal(tc)
		}
		r := httptest.NewRecorder()
		m.getFutuKline(r, testRequest("GET", "/futu/kline?symbol=AAPL&resolution="+tc.resolution+"&session=regular&from=1768435200&to=1768608000", nil))
		requireStatus(t, r, 200)
		if !strings.Contains(r.Body.String(), `"close":100`) || fake.historyKLType != tc.kind {
			t.Fatal(r.Body.String())
		}
	}
	fake.staticTypes = map[string]int{"OPTION": 7, "SPY": 4}
	for _, symbol := range []string{"OPTION", "/ES", "EUR/USD"} {
		r := httptest.NewRecorder()
		m.getFutuSymbols(r, testRequest("GET", "/futu/symbols?query="+symbol, nil))
		if symbol == "OPTION" {
			requireStatus(t, r, 200)
			if !strings.Contains(r.Body.String(), `"items":[]`) {
				t.Fatal(r.Body.String())
			}
		} else {
			requireStatus(t, r, 400)
		}
	}
	r := httptest.NewRecorder()
	m.getFutuSymbols(r, testRequest("GET", "/futu/symbols?query=SPY", nil))
	requireStatus(t, r, 200)
	if !strings.Contains(r.Body.String(), `"type":"fund"`) {
		t.Fatal(r.Body.String())
	}
}

func TestFutuMainQuotesNeedNoSchwabAndKeepUnknownFields(t *testing.T) {
	m, fake := mainFutuFixture(t)
	now := time.Date(2026, 1, 15, 10, 0, 0, 0, nyZone)
	m.now = func() time.Time { return now }
	fake.basicQotList = []map[string]any{{"security": map[string]any{"code": "AAPL", "market": marketUS}, "curPrice": 102, "lastClosePrice": 100, "updateTimestamp": now.Unix()}}
	r := httptest.NewRecorder()
	m.getFutuQuotes(r, testRequest("GET", "/futu/quotes?symbols=AAPL", nil))
	requireStatus(t, r, 200)
	var data struct{ Items []futuQuote }
	if err := json.Unmarshal(r.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Items) != 1 || data.Items[0].Price != 102 || data.Items[0].Time == nil || *data.Items[0].Time != now.UnixMilli() {
		t.Fatal(data)
	}
	row := map[string]any{"security": map[string]any{"code": "AAPL"}, "curPrice": 100, "overnight": map[string]any{"price": 101}}
	q, ok := parseMainFutuQuote(row, time.Date(2026, 1, 15, 21, 0, 0, 0, nyZone), true)
	if !ok || q.Price != 101 || q.Session != "night" || q.Time != nil || q.Chp != nil {
		t.Fatal(q)
	}
	q, ok = parseMainFutuQuote(row, time.Date(2026, 1, 15, 21, 0, 0, 0, nyZone), false)
	if !ok || q.Price != 100 || q.Session != "regular" {
		t.Fatal(q)
	}
}

func TestFutuMainStreamWorksWithNightDisabledAndFiltersNightTicks(t *testing.T) {
	m, fake := mainFutuFixture(t)
	fake.basicQotList = []map[string]any{{"security": map[string]any{"market": marketUS, "code": "AAPL"}, "curPrice": 100}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.serveFutuStream(w, r.WithContext(testContext(r.Context())))
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?mode=main", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var ready struct {
		Stream struct{ Owner, Generation string }
	}
	if err := conn.ReadJSON(&ready); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"command": "ADD", "symbol": "AAPL", "resolution": "5", "owner": ready.Stream.Owner, "generation": ready.Stream.Generation})
	r := httptest.NewRecorder()
	m.futuCommand(r, testRequest("POST", "/futu/quote/ws/command", strings.NewReader(string(body))))
	requireStatus(t, r, 200)
	night := time.Date(2026, 1, 15, 21, 0, 0, 0, nyZone).UnixMilli()
	regular := time.Date(2026, 1, 15, 10, 0, 0, 0, nyZone).UnixMilli()
	m.futu.broadcastKL("AAPL", "5", futuBar{Time: night, Close: 99})
	m.futu.broadcastKL("AAPL", "5", futuBar{Time: regular, Close: 100})
	var tick struct{ Data []struct{ Bar futuBar } }
	if err := conn.ReadJSON(&tick); err != nil {
		t.Fatal(err)
	}
	if len(tick.Data) != 1 || tick.Data[0].Bar.Close != 100 {
		t.Fatal("night-disabled main stream leaked night bar", tick)
	}
	nightResponse := httptest.NewRecorder()
	m.serveFutuStream(nightResponse, testRequest("GET", "/futu/quote/ws", nil))
	requireStatus(t, nightResponse, 409)
}

func TestFutuMainReportsLoggedOutOpenD(t *testing.T) {
	m, fake := mainFutuFixture(t)
	fake.loggedOut = true
	r := httptest.NewRecorder()
	m.getFutuQuotes(r, testRequest("GET", "/futu/quotes?symbols=AAPL", nil))
	requireStatus(t, r, 503)
	if !strings.Contains(r.Body.String(), "尚未登录") {
		t.Fatal(r.Body.String())
	}
}
