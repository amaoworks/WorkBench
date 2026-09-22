package investment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"workbench/internal/capabilities/notifications"
	"workbench/internal/contracts"
	"workbench/internal/foundation/events"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

type monitorTransport func(*http.Request) (*http.Response, error)

func (f monitorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func monitorResponse(value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}
}

func setupPriceMonitor(t *testing.T) (*Module, *time.Time) {
	t.Helper()
	m := openInvestmentModule(t)
	now := time.Date(2026, 9, 22, 14, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	service, err := notifications.NewService(m.deps.DB, events.NewStore(m.deps.DB))
	if err != nil {
		t.Fatal(err)
	}
	m.deps.Notifications = service
	seedSchwab(t, m, schwabRecord{AccessToken: "monitor-token", RefreshToken: "monitor-refresh", TokenExpiresAt: now.Add(24 * time.Hour)})
	return m, &now
}

func addPriceRule(t *testing.T, m *Module, id, symbol, direction string, bps int64) {
	t.Helper()
	if err := m.queries.CreatePriceRule(context.Background(), investmentsqlc.CreatePriceRuleParams{ID: id, Symbol: symbol, Direction: direction, ThresholdBps: bps, Enabled: 1, CreatedAt: m.now().UnixMilli(), UpdatedAt: m.now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
}

func priceFixture(symbol string, price float64, now time.Time) monitoredQuote {
	var q monitoredQuote
	q.Symbol, q.AssetMainType, q.Realtime = symbol, "EQUITY", true
	q.Regular.Price, q.Regular.Time, q.Quote.ClosePrice = price, now.UnixMilli(), 100
	return q
}

func marketFixture(m *Module, now time.Time, open bool) any {
	local := now.In(m.marketLocation)
	start := time.Date(local.Year(), local.Month(), local.Day(), 9, 30, 0, 0, m.marketLocation)
	return map[string]any{"equity": map[string]any{"EQ": map[string]any{"date": local.Format("2006-01-02"), "isOpen": open,
		"sessionHours": map[string]any{"regularMarket": []marketSession{{Start: start, End: start.Add(390 * time.Minute)}}}}}}
}

func installMonitorFeed(t *testing.T, m *Module, now *time.Time, quote func(string) monitoredQuote) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	m.httpClient = &http.Client{Transport: monitorTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer monitor-token" {
			t.Error("missing quote authorization")
		}
		if strings.Contains(r.URL.Path, "/markets/") {
			return monitorResponse(marketFixture(m, *now, true)), nil
		}
		if r.URL.Path != "/marketdata/v1/quotes" {
			return nil, errors.New("unexpected market path")
		}
		calls.Add(1)
		batch := make(map[string]monitoredQuote)
		for _, symbol := range strings.Split(r.URL.Query().Get("symbols"), ",") {
			batch[symbol] = quote(symbol)
		}
		return monitorResponse(batch), nil
	})}
	return &calls
}

func countTriggers(t *testing.T, m *Module, expected int) {
	t.Helper()
	for _, table := range []string{"investment_price_triggers", "notifications"} {
		var count int
		if err := m.deps.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != expected {
			t.Fatalf("%s count=%d want=%d error=%v", table, count, expected, err)
		}
	}
}

func TestPriceMonitorBackgroundBatchDailyDedupAndRestart(t *testing.T) {
	m, now := setupPriceMonitor(t)
	addPriceRule(t, m, "up", "AAPL", "up", 300)
	addPriceRule(t, m, "higher", "AAPL", "up", 500)
	addPriceRule(t, m, "down", "MSFT", "down", 500)
	calls := installMonitorFeed(t, m, now, func(symbol string) monitoredQuote {
		if symbol == "MSFT" {
			return priceFixture(symbol, 95, *now)
		}
		return priceFixture(symbol, 103, *now)
	})
	for i := 0; i < 2; i++ {
		if err := m.scanPrices(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	countTriggers(t, m, 2)
	if calls.Load() != 2 || len(m.streamer.clients) != 0 {
		t.Fatal("monitor must batch shared symbols and work with no browser connections")
	}
	// New module instance using the same persisted workspace, with no in-memory
	// trigger state. Existing day's reminders must remain deduplicated.
	restarted, err := New(m.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.now, restarted.httpClient = m.now, m.httpClient
	if err := restarted.scanPrices(context.Background()); err != nil {
		t.Fatal(err)
	}
	countTriggers(t, m, 2)
	*now = now.Add(24 * time.Hour)
	seedSchwab(t, restarted, schwabRecord{AccessToken: "monitor-token", RefreshToken: "monitor-refresh", TokenExpiresAt: now.Add(time.Hour)})
	if err := restarted.scanPrices(context.Background()); err != nil {
		t.Fatal(err)
	}
	countTriggers(t, m, 4)
}

func TestPriceMonitorRejectsInvalidQuotes(t *testing.T) {
	for _, kind := range []string{"stale", "future", "prior-session", "zero-close", "delayed", "option", "missing"} {
		t.Run(kind, func(t *testing.T) {
			m, now := setupPriceMonitor(t)
			addPriceRule(t, m, "rule", "AAPL", "up", 100)
			installMonitorFeed(t, m, now, func(symbol string) monitoredQuote {
				q := priceFixture(symbol, 120, *now)
				switch kind {
				case "stale":
					q.Regular.Time = now.Add(-4 * time.Minute).UnixMilli()
				case "future":
					q.Regular.Time = now.Add(time.Minute).UnixMilli()
				case "prior-session":
					q.Regular.Time = now.Add(-24 * time.Hour).UnixMilli()
				case "zero-close":
					q.Quote.ClosePrice = 0
				case "delayed":
					q.Realtime = false
				case "option":
					q.AssetMainType = "OPTION"
				case "missing":
					q = monitoredQuote{}
				}
				return q
			})
			if err := m.scanPrices(context.Background()); err != nil {
				t.Fatal(err)
			}
			countTriggers(t, m, 0)
			rule, _ := m.queries.GetPriceRule(context.Background(), "rule")
			if rule.LastError == "" || rule.Price.Valid {
				t.Fatal("invalid quote was displayed as current", rule)
			}
		})
	}
}

func TestPriceMonitorCalendarAndNoRulesAvoidQuotes(t *testing.T) {
	for _, state := range []string{"no-rules", "holiday", "early-close", "malformed-calendar"} {
		t.Run(state, func(t *testing.T) {
			m, now := setupPriceMonitor(t)
			if state != "no-rules" {
				addPriceRule(t, m, "rule", "AAPL", "up", 100)
			}
			m.httpClient = &http.Client{Transport: monitorTransport(func(r *http.Request) (*http.Response, error) {
				if state == "no-rules" || strings.HasSuffix(r.URL.Path, "/quotes") {
					t.Error("unnecessary upstream request", r.URL.Path)
				}
				if state == "malformed-calendar" {
					return monitorResponse(map[string]any{}), nil
				}
				if state == "early-close" {
					return monitorResponse(map[string]any{"equity": map[string]any{"EQ": map[string]any{"date": now.In(m.marketLocation).Format("2006-01-02"), "isOpen": true, "sessionHours": map[string]any{"regularMarket": []marketSession{{Start: now.Add(-time.Hour), End: now.Add(-time.Minute)}}}}}}), nil
				}
				return monitorResponse(marketFixture(m, *now, false)), nil
			})}
			err := m.scanPrices(context.Background())
			if (err != nil) != (state == "malformed-calendar") {
				t.Fatal(err)
			}
			countTriggers(t, m, 0)
		})
	}
}

type failedNotice struct{ contracts.NotificationService }

func (failedNotice) CreateTx(context.Context, *sql.Tx, contracts.NewNotification) (contracts.Notification, error) {
	return contracts.Notification{}, errors.New("notification fixture failure")
}

func TestPriceMonitorNotificationFailureRollsBackDailyTrigger(t *testing.T) {
	m, now := setupPriceMonitor(t)
	addPriceRule(t, m, "rule", "AAPL", "up", 300)
	installMonitorFeed(t, m, now, func(symbol string) monitoredQuote { return priceFixture(symbol, 103, *now) })
	actual := m.deps.Notifications
	m.deps.Notifications = failedNotice{actual}
	if err := m.scanPrices(context.Background()); err == nil {
		t.Fatal("missing notification failure")
	}
	countTriggers(t, m, 0)
	m.deps.Notifications = actual
	if err := m.scanPrices(context.Background()); err != nil {
		t.Fatal(err)
	}
	countTriggers(t, m, 1)
}

func TestPriceMonitorIgnoresInFlightResultAfterChanges(t *testing.T) {
	for _, change := range []string{"disabled", "edited", "deleted", "reauthorized"} {
		t.Run(change, func(t *testing.T) {
			m, now := setupPriceMonitor(t)
			addPriceRule(t, m, "rule", "AAPL", "up", 300)
			started, release := make(chan struct{}), make(chan struct{})
			installMonitorFeed(t, m, now, func(symbol string) monitoredQuote { close(started); <-release; return priceFixture(symbol, 105, *now) })
			done := make(chan error, 1)
			go func() { done <- m.scanPrices(context.Background()) }()
			<-started
			switch change {
			case "disabled":
				if err := m.OnEnabledChanged(context.Background(), false); err != nil {
					t.Fatal(err)
				}
			case "edited":
				_, _ = m.deps.DB.Exec("UPDATE investment_price_rules SET threshold_bps=1000,version=version+1 WHERE id='rule'")
			case "deleted":
				_, _ = m.queries.DeletePriceRule(context.Background(), "rule")
			case "reauthorized":
				seedSchwab(t, m, schwabRecord{AccessToken: "new-token", TokenExpiresAt: now.Add(time.Hour)})
			}
			close(release)
			err := <-done
			if err != nil && change != "reauthorized" {
				t.Fatal(err)
			}
			countTriggers(t, m, 0)
		})
	}
}

func TestPriceRulesHTTPValidationAndStableHistory(t *testing.T) {
	m, now := setupPriceMonitor(t)
	router := chi.NewRouter()
	router.Post("/rules", m.savePriceRule)
	router.Put("/rules/{id}", m.savePriceRule)
	router.Delete("/rules/{id}", m.deletePriceRule)
	for _, input := range []string{`{"symbol":"/ES","direction":"up","thresholdPercent":3,"enabled":true}`, `{"symbol":"AAPL","direction":"up","thresholdPercent":0,"enabled":true}`, `{"symbol":"AAPL","direction":"down","thresholdPercent":101,"enabled":true}`, `{"symbol":"AAPL","direction":"up","thresholdPercent":1.001,"enabled":true}`, `{"symbol":"AAPL","direction":"up","thresholdPercent":1}`} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", "/rules", strings.NewReader(input)))
		if w.Code != 400 {
			t.Fatalf("invalid rule accepted: %d %s", w.Code, w.Body.String())
		}
	}
	created := httptest.NewRecorder()
	router.ServeHTTP(created, httptest.NewRequest("POST", "/rules", strings.NewReader(`{"symbol":" aapl ","direction":"up","thresholdPercent":3,"enabled":true}`)))
	var rule priceRuleView
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &rule) != nil || rule.Symbol != "AAPL" {
		t.Fatal(created.Body.String())
	}
	for i := 0; i < 51; i++ {
		_, err := m.queries.CreatePriceTrigger(context.Background(), investmentsqlc.CreatePriceTriggerParams{ID: fmt.Sprintf("id-%03d", i), RuleID: fmt.Sprintf("rule-%d", i), TradingDate: "2026-09-22", Symbol: "AAPL", Direction: "up", ThresholdBps: 300, Price: 103, PreviousClose: 100, ChangePercent: 3, QuoteAt: now.UnixMilli(), TriggeredAt: now.UnixMilli(), NotificationID: "notice"})
		if err != nil {
			t.Fatal(err)
		}
	}
	page := func(cursor string) ([]priceTriggerView, string) {
		w := httptest.NewRecorder()
		m.getPriceHistory(w, httptest.NewRequest("GET", "/history?cursor="+cursor, nil))
		var result struct {
			Items []priceTriggerView `json:"items"`
			Next  string             `json:"nextCursor"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal(w.Body.String())
		}
		return result.Items, result.Next
	}
	first, cursor := page("")
	second, end := page(cursor)
	if len(first) != 50 || len(second) != 1 || cursor == "" || end != "" || first[0].ID != "id-050" || second[0].ID != "id-000" {
		t.Fatal("history pagination lost or duplicated records")
	}
	deleted := httptest.NewRecorder()
	router.ServeHTTP(deleted, httptest.NewRequest("DELETE", "/rules/"+rule.ID, nil))
	if deleted.Code != 200 {
		t.Fatal(deleted.Body.String())
	}
}
