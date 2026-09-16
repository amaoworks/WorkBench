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
