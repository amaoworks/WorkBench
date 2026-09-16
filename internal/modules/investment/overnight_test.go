package investment

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOvernightRequiresProviderAndStaysOffAfterReenable(t *testing.T) {
	m := openInvestmentModule(t)
	saveNight := func(enabled bool) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]bool{"enabled": enabled})
		r := httptest.NewRecorder()
		m.saveOvernight(r, httptest.NewRequest("PUT", "/", strings.NewReader(string(body))))
		return r
	}
	if got := saveNight(true); got.Code != 409 {
		t.Fatalf("night without Futu: %d %s", got.Code, got.Body.String())
	}
	fake := startFakeOpenD(t)
	m.deps.FutuOpenDAddress = fake.addr()
	providerBody := `{"enabled":true}`
	saveProvider := func(body string) {
		t.Helper()
		r := httptest.NewRecorder()
		m.saveFutu(r, httptest.NewRequest("PUT", "/", strings.NewReader(body)))
		if r.Code != 200 {
			t.Fatal(r.Body.String())
		}
	}
	saveProvider(providerBody)
	if _, err := m.futu.ensureOverlay(context.Background()); err != errOverlayOff {
		t.Fatalf("Futu alone enabled night: %v", err)
	}
	if got := saveNight(true); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if _, err := m.futu.ensureOverlay(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Both explicit provider disable and disconnect must clear the business choice.
	for _, disconnect := range []bool{false, true} {
		if disconnect {
			r := httptest.NewRecorder()
			m.disconnectFutu(r, httptest.NewRequest("POST", "/", nil))
			if r.Code != 200 {
				t.Fatal(r.Body.String())
			}
		} else {
			saveProvider(strings.Replace(providerBody, `"enabled":true`, `"enabled":false`, 1))
		}
		rec, err := m.loadFutu(context.Background())
		if err != nil || rec.Enabled || rec.OvernightEnabled {
			t.Fatalf("disable: %+v %v", rec, err)
		}
		saveProvider(providerBody)
		rec, _ = m.loadFutu(context.Background())
		if rec.OvernightEnabled {
			t.Fatal("provider reenable silently enabled night")
		}
		if got := saveNight(true); got.Code != 200 {
			t.Fatal(got.Body.String())
		}
	}
	if got := saveNight(false); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	rec, _ := m.loadFutu(context.Background())
	if !rec.Enabled || rec.OvernightEnabled {
		t.Fatal("night disable changed provider")
	}
	ws := httptest.NewRecorder()
	m.serveFutuStream(ws, httptest.NewRequest("GET", "/", nil))
	if ws.Code != 409 {
		t.Fatal("night websocket bypassed dependency")
	}
}
