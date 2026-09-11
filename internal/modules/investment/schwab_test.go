package investment

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	workbenchdb "workbench/internal/foundation/database"
)

func openInvestmentModule(t *testing.T) *Module {
	t.Helper()
	ctx := context.Background()
	db, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	module, err := New(Dependencies{DB: db.SQL()})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateModule(ctx, "investment", module.Migrations()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(module.Close)
	return module
}

func seedSchwab(t *testing.T, module *Module, rec schwabRecord) {
	t.Helper()
	module.tokenMu.Lock()
	defer module.tokenMu.Unlock()
	if err := module.storeSchwab(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}

func TestSchwabSettingsHideSecretAndRequireSecretOnKeyChange(t *testing.T) {
	module := openInvestmentModule(t)
	req := httptest.NewRequest(http.MethodPut, "/api/modules/investment/schwab", strings.NewReader(`{"appKey":"key-a","appSecret":"secret","callbackUrl":"https://127.0.0.1:8080/oauth/schwab"}`))
	rec := httptest.NewRecorder()
	module.saveSchwab(rec, req)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("save leaked secret: %d %s", rec.Code, rec.Body.String())
	}
	got := httptest.NewRecorder()
	module.getSchwab(got, httptest.NewRequest(http.MethodGet, "/api/modules/investment/schwab", nil))
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"hasAppSecret":true`) || strings.Contains(got.Body.String(), `"appSecret"`) {
		t.Fatalf("get: %s", got.Body.String())
	}
	change := httptest.NewRecorder()
	module.saveSchwab(change, httptest.NewRequest(http.MethodPut, "/api/modules/investment/schwab", strings.NewReader(`{"appKey":"key-b","appSecret":"","callbackUrl":"https://127.0.0.1:8080/oauth/schwab"}`)))
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
	module.oauthLogin(login, httptest.NewRequest(http.MethodGet, "/api/modules/investment/schwab/oauth/login", nil))
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
	module.oauthCallback(callback, httptest.NewRequest(http.MethodGet, "/oauth/schwab?code=abc&state="+state, nil))
	if callback.Code != 200 || !strings.Contains(callback.Body.String(), "授权成功") {
		t.Fatalf("callback: %d %s", callback.Code, callback.Body.String())
	}
	if !strings.Contains(callback.Body.String(), `content="0;url=/investment"`) || callback.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("callback must automatically return to the workbench without leaking the authorization URL")
	}
	if !strings.HasPrefix(seenAuth, "Basic ") {
		t.Fatalf("basic auth: %s", seenAuth)
	}
	stored, err := module.loadSchwab(context.Background())
	if err != nil || stored.AccessToken != "access" || stored.RefreshToken != "refresh" || stored.OAuthState != "" {
		t.Fatalf("stored=%+v err=%v", stored, err)
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
	req := httptest.NewRequest(http.MethodGet, "/api/modules/investment/schwab/marketdata/v1/instruments?symbol=AAPL", nil)
	req.Header.Set("Cookie", "workbench_session=nope")
	router.ServeHTTP(ok, req)
	if ok.Code != 200 || !strings.Contains(ok.Body.String(), `"ok":true`) {
		t.Fatalf("proxy: %d %s", ok.Code, ok.Body.String())
	}
	bad := httptest.NewRecorder()
	router.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/modules/investment/schwab/marketdata/v1/../secrets", nil))
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
	module.tvProxy = newTVProxy(upstream.URL)
	router := chi.NewRouter()
	router.Get("/charting_library/*", module.proxyChartingLibrary)
	got := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/charting_library/charting_library.esm.js", nil)
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
	module.serveTerminal(html, httptest.NewRequest(http.MethodGet, "/investment/terminal", nil))
	if html.Code != 200 || !strings.Contains(html.Body.String(), "charting_library") || !strings.Contains(html.Body.String(), "datafeed.js") {
		t.Fatalf("html: %d %s", html.Code, html.Body.String())
	}
	js := httptest.NewRecorder()
	module.serveTerminal(js, httptest.NewRequest(http.MethodGet, "/investment/terminal/datafeed.js", nil))
	if js.Code != 200 || !strings.Contains(js.Body.String(), "export default class Datafeed") {
		t.Fatalf("datafeed: %d", js.Code)
	}
}

func TestStreamerCommandWithoutConnection(t *testing.T) {
	module := openInvestmentModule(t)
	got := httptest.NewRecorder()
	module.streamerCommand(got, httptest.NewRequest(http.MethodPost, "/api/modules/investment/schwab/marketdata/ws/command", strings.NewReader(`{"service":"LEVELONE_EQUITIES","command":"ADD","keys":"AAPL"}`)))
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
		module.saveSchwab(got, httptest.NewRequest("PUT", "/", strings.NewReader(input)))
		if got.Code != 400 {
			t.Fatalf("invalid callback change accepted: %d", got.Code)
		}
	}
}

func TestSchwabLoginRejectsLegacyHTTPCallback(t *testing.T) {
	module := openInvestmentModule(t)
	seedSchwab(t, module, schwabRecord{AppKey: "key", AppSecret: "secret", CallbackURL: "http://127.0.0.1/oauth/schwab"})
	got := httptest.NewRecorder()
	module.oauthLogin(got, httptest.NewRequest(http.MethodGet, "/api/modules/investment/schwab/oauth/login", nil))
	if got.Code != http.StatusBadRequest || got.Header().Get("Location") != "" || !strings.Contains(got.Body.String(), "HTTPS") {
		t.Fatalf("invalid legacy callback must be rejected before navigating to Schwab: %d", got.Code)
	}
}
