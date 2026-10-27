package investment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func portfolioModule(t *testing.T, upstream http.HandlerFunc) *Module {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	m := openInvestmentModule(t)
	m.schwabAPI = server.URL
	m.now = func() time.Time { return time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC) }
	seedSchwab(t, m, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1/oauth/schwab",
		AccessToken: "access", RefreshToken: "refresh", TokenExpiresAt: m.now().Add(time.Hour), AuthorizedAt: m.now()})
	return m
}

func TestPositionsUseQuotesAndPreserveMissingValues(t *testing.T) {
	m := portfolioModule(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access" {
			t.Error("missing broker authorization")
		}
		switch r.URL.Path {
		case "/trader/v1/accounts/accountNumbers":
			io.WriteString(w, `[{"accountNumber":"123456789","hashValue":"safe-hash"}]`)
		case "/trader/v1/accounts":
			io.WriteString(w, `[{"securitiesAccount":{"accountNumber":"123456789","type":"MARGIN","currentBalances":{"cashBalance":0,"totalCash":500,"liquidationValue":1000},"positions":[
			{"longQuantity":1,"shortQuantity":0,"marketValue":200,"averagePrice":1,"longOpenProfitLoss":100,"currentDayProfitLoss":20,"instrument":{"symbol":"AAPL 261120C00200000","assetType":"OPTION"}},
			{"longQuantity":2,"instrument":{"symbol":"MISSING","assetType":"EQUITY"}}
			]}}]`)
		case "/marketdata/v1/quotes":
			io.WriteString(w, `{"AAPL 261120C00200000":{"quote":{"lastPrice":2}}}`)
		default:
			t.Errorf("unexpected %s", r.URL)
			w.WriteHeader(404)
		}
	})
	w := httptest.NewRecorder()
	m.getPositions(w, httptest.NewRequest("GET", "/", nil))
	var got portfolioResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" || len(got.Items) != 2 || got.UpdatedAt == nil {
		t.Fatalf("positions: %s", w.Body)
	}
	if got.Items[0].LastPrice == nil || *got.Items[0].LastPrice != 2 || *got.Items[0].MarketValue != 200 {
		t.Fatalf("option unit price incorrect: %+v", got.Items[0])
	}
	if got.Items[1].MarketValue != nil || got.Items[1].LastPrice != nil || got.Items[1].UnrealizedPnl != nil || got.Accounts[0].Invested != nil {
		t.Fatalf("missing fields became real zeros: %s", w.Body)
	}
	if got.Accounts[0].Cash == nil || *got.Accounts[0].Cash != 0 {
		t.Fatal("real zero cash was replaced by totalCash")
	}
	if strings.Contains(w.Body.String(), "123456789") || got.Items[0].AccountHash != "safe-hash" {
		t.Fatalf("raw account exposed: %s", w.Body)
	}
}

func TestOrderAverageFillUsesDirectionsRatiosAndMatchedExecutions(t *testing.T) {
	var order traderOrder
	json.Unmarshal([]byte(`{"quantity":2,"orderLegCollection":[
		{"legId":1,"quantity":2,"instruction":"BUY_TO_OPEN"},
		{"legId":2,"quantity":4,"instruction":"SELL_TO_OPEN"}],
		"orderActivityCollection":[{"activityType":"EXECUTION","executionLegs":[
		{"legId":1,"price":5,"quantity":1},{"legId":2,"price":1,"quantity":2},
		{"legId":1,"price":7,"quantity":1},{"legId":2,"price":2,"quantity":2}]}]}`), &order)
	if got := orderAverageFill(order); got == nil || *got != 3 {
		t.Fatalf("ratio spread net price = %v, want 3 debit", got)
	}
	order.Legs[0].Instruction, order.Legs[1].Instruction = "SELL_TO_OPEN", "BUY_TO_OPEN"
	if got := orderAverageFill(order); got == nil || *got != -3 {
		t.Fatalf("credit spread net price = %v, want -3", got)
	}
	order.Activities[0].Executions = order.Activities[0].Executions[:3]
	if got := orderAverageFill(order); got != nil {
		t.Fatal("unmatched partial legs must not fabricate a strategy price")
	}
	order.Activities[0].Executions[0].LegID = nil
	if got := orderAverageFill(order); got != nil {
		t.Fatal("unknown execution leg must not fabricate a strategy price")
	}
	json.Unmarshal([]byte(`{"orderLegCollection":[{"instruction":"BUY"}],"orderActivityCollection":[{"executionLegs":[{"price":10,"quantity":1},{"price":13,"quantity":2}]}]}`), &order)
	if got := orderAverageFill(order); got == nil || *got != 12 {
		t.Fatalf("single leg average = %v, want 12", got)
	}
}

func TestOrdersSplitCappedWindowsBeforeFilteringAndDeduplicateChildren(t *testing.T) {
	calls := 0
	m := portfolioModule(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/trader/v1/accounts/accountNumbers":
			io.WriteString(w, `[{"accountNumber":123456789,"hashValue":"safe-hash"}]`)
		case "/trader/v1/orders":
			calls++
			if r.URL.Query().Get("maxResults") != "500" {
				t.Error("missing result bound")
			}
			if calls == 1 {
				orders := make([]map[string]any, portfolioOrderLimit)
				for i := range orders {
					orders[i] = map[string]any{"orderId": i}
				}
				json.NewEncoder(w).Encode(orders)
			} else if calls == 2 {
				io.WriteString(w, `[{"accountNumber":123456789,"orderId":1,"status":"FILLED","orderLegCollection":[{"instrument":{"symbol":"AAPL"}}]},
				{"accountNumber":123456789,"childOrderStrategies":[{"orderId":2,"status":"WORKING","enteredTime":"2026-11-01T12:00:00-05:00","orderLegCollection":[{"instrument":{"symbol":"MSFT"}}]}]}]`)
			} else {
				io.WriteString(w, `[{"accountNumber":123456789,"orderId":2,"status":"WORKING","enteredTime":"2026-11-01T17:00:00Z","orderLegCollection":[{"instrument":{"symbol":"MSFT"}}]}]`)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	w := httptest.NewRecorder()
	m.getOrders(w, httptest.NewRequest("GET", "/?days=7&status=working&tz=America%2FNew_York", nil))
	var got ordersResponse
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.Status != "ready" || len(got.Items) != 1 || got.Items[0].ID != "2" || calls != 3 {
		t.Fatalf("incomplete or duplicate orders: %s; calls=%d", w.Body, calls)
	}
	if strings.Contains(w.Body.String(), "123456789") {
		t.Fatalf("raw account exposed: %s", w.Body)
	}
}

func TestOrdersFailRatherThanReturnAnUnsplittableCappedWindow(t *testing.T) {
	m := portfolioModule(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "["+strings.TrimSuffix(strings.Repeat(`{"orderId":1},`, portfolioOrderLimit), ",")+"]")
	})
	_, err := m.readPortfolioOrders(context.Background(), "access", m.now(), m.now())
	if err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("capped response reported complete: %v", err)
	}
}

func TestOrdersUseCalendarRangeAcrossDSTAndValidateFilters(t *testing.T) {
	m := portfolioModule(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "accountNumbers") {
			io.WriteString(w, `[]`)
			return
		}
		if got := r.URL.Query().Get("fromEnteredTime"); got != "2026-10-31T04:00:00Z" {
			t.Errorf("calendar range start = %s", got)
		}
		io.WriteString(w, `[]`)
	})
	m.now = func() time.Time { return time.Date(2026, 11, 6, 18, 0, 0, 0, time.UTC) }
	seedSchwab(t, m, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1/oauth/schwab",
		AccessToken: "access", RefreshToken: "refresh", TokenExpiresAt: m.now().Add(time.Hour), AuthorizedAt: m.now()})
	w := httptest.NewRecorder()
	m.getOrders(w, httptest.NewRequest("GET", "/?days=7&tz=America%2FNew_York", nil))
	if !strings.Contains(w.Body.String(), `"status":"ready"`) {
		t.Fatalf("orders: %s", w.Body)
	}
	for _, query := range []string{"days=2", "status=invalid", "tz=unknown"} {
		w = httptest.NewRecorder()
		m.getOrders(w, httptest.NewRequest("GET", "/?"+query, nil))
		if w.Code != 400 {
			t.Errorf("accepted %s: %d", query, w.Code)
		}
	}
}

func TestPortfolioDisconnectedAndReauthorizationStates(t *testing.T) {
	m := openInvestmentModule(t)
	for _, method := range []http.HandlerFunc{m.getPositions, m.getOrders} {
		w := httptest.NewRecorder()
		method(w, httptest.NewRequest("GET", "/", nil))
		if !strings.Contains(w.Body.String(), `"status":"unconfigured"`) {
			t.Fatalf("unconfigured: %s", w.Body)
		}
	}
	seedSchwab(t, m, schwabRecord{AppKey: "key", AppSecret: "secret", ReauthorizationRequired: true})
	w := httptest.NewRecorder()
	m.getPositions(w, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(w.Body.String(), `"status":"reauth"`) {
		t.Fatal(fmt.Sprintf("reauthorization: %s", w.Body))
	}
}
