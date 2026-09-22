package app

import (
	"bytes"
	"context"
	"testing"
)

func TestInvestmentWatchlistsRequireCSRFAndSurviveRestart(t *testing.T) {
	a := newTestApp(t)
	const route = "/api/modules/investment/watchlists"
	body := []byte(`{"revision":0,"writer":"test","sequence":1,"state":{"lists":[{"id":"main","title":"自选","symbols":["MSFT","AAPL"]}],"activeId":"main"}}`)
	if got := request(t, a.Handler(), "PUT", route, body, "", nil); got.Code != 400 {
		t.Fatalf("missing CSRF: %d", got.Code)
	}
	token, cookies := csrf(t, a.Handler())
	if got := request(t, a.Handler(), "PUT", route, body, token, cookies); got.Code != 200 {
		t.Fatalf("save: %d %s", got.Code, got.Body)
	}
	before := request(t, a.Handler(), "GET", route, nil, "", cookies)
	if before.Code != 200 || !bytes.Contains(before.Body.Bytes(), []byte(`"revision":1`)) {
		t.Fatalf("read: %d %s", before.Code, before.Body)
	}
	if got := request(t, a.Handler(), "PUT", "/api/modules/investment/enabled", []byte(`{"enabled":false}`), token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if got := request(t, a.Handler(), "GET", route, nil, "", cookies); got.Code != 503 {
		t.Fatalf("module gate: %d", got.Code)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), a.config, a.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	token, cookies = csrf(t, restarted.Handler())
	if got := request(t, restarted.Handler(), "PUT", "/api/modules/investment/enabled", []byte(`{"enabled":true}`), token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	after := request(t, restarted.Handler(), "GET", route, nil, "", cookies)
	if after.Code != 200 || !bytes.Equal(after.Body.Bytes(), before.Body.Bytes()) {
		t.Fatalf("state lost after restart: %d %s", after.Code, after.Body)
	}
}
