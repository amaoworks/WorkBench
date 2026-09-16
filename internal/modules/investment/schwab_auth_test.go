package investment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestSchwabRefreshFailuresRequireAuthorizationOnlyForInvalidTokens(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		status      int
		reauthorize bool
	}{
		{"expired grant", `{"error":"invalid_grant"}`, 400, true},
		{"Schwab expired refresh token", `{"error":"invalid_client","error_description":"refresh token invalid"}`, 400, true},
		{"revoked refresh token", `{"error":"invalid_client","error_description":"Refresh_token revoked"}`, 401, true},
		{"wrong app secret", `{"error":"invalid_client","error_description":"Unauthorized"}`, 401, false},
		{"rate limited", `{"error":"invalid_grant"}`, 429, false},
		{"service unavailable", `{"error":"invalid_grant"}`, 503, false},
		{"network unavailable", "", 0, false},
		{"unexpected response", `upstream contains private-refresh-token`, 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			if tc.status == 0 {
				upstream.Close()
			}
			module := openInvestmentModule(t)
			module.schwabAPI = upstream.URL
			seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1/oauth/schwab", AccessToken: "old-access", RefreshToken: "private-refresh-token", TokenExpiresAt: time.Now().Add(-time.Minute)})
			_, err := module.ensureAccessToken(context.Background())
			if err == nil || errors.Is(err, errSchwabReauthorizationRequired) != tc.reauthorize {
				t.Fatalf("unexpected refresh error: %v", err)
			}
			rec, err := module.loadSchwab(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if rec.ReauthorizationRequired != tc.reauthorize || rec.view().Connected == tc.reauthorize {
				t.Fatalf("unexpected authorization state: %+v", rec.view())
			}
			if strings.Contains(rec.LastError, "private-refresh-token") {
				t.Fatal("token exposed in error")
			}
			if tc.reauthorize {
				if rec.AccessToken != "" || rec.RefreshToken != "" || !rec.TokenExpiresAt.IsZero() {
					t.Fatal("invalid tokens were retained")
				}
				if _, err := module.ensureAccessToken(context.Background()); !errors.Is(err, errSchwabReauthorizationRequired) {
					t.Fatal(err)
				}
				if calls.Load() != 1 {
					t.Fatal("retried a refresh token already known to be invalid")
				}
				response := httptest.NewRecorder()
				module.oauthRefreshHTTP(response, httptest.NewRequest("POST", "/", strings.NewReader(`{}`)))
				if response.Code != 409 || !strings.Contains(response.Body.String(), "schwab_reauthorization_required") {
					t.Fatal(response.Body.String())
				}
			} else if rec.AccessToken != "old-access" || rec.RefreshToken != "private-refresh-token" {
				t.Fatal("recoverable failure discarded existing authorization")
			}
		})
	}
}

func TestSchwabTemporaryFailureCanRecoverWithoutLogin(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "expires_in": 1800})
	}))
	defer upstream.Close()
	module := openInvestmentModule(t)
	module.schwabAPI = upstream.URL
	seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", AccessToken: "old-access", RefreshToken: "refresh"})
	if _, err := module.ensureAccessToken(context.Background()); err == nil {
		t.Fatal("expected temporary error")
	}
	if token, err := module.ensureAccessToken(context.Background()); err != nil || token != "new-access" {
		t.Fatalf("recovery: %s %v", token, err)
	}
	rec, err := module.loadSchwab(context.Background())
	if err != nil || rec.LastError != "" || rec.ReauthorizationRequired || rec.RefreshToken != "refresh" {
		t.Fatalf("recovered state: %+v %v", rec.view(), err)
	}
}

func TestSchwabReauthorizationStateSurvivesSaveAndClearsAfterCallback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 1800})
	}))
	defer upstream.Close()
	module := openInvestmentModule(t)
	module.schwabAPI = upstream.URL
	seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1/oauth/schwab", ReauthorizationRequired: true})
	save := httptest.NewRecorder()
	module.saveSchwab(save, httptest.NewRequest("PUT", "/", strings.NewReader(`{"appKey":"key","appSecret":"","callbackUrl":"https://127.0.0.1/oauth/schwab"}`)))
	if save.Code != 200 || !strings.Contains(save.Body.String(), `"reauthorizationRequired":true`) {
		t.Fatal(save.Body.String())
	}
	login := httptest.NewRecorder()
	module.oauthLogin(login, httptest.NewRequest("GET", "/", nil))
	if login.Code != 302 {
		t.Fatal(login.Body.String())
	}
	target, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := module.loadSchwab(context.Background())
	if err != nil || !rec.ReauthorizationRequired {
		t.Fatal("starting authorization must not mark it successful")
	}
	callback := httptest.NewRecorder()
	module.oauthCallback(callback, httptest.NewRequest("GET", "/oauth/schwab?code=new-code&state="+target.Query().Get("state"), nil))
	if callback.Code != 200 || !strings.Contains(callback.Body.String(), "url=/investment") {
		t.Fatal(callback.Body.String())
	}
	rec, err = module.loadSchwab(context.Background())
	if err != nil || rec.ReauthorizationRequired || !rec.view().Connected || rec.LastError != "" {
		t.Fatalf("reauthorization did not restore connection: %+v %v", rec.view(), err)
	}
}

func TestSchwabRejectedAccessChecksRefreshWithoutReplayingOrder(t *testing.T) {
	for _, invalidRefresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "access refreshed", true: "reauthorization required"}[invalidRefresh], func(t *testing.T) {
			var orderCalls, refreshCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/oauth/token" {
					refreshCalls.Add(1)
					if invalidRefresh {
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "expires_in": 1800})
					return
				}
				orderCalls.Add(1)
				w.WriteHeader(401)
			}))
			defer upstream.Close()
			module := openInvestmentModule(t)
			module.schwabAPI = upstream.URL
			seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", AccessToken: "old-access", RefreshToken: "refresh", TokenExpiresAt: time.Now().Add(time.Hour)})
			router := chi.NewRouter()
			router.Post("/trader/*", module.proxySchwab("/trader/v1"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("POST", "/trader/accounts/A/orders", strings.NewReader(`{}`)))
			if orderCalls.Load() != 1 || refreshCalls.Load() != 1 {
				t.Fatal("order replayed or rejected access token not refreshed")
			}
			rec, err := module.loadSchwab(context.Background())
			if err != nil || rec.ReauthorizationRequired != invalidRefresh {
				t.Fatalf("wrong state: %+v %v", rec.view(), err)
			}
			if invalidRefresh && !strings.Contains(response.Body.String(), "schwab_reauthorization_required") {
				t.Fatal(response.Body.String())
			}
		})
	}
}

func TestSchwabLateUnauthorizedResponseCannotInvalidateNewAuthorization(t *testing.T) {
	module := openInvestmentModule(t)
	seedSchwab(t, module, schwabRecord{AccessToken: "new-access", RefreshToken: "new-refresh", TokenExpiresAt: time.Now().Add(time.Hour)})
	if err := module.refreshRejectedAccessToken(context.Background(), "old-access"); err != nil {
		t.Fatal(err)
	}
	rec, err := module.loadSchwab(context.Background())
	if err != nil || rec.AccessToken != "new-access" || rec.RefreshToken != "new-refresh" || rec.ReauthorizationRequired || rec.TokenExpiresAt.Before(time.Now()) {
		t.Fatal("old request damaged new authorization")
	}
}
