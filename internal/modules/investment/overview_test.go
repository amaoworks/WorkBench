package investment

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestMarketPhaseUsesNewYorkSessions(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	for _, tc := range []struct {
		at   string
		want string
	}{
		{"2026-10-26T08:00:00", "pre"}, {"2026-10-26T10:00:00", "regular"}, {"2026-10-26T17:00:00", "post"},
		{"2026-10-26T21:00:00", "overnight"}, {"2026-10-27T02:00:00", "overnight"}, {"2026-10-30T21:00:00", "closed"},
		{"2026-10-31T12:00:00", "closed"}, {"2026-11-01T12:00:00", "closed"}, {"2026-11-01T20:30:00", "overnight"},
	} {
		at, _ := time.ParseInLocation("2006-01-02T15:04:05", tc.at, ny)
		if got := marketPhase(at, ny); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.at, got, tc.want)
		}
	}
}

func TestNormalizeOverviewSymbol(t *testing.T) {
	for raw, want := range map[string]string{"NASDAQ:aapl": "AAPL", " BRK.B ": "BRK.B", "###Tech": "", "bad symbol": "", "": ""} {
		if got := normalizeOverviewSymbol(raw); got != want {
			t.Errorf("%q: %q, want %q", raw, got, want)
		}
	}
}

func TestQuoteFromSchwabPicksSessionPrice(t *testing.T) {
	var entry schwabQuoteEntry
	if err := json.Unmarshal([]byte(`{"reference":{"description":"Apple"},"quote":{"lastPrice":201,"netPercentChange":1.5,"postMarketPercentChange":0.5},"regular":{"regularMarketLastPrice":200,"regularMarketPercentChange":1}}`), &entry); err != nil {
		t.Fatal(err)
	}
	regular, _ := quoteFromSchwab("AAPL", entry, "regular")
	if regular.Price != 200 || regular.ChangePct != 1 || regular.ExtChangePct != nil {
		t.Fatalf("regular: %+v", regular)
	}
	post, _ := quoteFromSchwab("AAPL", entry, "post")
	if post.Price != 201 || post.ChangePct != 1 || post.ExtChangePct == nil || *post.ExtChangePct != 0.5 {
		t.Fatalf("post: %+v", post)
	}
	if _, ok := quoteFromSchwab("AAPL", schwabQuoteEntry{}, "regular"); ok {
		t.Fatal("zero price must be skipped")
	}
}

func TestOverviewSnapshotAndQuotesUseCache(t *testing.T) {
	var accountCalls, quoteCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-token" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/trader/v1/accounts":
			accountCalls.Add(1)
			_, _ = io.WriteString(w, `[{"securitiesAccount":{"currentBalances":{"liquidationValue":110000,"cashBalance":10000,"buyingPower":20000},"positions":[
				{"marketValue":60000,"currentDayProfitLoss":1200,"instrument":{"assetType":"EQUITY","symbol":"AAPL","description":"Apple"}},
				{"marketValue":30000,"currentDayProfitLoss":-1500,"instrument":{"assetType":"EQUITY","symbol":"TSLA","description":"Tesla"}},
				{"marketValue":10000,"currentDayProfitLoss":100,"instrument":{"assetType":"EQUITY","symbol":"MSFT","description":"Microsoft"}},
				{"marketValue":500,"currentDayProfitLoss":200,"instrument":{"assetType":"OPTION","symbol":"AAPL 261120C00200000"}}]}}]`)
		case "/marketdata/v1/quotes":
			quoteCalls.Add(1)
			if r.URL.Query().Get("symbols") != "AAPL,TSLA,MSFT" {
				t.Errorf("symbols %q", r.URL.Query().Get("symbols"))
			}
			_, _ = io.WriteString(w, `{"AAPL":{"quote":{"lastPrice":200},"regular":{"regularMarketLastPrice":200,"regularMarketPercentChange":2}},
				"TSLA":{"quote":{"lastPrice":250},"regular":{"regularMarketLastPrice":250,"regularMarketPercentChange":-4.8}}}`)
		case "/marketdata/v1/pricehistory":
			if r.URL.Query().Get("symbol") == "AAPL" {
				_, _ = io.WriteString(w, `{"candles":[{"close":196},{"close":198},{"close":200}]}`)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	module := openInvestmentModule(t)
	module.schwabAPI = upstream.URL
	ny, _ := time.LoadLocation("America/New_York")
	module.marketLocation = ny
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC) // 11:00 New York, regular session
	module.now = func() time.Time { return now }
	seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1:8080/oauth/schwab",
		AccessToken: "access-token", RefreshToken: "refresh", TokenExpiresAt: now.Add(time.Hour), AuthorizedAt: now.Add(-24 * time.Hour)})
	snapshot := module.overviewSnapshot(context.Background())
	if snapshot.Status != "ok" || snapshot.NetWorth != 110000 || snapshot.Cash != 10000 || snapshot.BuyingPower != 20000 || snapshot.Positions != 3 {
		t.Fatalf("snapshot: %+v", snapshot)
	}
	if len(snapshot.Movers) != 3 || snapshot.Movers[0].Symbol != "TSLA" || snapshot.Movers[1].Symbol != "AAPL" {
		t.Fatalf("movers: %+v", snapshot.Movers)
	}
	if want := []string{"AAPL", "TSLA", "MSFT"}; len(snapshot.Symbols) != 3 || snapshot.Symbols[0] != want[0] || snapshot.Symbols[2] != want[2] {
		t.Fatalf("symbols: %v", snapshot.Symbols)
	}
	quotes := module.overviewQuotes(context.Background())
	if quotes.Status != "ok" || quotes.Session != "regular" || len(quotes.Items) != 2 || !quotes.Items[0].Held || len(quotes.Items[0].Spark) != 3 || quotes.Items[1].Spark != nil {
		t.Fatalf("quotes: %+v", quotes)
	}
	now = now.Add(20 * time.Second)
	module.overviewSnapshot(context.Background())
	module.overviewQuotes(context.Background())
	if accountCalls.Load() != 1 || quoteCalls.Load() != 1 {
		t.Fatalf("cache missed: accounts=%d quotes=%d", accountCalls.Load(), quoteCalls.Load())
	}
	now = now.Add(time.Minute)
	module.overviewSnapshot(context.Background())
	if accountCalls.Load() != 2 {
		t.Fatalf("expired cache not refreshed: %d", accountCalls.Load())
	}
	alerts, err := module.overviewAlerts(context.Background())
	if err != nil || alerts.Schwab.Status != "connected" || alerts.Schwab.RefreshExpiresAt == nil ||
		!alerts.Schwab.RefreshExpiresAt.Equal(now.Add(-80*time.Second).Add(-24*time.Hour).Add(7*24*time.Hour)) {
		t.Fatalf("alerts: %+v %v", alerts, err)
	}
}

func TestOverviewDisconnectedDoesNotCallSchwab(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected upstream call %s", r.URL.Path)
	}))
	defer upstream.Close()
	module := openInvestmentModule(t)
	module.schwabAPI = upstream.URL
	if got := module.overviewSnapshot(context.Background()); got.Status != "disconnected" {
		t.Fatalf("snapshot: %+v", got)
	}
	if got := module.overviewQuotes(context.Background()); got.Status != "disconnected" || len(got.Items) != 0 {
		t.Fatalf("quotes: %+v", got)
	}
	alerts, err := module.overviewAlerts(context.Background())
	if err != nil || alerts.Schwab.Status != "unconfigured" || alerts.Futu.Enabled {
		t.Fatalf("alerts: %+v %v", alerts, err)
	}
}
