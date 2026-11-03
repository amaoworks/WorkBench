package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func issueBrowserGrant(t *testing.T, h http.Handler, access string) string {
	t.Helper()
	w := nativeRequest(h, "POST", "/api/auth/browser-grant", map[string]any{}, access)
	var result struct {
		URLPath   string    `json:"urlPath"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.URLPath == "" {
		t.Fatalf("grant: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("browser grant can be cached")
	}
	return result.URLPath
}

func TestBrowserGrantEstablishesCookieAndTracksParentDevice(t *testing.T) {
	_, h := tokenService(t, false)
	device := nativeLogin(t, h)
	target := issueBrowserGrant(t, h, device.AccessToken)
	w := nativeRequest(h, "GET", target, nil, "")
	if w.Code != 303 || w.Header().Get("Location") != "/investment/terminal" {
		t.Fatalf("consume: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("ticket response can leak via cache/referrer")
	}
	cookies := w.Result().Cookies()
	browserGet := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := browserGet("/protected"); w.Code != 204 {
		t.Fatalf("WebView authentication: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "GET", target, nil, ""); w.Code != 401 {
		t.Fatal("ticket replay succeeded")
	}
	if w := nativeRequest(h, "GET", target, nil, device.AccessToken); w.Code != 400 {
		t.Fatal("ticket consume accepted Bearer")
	}
	if w := nativeRequest(h, "POST", "/api/auth/token/revoke", nil, device.AccessToken); w.Code != 200 {
		t.Fatal("device revoke failed")
	}
	if w := browserGet("/protected"); w.Code != 401 {
		t.Fatal("parent revoke left WebView cookie authenticated")
	}
}

func TestBrowserGrantRejectsExpiredOrRevokedParent(t *testing.T) {
	s, h := tokenService(t, false)
	clock := time.Now().UTC().Truncate(time.Second)
	s.now = func() time.Time { return clock }
	device := nativeLogin(t, h)
	target := issueBrowserGrant(t, h, device.AccessToken)
	clock = clock.Add(time.Minute)
	if w := nativeRequest(h, "GET", target, nil, ""); w.Code != 401 {
		t.Fatal("expired ticket accepted")
	}
	target = issueBrowserGrant(t, h, device.AccessToken)
	if w := nativeRequest(h, "POST", "/api/auth/token/revoke", nil, device.AccessToken); w.Code != 200 {
		t.Fatal("device revoke failed")
	}
	if w := nativeRequest(h, "GET", target, nil, ""); w.Code != 401 {
		t.Fatal("revoked parent ticket accepted")
	}
}

func TestBrowserGrantOnlyStoresTicketHash(t *testing.T) {
	s, h := tokenService(t, false)
	device := nativeLogin(t, h)
	target := issueBrowserGrant(t, h, device.AccessToken)
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	ticket := u.Query().Get("ticket")
	var stored string
	if err := s.db.QueryRow("SELECT token_hash FROM auth_browser_grants").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == ticket || stored != tokenHash(ticket) {
		t.Fatal("ticket was not hashed")
	}
	if w := nativeRequest(h, "GET", target+"&next=https://evil.invalid", nil, ""); w.Code != 303 || w.Header().Get("Location") != "/investment/terminal" {
		t.Fatal("ticket redirected to untrusted input")
	}
}
