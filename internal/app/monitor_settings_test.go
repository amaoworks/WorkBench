package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTelegramAndMonitorRoutesPersistAndRequireCSRF(t *testing.T) {
	a := newTestApp(t)
	token, cookies := csrf(t, a.Handler())
	input := []byte(`{"enabled":true,"botToken":"123:secret-fixture","chatId":"12345"}`)
	if got := request(t, a.Handler(), "PUT", "/api/settings/telegram", input, "", nil); got.Code != 400 {
		t.Fatal("Telegram settings bypassed CSRF", got.Code)
	}
	if got := request(t, a.Handler(), "POST", "/api/settings/telegram/test", input, "", nil); got.Code != 400 {
		t.Fatal("Telegram test bypassed CSRF", got.Code)
	}
	if got := request(t, a.Handler(), "PUT", "/api/settings/telegram", input, token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	got := request(t, a.Handler(), "GET", "/api/settings/telegram", nil, "", cookies)
	if got.Code != 200 || strings.Contains(got.Body.String(), "secret-fixture") || !strings.Contains(got.Body.String(), `"hasBotToken":true`) {
		t.Fatal("unsafe settings response", got.Body.String())
	}
	ruleInput := []byte(`{"symbol":"SPY","direction":"down","thresholdPercent":2,"enabled":true}`)
	if got := request(t, a.Handler(), "POST", "/api/modules/investment/monitor/rules", ruleInput, "", nil); got.Code != 400 {
		t.Fatal("monitor write bypassed CSRF")
	}
	created := request(t, a.Handler(), "POST", "/api/modules/investment/monitor/rules", ruleInput, token, cookies)
	var rule struct {
		ID string `json:"id"`
	}
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &rule) != nil {
		t.Fatal(created.Body.String())
	}
	cfg := a.config
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	token, cookies = csrf(t, restarted.Handler())
	got = request(t, restarted.Handler(), "GET", "/api/modules/investment/monitor", nil, "", cookies)
	if got.Code != 200 || !strings.Contains(got.Body.String(), rule.ID) {
		t.Fatal("rules not persisted", got.Body.String())
	}
	got = request(t, restarted.Handler(), "GET", "/api/settings/telegram", nil, "", cookies)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"enabled":true`) || strings.Contains(got.Body.String(), "secret-fixture") {
		t.Fatal("TG settings not persisted safely", got.Body.String())
	}
	if got := request(t, restarted.Handler(), "PUT", "/api/modules/investment/enabled", []byte(`{"enabled":false}`), token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	for _, route := range []string{"/api/modules/investment/monitor", "/api/modules/investment/monitor/history"} {
		if got := request(t, restarted.Handler(), "GET", route, nil, "", cookies); got.Code != 503 {
			t.Fatal("disabled module exposes monitoring", route, got.Code)
		}
	}
}
