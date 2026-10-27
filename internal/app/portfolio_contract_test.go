package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type portfolioContractTransport struct{ fallback http.RoundTripper }

func (tr portfolioContractTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "api.schwabapi.com" {
		return tr.fallback.RoundTrip(r)
	}
	var body string
	switch r.URL.Path {
	case "/trader/v1/accounts/accountNumbers":
		body = `[{"accountNumber":"123456789","hashValue":"contract-account"}]`
	case "/trader/v1/accounts":
		body = `[{"securitiesAccount":{"accountNumber":"123456789","type":"MARGIN","currentBalances":{"liquidationValue":1200,"cashBalance":100},"positions":[{"longQuantity":1,"shortQuantity":0,"averagePrice":1,"marketValue":200,"currentDayProfitLoss":20,"currentDayProfitLossPercentage":10,"longOpenProfitLoss":100,"instrument":{"symbol":"AAPL 261120C00200000","assetType":"OPTION","description":"Apple call"}},{"longQuantity":1,"instrument":{"symbol":"MISSING","assetType":"EQUITY"}}]}}]`
	case "/marketdata/v1/quotes":
		body = `{"AAPL 261120C00200000":{"quote":{"lastPrice":2}}}`
	case "/trader/v1/orders":
		body = `[{"orderId":10,"accountNumber":"123456789","orderType":"NET_DEBIT","status":"FILLED","duration":"DAY","quantity":1,"filledQuantity":1,"price":1,"enteredTime":"2026-10-26T15:00:00Z","closeTime":"2026-10-26T15:00:01Z","orderLegCollection":[{"legId":1,"instruction":"BUY_TO_OPEN","quantity":1,"instrument":{"symbol":"AAPL CALL 1"}},{"legId":2,"instruction":"SELL_TO_OPEN","quantity":1,"instrument":{"symbol":"AAPL CALL 2"}}],"orderActivityCollection":[{"activityType":"EXECUTION","executionLegs":[{"legId":1,"quantity":1,"price":2},{"legId":2,"quantity":1,"price":1}]}]}]`
	default:
		return &http.Response{StatusCode: 404, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func capturePortfolioContracts(t *testing.T, a *App, capture func(string, string, string, string, string, int) json.RawMessage) {
	t.Helper()
	original := http.DefaultTransport
	http.DefaultTransport = portfolioContractTransport{fallback: original}
	defer func() { http.DefaultTransport = original }()
	now := time.Now()
	if _, err := a.DB().Exec(`UPDATE investment_schwab SET access_token='contract-access',refresh_token='contract-refresh',token_expires_at=?,authorized_at=? WHERE id=1`,
		now.Add(time.Hour).UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := a.DB().Exec(`UPDATE investment_schwab SET access_token='',refresh_token='',token_expires_at=0,authorized_at=0 WHERE id=1`); err != nil {
			t.Error(err)
		}
	}()
	positions := capture("positions.ready", "investmentPositions", "GET", "/api/modules/investment/positions", "", 200)
	if strings.Contains(string(positions), "123456789") || !strings.Contains(string(positions), `"lastPrice":2`) || !strings.Contains(string(positions), `"lastPrice":null`) {
		t.Fatalf("positions contract lost privacy or missing-value semantics: %s", positions)
	}
	orders := capture("orders.ready", "investmentOrders", "GET", "/api/modules/investment/orders?days=90&status=filled&tz=UTC", "", 200)
	if !strings.Contains(string(orders), `"averageFill":1`) || strings.Contains(string(orders), "123456789") {
		t.Fatalf("orders contract lost net-fill or privacy: %s", orders)
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	day := now.In(location).Format("2006-01-02")
	if _, err := a.DB().Exec(`INSERT INTO investment_networth_account_scopes(authorized_at,account_hash) VALUES(?,'contract-account')`, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB().Exec(`INSERT INTO investment_networth_snapshots(account_hash,day,net_worth,cash,invested,day_pnl,captured_at) VALUES('contract-account',?,1200,100,1100,20,?)`, day, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	capture("networth.ready", "investmentNetworth", "GET", "/api/modules/investment/networth?range=1m", "", 200)
}
