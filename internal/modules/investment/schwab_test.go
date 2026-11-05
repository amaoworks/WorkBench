package investment

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"workbench/internal/capabilities/notifications"
	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
	"workbench/internal/foundation/events"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

func testInvestmentDependencies(t *testing.T, db *sql.DB) Dependencies {
	t.Helper()
	service, err := notifications.NewService(db, events.NewStore(db))
	if err != nil {
		t.Fatal(err)
	}
	return Dependencies{DB: db, Notifications: service, Users: testInvestmentUsers{}}
}

func openInvestmentModule(t *testing.T) *Module {
	t.Helper()
	ctx := testContext(context.Background())
	db, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	module, err := New(testInvestmentDependencies(t, db.SQL()))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateModule(ctx, "investment", module.Migrations()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(module.Close)
	child := module.userModule("owner")
	module.futu.deps.load = child.loadFutu
	return child
}

func seedSchwab(t *testing.T, module *Module, rec schwabRecord) {
	t.Helper()
	module.tokenMu.Lock()
	defer module.tokenMu.Unlock()
	ctx := testContext(context.Background())
	app, err := module.queries.GetSchwabApp(ctx)
	if err != nil || app.AppKey != rec.AppKey || app.AppSecret != rec.AppSecret || app.CallbackUrl != rec.CallbackURL {
		if err := module.queries.SaveSchwabApp(ctx, investmentsqlc.SaveSchwabAppParams{AppKey: rec.AppKey, AppSecret: rec.AppSecret, CallbackUrl: rec.CallbackURL}); err != nil {
			t.Fatal(err)
		}
	}
	app, err = module.queries.GetSchwabApp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rec.AppVersion = app.Version
	if current, err := module.loadSchwab(ctx); err == nil {
		rec.ConnectionVersion = current.ConnectionVersion
	} else {
		t.Fatal(err)
	}
	if err := module.storeSchwab(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

func TestSchwabSettingsHideSecretAndRequireSecretOnKeyChange(t *testing.T) {
	module := openInvestmentModule(t)
	req := testRequest(http.MethodPut, "/api/modules/investment/schwab", strings.NewReader(`{"appKey":"key-a","appSecret":"secret","callbackUrl":"https://127.0.0.1:8080/oauth/schwab"}`))
	rec := httptest.NewRecorder()
	module.saveSchwab(rec, req)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("save leaked secret: %d %s", rec.Code, rec.Body.String())
	}
	got := httptest.NewRecorder()
	module.getSchwab(got, testRequest(http.MethodGet, "/api/modules/investment/schwab", nil))
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"hasAppSecret":true`) || strings.Contains(got.Body.String(), `"appSecret"`) {
		t.Fatalf("get: %s", got.Body.String())
	}
	change := httptest.NewRecorder()
	module.saveSchwab(change, testRequest(http.MethodPut, "/api/modules/investment/schwab", strings.NewReader(`{"appKey":"key-b","appSecret":"","callbackUrl":"https://127.0.0.1:8080/oauth/schwab"}`)))
	if change.Code != 400 {
		t.Fatalf("key change without secret: %d %s", change.Code, change.Body.String())
	}
}

func TestSchwabOAuthLoginAndCallback(t *testing.T) {
	var seenAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/oauth/token" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		seenAuth = r.Header.Get("Authorization")
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "abc" {
			t.Errorf("form=%v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 1800})
	}))
	defer upstream.Close()
	module := openInvestmentModule(t)
	module.schwabAPI = upstream.URL
	seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1:8080/oauth/schwab"})

	login := httptest.NewRecorder()
	module.oauthLogin(login, testRequest(http.MethodGet, "/api/modules/investment/schwab/oauth/login", nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	location := login.Header().Get("Location")
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/v1/oauth/authorize" || parsed.Query().Get("client_id") != "key" || parsed.Query().Get("redirect_uri") != "https://127.0.0.1:8080/oauth/schwab" {
		t.Fatalf("location=%s", location)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatal("missing state")
	}

	callback := httptest.NewRecorder()
	module.oauthCallback(callback, testRequest(http.MethodGet, "/oauth/schwab?code=abc&state="+state, nil))
	if callback.Code != 200 || !strings.Contains(callback.Body.String(), "授权成功") || !strings.Contains(callback.Body.String(), "返回投资") {
		t.Fatalf("callback: %d %s", callback.Code, callback.Body.String())
	}
	if !strings.Contains(callback.Body.String(), `content="0;url=/investment"`) || callback.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("callback must automatically return to the workbench without leaking the authorization URL")
	}
	if !strings.HasPrefix(seenAuth, "Basic ") {
		t.Fatalf("basic auth: %s", seenAuth)
	}
	stored, err := module.loadSchwab(testContext(context.Background()))
	if err != nil || stored.AccessToken != "access" || stored.RefreshToken != "refresh" || stored.OAuthState != "" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestSchwabMobileOAuthStartAndStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/oauth/token" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("code") != "mobile-code" {
			t.Fatalf("token form=%v err=%v", r.Form, err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "mobile-access", "refresh_token": "mobile-refresh", "expires_in": 1800})
	}))
	defer upstream.Close()
	module := openInvestmentModule(t)
	module.schwabAPI = upstream.URL
	seedSchwab(t, module, schwabRecord{
		AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1:8080/oauth/schwab", AccessToken: "old-access-token",
	})

	start := httptest.NewRecorder()
	request := testRequest(http.MethodPost, "/api/modules/investment/schwab/oauth/start", nil)
	request.Header.Set("Authorization", "Bearer app-access-token")
	module.oauthStart(start, request)
	if start.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", start.Code, start.Body.String())
	}
	var started mobileOAuthStartView
	if err := json.Unmarshal(start.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(started.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorizationURL.Query().Get("state")
	if started.StatusID == "" || started.Status != mobileOAuthPending || state == "" || started.StatusID == state {
		t.Fatalf("invalid start response: %+v", started)
	}
	if strings.Contains(started.AuthorizationURL, "app-access-token") || strings.Contains(started.AuthorizationURL, "old-access-token") {
		t.Fatal("authorization URL leaked an access token")
	}

	readStatus := func() (int, mobileOAuthStatusView) {
		t.Helper()
		response := httptest.NewRecorder()
		req := testRequest(http.MethodGet, "/api/modules/investment/schwab/oauth/status/"+started.StatusID, nil)
		req.SetPathValue("id", started.StatusID)
		module.oauthStatus(response, req)
		var body mobileOAuthStatusView
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		return response.Code, body
	}
	if code, status := readStatus(); code != http.StatusOK || status.Status != mobileOAuthPending {
		t.Fatalf("pending status: %d %+v", code, status)
	}

	callback := httptest.NewRecorder()
	module.oauthCallback(callback, testRequest(http.MethodGet, "/oauth/schwab?code=mobile-code&state="+url.QueryEscape(state), nil))
	if callback.Code != http.StatusOK {
		t.Fatalf("callback: %d %s", callback.Code, callback.Body.String())
	}
	if code, status := readStatus(); code != http.StatusOK || status.Status != mobileOAuthSucceeded || status.Error != "" {
		t.Fatalf("completed status: %d %+v", code, status)
	}
	stored, err := module.loadSchwab(testContext(context.Background()))
	if err != nil || stored.AccessToken != "mobile-access" || stored.RefreshToken != "mobile-refresh" || stored.OAuthState != "" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func startSchwabOAuth(t *testing.T, module *Module, mobile bool) (state, statusID string) {
	t.Helper()
	response := httptest.NewRecorder()
	var target string
	if mobile {
		module.oauthStart(response, testRequest(http.MethodPost, "/api/modules/investment/schwab/oauth/start", nil))
		var started mobileOAuthStartView
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &started) != nil {
			t.Fatalf("start: %d %s", response.Code, response.Body.String())
		}
		target, statusID = started.AuthorizationURL, started.StatusID
	} else {
		module.oauthLogin(response, testRequest(http.MethodGet, "/api/modules/investment/schwab/oauth/login", nil))
		if response.Code != http.StatusFound {
			t.Fatalf("login: %d %s", response.Code, response.Body.String())
		}
		target = response.Header().Get("Location")
	}
	authorizationURL, err := url.Parse(target)
	if err != nil || authorizationURL.Query().Get("state") == "" {
		t.Fatalf("authorization URL=%q err=%v", target, err)
	}
	return authorizationURL.Query().Get("state"), statusID
}

func readSchwabOAuthStatus(t *testing.T, module *Module, statusID string) mobileOAuthStatusView {
	t.Helper()
	response := httptest.NewRecorder()
	request := testRequest(http.MethodGet, "/api/modules/investment/schwab/oauth/status/"+statusID, nil)
	request.SetPathValue("id", statusID)
	module.oauthStatus(response, request)
	var status mobileOAuthStatusView
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
		t.Fatalf("status: %d %s", response.Code, response.Body.String())
	}
	return status
}

func TestSchwabOAuthExchangeFailureConsumesState(t *testing.T) {
	for _, mobile := range []bool{false, true} {
		name := "web"
		if mobile {
			name = "mobile"
		}
		t.Run(name, func(t *testing.T) {
			var exchanges atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if exchanges.Add(1) == 1 {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 1800})
			}))
			defer upstream.Close()
			module := openInvestmentModule(t)
			module.schwabAPI = upstream.URL
			seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1/oauth/schwab"})
			state, statusID := startSchwabOAuth(t, module, mobile)
			callbackURL := "/oauth/schwab?code=abc&state=" + state
			callback := httptest.NewRecorder()
			module.oauthCallback(callback, testRequest(http.MethodGet, callbackURL, nil))
			if callback.Code != http.StatusBadGateway {
				t.Fatalf("first callback: %d %s", callback.Code, callback.Body.String())
			}
			stored, err := module.loadSchwab(testContext(context.Background()))
			if err != nil || stored.OAuthState != "" || !stored.OAuthStateExpiresAt.IsZero() || stored.AccessToken != "" || stored.RefreshToken != "" || stored.LastError == "" {
				t.Fatalf("failed exchange: stored=%+v err=%v", stored, err)
			}
			var failed mobileOAuthStatusView
			if mobile {
				failed = readSchwabOAuthStatus(t, module, statusID)
				if failed.Status != mobileOAuthFailed || failed.Error == "" {
					t.Fatalf("failed status: %+v", failed)
				}
			}
			retry := httptest.NewRecorder()
			module.oauthCallback(retry, testRequest(http.MethodGet, callbackURL, nil))
			afterRetry, err := module.loadSchwab(testContext(context.Background()))
			if retry.Code != http.StatusBadRequest || exchanges.Load() != 1 || err != nil || afterRetry != stored {
				t.Fatalf("retry must not exchange or save tokens: code=%d exchanges=%d stored=%+v err=%v", retry.Code, exchanges.Load(), afterRetry, err)
			}
			if mobile && readSchwabOAuthStatus(t, module, statusID) != failed {
				t.Fatal("replay changed failed terminal status")
			}

			newState, newStatusID := startSchwabOAuth(t, module, mobile)
			success := httptest.NewRecorder()
			module.oauthCallback(success, testRequest(http.MethodGet, "/oauth/schwab?code=new-code&state="+newState, nil))
			stored, err = module.loadSchwab(testContext(context.Background()))
			if success.Code != http.StatusOK || exchanges.Load() != 2 || err != nil || stored.AccessToken != "access" || stored.RefreshToken != "refresh" || stored.OAuthState != "" {
				t.Fatalf("new grant: code=%d exchanges=%d stored=%+v err=%v", success.Code, exchanges.Load(), stored, err)
			}
			if mobile {
				status := readSchwabOAuthStatus(t, module, newStatusID)
				if status.Status != mobileOAuthSucceeded || status.Error != "" || readSchwabOAuthStatus(t, module, statusID) != failed {
					t.Fatalf("new grant must succeed without changing old result: %+v", status)
				}
			}
			for _, query := range []string{"code=new-code&state=", "error=access_denied&state="} {
				replay := httptest.NewRecorder()
				module.oauthCallback(replay, testRequest(http.MethodGet, "/oauth/schwab?"+query+newState, nil))
				if replay.Code != http.StatusBadRequest || exchanges.Load() != 2 {
					t.Fatalf("success replay: code=%d exchanges=%d", replay.Code, exchanges.Load())
				}
			}
			if mobile && readSchwabOAuthStatus(t, module, newStatusID).Status != mobileOAuthSucceeded {
				t.Fatal("replay changed succeeded terminal status")
			}
		})
	}
}

func TestSchwabOAuthRejectedCallbackConsumesMatchingState(t *testing.T) {
	for _, reason := range []string{"missing-code", "denied", "expired", "expiry-boundary"} {
		t.Run(reason, func(t *testing.T) {
			module := openInvestmentModule(t)
			now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
			module.now = func() time.Time { return now }
			var exchanges atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				exchanges.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 1800})
			}))
			defer upstream.Close()
			module.schwabAPI = upstream.URL
			seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1/oauth/schwab"})
			state, statusID := startSchwabOAuth(t, module, true)
			query := "state=" + state
			switch reason {
			case "denied":
				query += "&error=access_denied"
			case "expired", "expiry-boundary":
				now = now.Add(oauthStateTTL)
				if reason == "expired" {
					now = now.Add(time.Second)
				}
				query += "&code=abc"
			}
			callback := httptest.NewRecorder()
			module.oauthCallback(callback, testRequest(http.MethodGet, "/oauth/schwab?"+query, nil))
			stored, err := module.loadSchwab(testContext(context.Background()))
			status := readSchwabOAuthStatus(t, module, statusID)
			if callback.Code != http.StatusBadRequest || err != nil || stored.OAuthState != "" || !stored.OAuthStateExpiresAt.IsZero() || status.Status != mobileOAuthFailed {
				t.Fatalf("rejection: code=%d stored=%+v err=%v status=%+v", callback.Code, stored, err, status)
			}
			retry := httptest.NewRecorder()
			module.oauthCallback(retry, testRequest(http.MethodGet, "/oauth/schwab?code=abc&state="+state, nil))
			if retry.Code != http.StatusBadRequest || exchanges.Load() != 0 || readSchwabOAuthStatus(t, module, statusID) != status {
				t.Fatalf("rejected callback reused: code=%d exchanges=%d", retry.Code, exchanges.Load())
			}
		})
	}
}

func TestSchwabOAuthConcurrentCallbackAndStatus(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var exchanges atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exchanges.Add(1) == 1 {
			close(entered)
			<-release
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 1800})
	}))
	defer upstream.Close()
	module := openInvestmentModule(t)
	module.schwabAPI = upstream.URL
	var now atomic.Int64
	now.Store(time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC).UnixMilli())
	module.now = func() time.Time { return time.UnixMilli(now.Load()).UTC() }
	seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1/oauth/schwab"})
	state, statusID := startSchwabOAuth(t, module, true)
	callbackURL := "/oauth/schwab?code=abc&state=" + state
	callback, duplicate, statusResponse := httptest.NewRecorder(), httptest.NewRecorder(), httptest.NewRecorder()
	callbackDone, duplicateDone, statusDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		module.oauthCallback(callback, testRequest(http.MethodGet, callbackURL, nil))
		close(callbackDone)
	}()
	<-entered
	// The callback was accepted before expiry; polling must not fail it while exchanging.
	now.Add((oauthStateTTL + time.Second).Milliseconds())
	go func() {
		module.oauthCallback(duplicate, testRequest(http.MethodGet, callbackURL, nil))
		close(duplicateDone)
	}()
	go func() {
		request := testRequest(http.MethodGet, "/api/modules/investment/schwab/oauth/status/"+statusID, nil)
		request.SetPathValue("id", statusID)
		module.oauthStatus(statusResponse, request)
		close(statusDone)
	}()
	premature := false
	select {
	case <-statusDone:
		premature = true
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-callbackDone
	<-duplicateDone
	<-statusDone
	var status mobileOAuthStatusView
	if premature || statusResponse.Code != http.StatusOK || json.Unmarshal(statusResponse.Body.Bytes(), &status) != nil || status.Status != mobileOAuthSucceeded || status.Error != "" {
		t.Fatalf("in-flight status: premature=%v code=%d body=%s", premature, statusResponse.Code, statusResponse.Body.String())
	}
	stored, err := module.loadSchwab(testContext(context.Background()))
	if callback.Code != http.StatusOK || duplicate.Code != http.StatusBadRequest || exchanges.Load() != 1 || err != nil || stored.RefreshToken != "refresh" || stored.OAuthState != "" {
		t.Fatalf("concurrent callbacks: first=%d duplicate=%d exchanges=%d stored=%+v err=%v", callback.Code, duplicate.Code, exchanges.Load(), stored, err)
	}
}

func TestSchwabOAuthNewGrantSupersedesOldState(t *testing.T) {
	for _, mobile := range []bool{false, true} {
		name := "web"
		if mobile {
			name = "mobile"
		}
		t.Run(name, func(t *testing.T) {
			var exchanges atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				exchanges.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 1800})
			}))
			defer upstream.Close()
			module := openInvestmentModule(t)
			module.schwabAPI = upstream.URL
			seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1/oauth/schwab"})
			oldState, oldStatusID := startSchwabOAuth(t, module, true)
			newState, newStatusID := startSchwabOAuth(t, module, mobile)
			oldStatus := readSchwabOAuthStatus(t, module, oldStatusID)
			if oldStatus.Status != mobileOAuthFailed || !strings.Contains(oldStatus.Error, "替代") {
				t.Fatalf("superseded status: %+v", oldStatus)
			}
			for _, query := range []string{"code=old-code&state=" + oldState, "error=access_denied&state=" + oldState, "code=abc", "code=abc&state=unknown"} {
				callback := httptest.NewRecorder()
				module.oauthCallback(callback, testRequest(http.MethodGet, "/oauth/schwab?"+query, nil))
				stored, err := module.loadSchwab(testContext(context.Background()))
				if callback.Code != http.StatusBadRequest || exchanges.Load() != 0 || err != nil || stored.OAuthState != newState || readSchwabOAuthStatus(t, module, oldStatusID) != oldStatus {
					t.Fatalf("old/invalid callback affected new grant: code=%d exchanges=%d stored=%+v err=%v", callback.Code, exchanges.Load(), stored, err)
				}
			}
			callback := httptest.NewRecorder()
			module.oauthCallback(callback, testRequest(http.MethodGet, "/oauth/schwab?code=new-code&state="+newState, nil))
			if callback.Code != http.StatusOK || exchanges.Load() != 1 {
				t.Fatalf("new callback: code=%d exchanges=%d", callback.Code, exchanges.Load())
			}
			if mobile && readSchwabOAuthStatus(t, module, newStatusID).Status != mobileOAuthSucceeded {
				t.Fatal("new mobile grant did not succeed")
			}
		})
	}
}

func TestSchwabMobileOAuthStatusExpires(t *testing.T) {
	module := openInvestmentModule(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	module.now = func() time.Time { return now }
	module.oauthSessions["status-id"] = mobileOAuthSession{State: "state", Status: mobileOAuthPending, ExpiresAt: now.Add(-time.Second)}

	response := httptest.NewRecorder()
	req := testRequest(http.MethodGet, "/api/modules/investment/schwab/oauth/status/status-id", nil)
	req.SetPathValue("id", "status-id")
	module.oauthStatus(response, req)
	var status mobileOAuthStatusView
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || status.Status != mobileOAuthFailed || !strings.Contains(status.Error, "过期") {
		t.Fatalf("expired status: %d %+v", response.Code, status)
	}
}

func TestSchwabProxyInjectsBearerAndBlocksTraversal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/marketdata/v1/instruments" || r.URL.Query().Get("symbol") != "AAPL" {
			t.Errorf("upstream path %s query %s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer access-token" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Cookie") != "" {
			t.Error("cookie forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	module := openInvestmentModule(t)
	module.schwabAPI = upstream.URL
	seedSchwab(t, module, schwabRecord{
		AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1:8080/oauth/schwab",
		AccessToken: "access-token", RefreshToken: "refresh", TokenExpiresAt: module.now().UTC().Add(time.Hour),
	})
	router := chi.NewRouter()
	router.Get("/api/modules/investment/schwab/marketdata/v1/*", module.proxySchwab("/marketdata/v1"))
	ok := httptest.NewRecorder()
	req := testRequest(http.MethodGet, "/api/modules/investment/schwab/marketdata/v1/instruments?symbol=AAPL", nil)
	req.Header.Set("Cookie", "workbench_session=nope")
	router.ServeHTTP(ok, req)
	if ok.Code != 200 || !strings.Contains(ok.Body.String(), `"ok":true`) {
		t.Fatalf("proxy: %d %s", ok.Code, ok.Body.String())
	}
	bad := httptest.NewRecorder()
	router.ServeHTTP(bad, testRequest(http.MethodGet, "/api/modules/investment/schwab/marketdata/v1/../secrets", nil))
	if bad.Code != http.StatusBadRequest && bad.Code != http.StatusNotFound {
		t.Fatalf("traversal: %d %s", bad.Code, bad.Body.String())
	}
}

func TestChartingLibraryProxyStripsCookiesAndFrameOptions(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/charting_library/charting_library.esm.js" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("leaked headers cookie=%q auth=%q", r.Header.Get("Cookie"), r.Header.Get("Authorization"))
		}
		w.Header().Set("X-Frame-Options", "sameorigin")
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = io.WriteString(w, "export default 1;")
	}))
	defer upstream.Close()
	module := openInvestmentModule(t)
	module.tvOrigin = upstream.URL
	module.tvProxy = newTVProxy(upstream.URL, "")
	router := chi.NewRouter()
	router.Get("/charting_library/*", module.proxyChartingLibrary)
	got := httptest.NewRecorder()
	req := testRequest(http.MethodGet, "/charting_library/charting_library.esm.js", nil)
	req.Header.Set("Cookie", "workbench_session=secret")
	req.Header.Set("Authorization", "Bearer nope")
	router.ServeHTTP(got, req)
	if got.Code != 200 || got.Body.String() != "export default 1;" {
		t.Fatalf("tv proxy: %d %s", got.Code, got.Body.String())
	}
	if got.Header().Get("X-Frame-Options") != "" {
		t.Fatalf("frame options leaked: %s", got.Header().Get("X-Frame-Options"))
	}
}

func TestTerminalAssetsAreEmbedded(t *testing.T) {
	module := openInvestmentModule(t)
	html := httptest.NewRecorder()
	module.serveTerminal(html, testRequest(http.MethodGet, "/investment/terminal", nil))
	if html.Code != 200 || !strings.Contains(html.Body.String(), "charting_library") || !strings.Contains(html.Body.String(), "datafeed.js") {
		t.Fatalf("html: %d %s", html.Code, html.Body.String())
	}
	js := httptest.NewRecorder()
	module.serveTerminal(js, testRequest(http.MethodGet, "/investment/terminal/datafeed.js", nil))
	if js.Code != 200 || !strings.Contains(js.Body.String(), "export default class Datafeed") {
		t.Fatalf("datafeed: %d", js.Code)
	}
}

func TestStreamerCommandWithoutConnection(t *testing.T) {
	module := openInvestmentModule(t)
	got := httptest.NewRecorder()
	module.streamerCommand(got, testRequest(http.MethodPost, "/api/modules/investment/schwab/marketdata/ws/command", strings.NewReader(`{"service":"LEVELONE_EQUITIES","command":"ADD","keys":"AAPL"}`)))
	if got.Code != 409 {
		t.Fatalf("expected disconnected, got %d %s", got.Code, got.Body.String())
	}
}

func TestSchwabCallbackChangeRequiresSecretAndExactPath(t *testing.T) {
	module := openInvestmentModule(t)
	seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "https://127.0.0.1/oauth/schwab"})
	for _, input := range []string{
		`{"appKey":"key","appSecret":"secret","callbackUrl":"http://127.0.0.1/oauth/schwab"}`,
		`{"appKey":"key","appSecret":"","callbackUrl":"https://127.0.0.1:8080/oauth/schwab"}`,
		`{"appKey":"key","appSecret":"secret","callbackUrl":"https://127.0.0.1/foo/oauth/schwab"}`,
	} {
		got := httptest.NewRecorder()
		module.saveSchwab(got, testRequest("PUT", "/", strings.NewReader(input)))
		if got.Code != 400 {
			t.Fatalf("invalid callback change accepted: %d", got.Code)
		}
	}
}

func TestSchwabLoginRejectsLegacyHTTPCallback(t *testing.T) {
	module := openInvestmentModule(t)
	seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "http://127.0.0.1/oauth/schwab"})
	for _, role := range []contracts.Role{contracts.RoleAdmin, contracts.RoleUser} {
		t.Run(string(role), func(t *testing.T) {
			ctx := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: "owner", Role: role})
			request := httptest.NewRequest(http.MethodGet, "/api/modules/investment/schwab/oauth/login", nil).WithContext(ctx)
			got := httptest.NewRecorder()
			module.oauthLogin(got, request)
			body := got.Body.String()
			if got.Code != http.StatusBadRequest || got.Header().Get("Location") != "" || !strings.Contains(body, "HTTPS") {
				t.Fatalf("invalid legacy callback must be rejected before navigating to Schwab: %d", got.Code)
			}
			if role == contracts.RoleAdmin {
				if !strings.Contains(body, `href="/admin/settings?tab=modules&amp;module=investment"`) || strings.Contains(body, "联系管理员") {
					t.Fatalf("admin must receive the shared application settings link: %s", body)
				}
			} else if !strings.Contains(body, "联系管理员修正回调地址") || strings.Contains(body, "/admin/settings") {
				t.Fatalf("ordinary user must not be directed to administrator settings: %s", body)
			}
			if !strings.Contains(body, "授权未完成") || !strings.Contains(body, `href="/investment"`) || !strings.Contains(body, "返回投资") {
				t.Fatalf("oauth result must return to investment: %s", body)
			}
		})
	}
}

func testContext(ctx context.Context) context.Context {
	return contracts.WithPrincipal(ctx, contracts.Principal{UserID: "owner", Role: contracts.RoleAdmin, SessionID: "test-session"})
}
func testRequest(method, target string, body io.Reader) *http.Request {
	return httptest.NewRequest(method, target, body).WithContext(testContext(context.Background()))
}

type testInvestmentUsers struct{}

func (testInvestmentUsers) ActiveUserIDs(context.Context) ([]string, error) {
	return []string{"owner"}, nil
}
func (testInvestmentUsers) IsUserActive(_ context.Context, id string) (bool, error) {
	return id != "", nil
}
func testAuthenticated(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { handler(w, r.WithContext(testContext(r.Context()))) }
}
