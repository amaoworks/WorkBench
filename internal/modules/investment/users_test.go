package investment

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"workbench/internal/contracts"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

type investmentUsers struct {
	mu     sync.Mutex
	active map[string]bool
}

func (u *investmentUsers) ActiveUserIDs(context.Context) ([]string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	ids := []string{}
	for id, active := range u.active {
		if active {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
func (u *investmentUsers) IsUserActive(_ context.Context, id string) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.active[id], nil
}
func (u *investmentUsers) disable(id string) { u.mu.Lock(); defer u.mu.Unlock(); u.active[id] = false }
func investorContext(id string) context.Context {
	return contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: id, Role: contracts.RoleUser, SessionID: "session-" + id})
}

type investmentRoutes struct {
	investmentToolRegistrar
	router *chi.Mux
}

func (r *investmentRoutes) Handle(method, path string, handler http.Handler) error {
	r.router.Method(method, path, handler)
	return nil
}
func multiInvestment(t *testing.T) (*Module, *chi.Mux, *investmentUsers) {
	t.Helper()
	root := openInvestmentModule(t).root
	users := &investmentUsers{active: map[string]bool{"owner": true, "alice": true, "bob": true}}
	root.deps.Users = users
	routes := &investmentRoutes{router: chi.NewRouter()}
	if err := root.Register(routes); err != nil {
		t.Fatal(err)
	}
	return root, routes.router, users
}
func investorRequest(router http.Handler, ctx context.Context, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}
func requireStatus(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
}

func TestInvestmentPersonalDataAndManagementPermissions(t *testing.T) {
	root, router, _ := multiInvestment(t)
	a, b := investorContext("alice"), investorContext("bob")
	prefix := "/api/modules/investment"
	requireStatus(t, investorRequest(router, context.Background(), "GET", prefix+"/preferences", ""), 401)
	for _, ctx := range []context.Context{a, b} {
		requireStatus(t, investorRequest(router, ctx, "PUT", prefix+"/schwab", `{}`), 403)
		requireStatus(t, investorRequest(router, ctx, "GET", prefix+"/futu", ""), 403)
		requireStatus(t, investorRequest(router, ctx, "PUT", prefix+"/futu", `{}`), 403)
		requireStatus(t, investorRequest(router, ctx, "POST", prefix+"/futu/disconnect", `{}`), 403)
		requireStatus(t, investorRequest(router, ctx, "PUT", prefix+"/overnight", `{"enabled":true}`), 403)
		requireStatus(t, investorRequest(router, ctx, "GET", prefix+"/overnight", ""), 200)
		requireStatus(t, investorRequest(router, ctx, "GET", prefix+"/watchlists", ""), 200)
		requireStatus(t, investorRequest(router, ctx, "GET", prefix+"/monitor", ""), 200)
	}
	for _, user := range []string{"alice", "bob"} {
		body := `{"name":"account","revision":0,"writer":"same-writer","sequence":1,"value":"` + user + `-account"}`
		requireStatus(t, investorRequest(router, investorContext(user), "PUT", prefix+"/preferences", body), 200)
		watch := `{"revision":0,"writer":"same-writer","sequence":1,"state":{"lists":[{"id":"same-list","title":"` + user + `","symbols":["AAPL"]}],"activeId":"same-list"}}`
		requireStatus(t, investorRequest(router, investorContext(user), "PUT", prefix+"/watchlists", watch), 200)
	}
	for _, entry := range []struct {
		ctx        context.Context
		own, other string
	}{{a, "alice", "bob"}, {b, "bob", "alice"}} {
		for _, path := range []string{"/preferences", "/watchlists"} {
			result := investorRequest(router, entry.ctx, "GET", prefix+path, "")
			requireStatus(t, result, 200)
			if !strings.Contains(result.Body.String(), entry.own) || strings.Contains(result.Body.String(), entry.other) {
				t.Fatalf("cross-user data: %s", result.Body.String())
			}
		}
	}
	created := investorRequest(router, a, "POST", prefix+"/monitor/rules", `{"symbol":"AAPL","direction":"up","thresholdPercent":3,"enabled":true}`)
	requireStatus(t, created, 201)
	var rule priceRuleView
	if err := json.Unmarshal(created.Body.Bytes(), &rule); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, investorRequest(router, b, "PUT", prefix+"/monitor/rules/"+rule.ID, `{"symbol":"MSFT","direction":"up","thresholdPercent":4,"enabled":true}`), 404)
	requireStatus(t, investorRequest(router, b, "DELETE", prefix+"/monitor/rules/"+rule.ID, ""), 404)
	other := investorRequest(router, b, "GET", prefix+"/monitor", "")
	if strings.Contains(other.Body.String(), "AAPL") {
		t.Fatal(other.Body.String())
	}
	// The same account and grant timestamp can belong to separate users without
	// net worth rows or account-scope collisions.
	for _, user := range []string{"alice", "bob"} {
		ctx := investorContext(user)
		value := float64(len(user))
		portfolio := portfolioResponse{Accounts: []portfolioAccount{{Hash: "same-hash", NetWorth: &value, Cash: &value, Invested: &value, DayPnl: &value}}}
		if err := root.userModule(user).storeNetWorthSnapshot(ctx, 123, portfolio); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []string{"alice", "bob"} {
		rows, err := root.queries.ListNetWorthSnapshots(investorContext(user), investmentsqlc.ListNetWorthSnapshotsParams{UserID: user, AuthorizedAt: 123})
		if err != nil || len(rows) != 1 || rows[0].NetWorth != float64(len(user)) {
			t.Fatalf("net worth isolation: %+v %v", rows, err)
		}
	}
}

func TestSchwabTwoUserOAuthAndPublicCallback(t *testing.T) {
	root, router, users := multiInvestment(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/oauth/token" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_ = r.ParseForm()
		code := r.Form.Get("code")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-" + code, "refresh_token": "refresh-" + code, "expires_in": 1800})
	}))
	defer upstream.Close()
	root.schwabAPI = upstream.URL
	prefix := "/api/modules/investment"
	admin := testContext(context.Background())
	requireStatus(t, investorRequest(router, admin, "PUT", prefix+"/schwab", `{"appKey":"shared-app","appSecret":"shared-secret","callbackUrl":"https://workbench.test/oauth/schwab"}`), 200)
	starts := map[string]mobileOAuthStartView{}
	for _, user := range []string{"alice", "bob"} {
		response := investorRequest(router, investorContext(user), "POST", prefix+"/schwab/oauth/start", `{}`)
		requireStatus(t, response, 201)
		var start mobileOAuthStartView
		if json.Unmarshal(response.Body.Bytes(), &start) != nil {
			t.Fatal(response.Body.String())
		}
		starts[user] = start
	}
	// A status identifier is not a bearer capability.
	requireStatus(t, investorRequest(router, investorContext("bob"), "GET", prefix+"/schwab/oauth/status/"+starts["alice"].StatusID, ""), 404)
	for _, user := range []string{"alice", "bob"} {
		target, _ := url.Parse(starts[user].AuthorizationURL)
		state := target.Query().Get("state")
		callback := httptest.NewRecorder()
		root.oauthCallback(callback, httptest.NewRequest("GET", "/oauth/schwab?state="+state+"&code="+user, nil))
		requireStatus(t, callback, 200)
		rec, err := root.userModule(user).loadSchwab(investorContext(user))
		if err != nil || rec.AccessToken != "access-"+user {
			t.Fatalf("wrong user grant: %+v %v", rec, err)
		}
		replay := httptest.NewRecorder()
		root.oauthCallback(replay, httptest.NewRequest("GET", "/oauth/schwab?state="+state+"&code=evil", nil))
		requireStatus(t, replay, 400)
		status := investorRequest(router, investorContext(user), "GET", prefix+"/schwab/oauth/status/"+starts[user].StatusID, "")
		requireStatus(t, status, 200)
		if !strings.Contains(status.Body.String(), "succeeded") {
			t.Fatal(status.Body.String())
		}
		settings := investorRequest(router, investorContext(user), "GET", prefix+"/schwab", "")
		requireStatus(t, settings, 200)
		if strings.Contains(settings.Body.String(), "shared-app") || strings.Contains(settings.Body.String(), "shared-secret") || !strings.Contains(settings.Body.String(), `"configured":true`) {
			t.Fatal(settings.Body.String())
		}
	}
	// Disconnecting Alice must not invalidate Bob's token or streamer.
	bob := root.userModule("bob")
	requireStatus(t, investorRequest(router, investorContext("alice"), "POST", prefix+"/schwab/disconnect", `{}`), 200)
	rec, err := bob.loadSchwab(investorContext("bob"))
	if err != nil || rec.RefreshToken != "refresh-bob" || root.userModule("alice").streamer == bob.streamer {
		t.Fatal("connections not independent", err)
	}
	// A disabled user cannot complete a pending public OAuth redirect.
	startResponse := investorRequest(router, investorContext("alice"), "POST", prefix+"/schwab/oauth/start", `{}`)
	requireStatus(t, startResponse, 201)
	var start mobileOAuthStartView
	_ = json.Unmarshal(startResponse.Body.Bytes(), &start)
	target, _ := url.Parse(start.AuthorizationURL)
	users.disable("alice")
	callback := httptest.NewRecorder()
	root.oauthCallback(callback, httptest.NewRequest("GET", "/oauth/schwab?state="+target.Query().Get("state")+"&code=late", nil))
	requireStatus(t, callback, 403)
	requireStatus(t, investorRequest(router, investorContext("alice"), "GET", prefix+"/preferences", ""), 401)
	// Re-enabling the same user must not revive the pre-disable authorization request.
	root.RevokeUser("alice")
	users.mu.Lock()
	users.active["alice"] = true
	users.mu.Unlock()
	late := httptest.NewRecorder()
	root.oauthCallback(late, httptest.NewRequest("GET", "/oauth/schwab?state="+target.Query().Get("state")+"&code=revived", nil))
	requireStatus(t, late, 400)
}

func TestSharedFutuBindsUserAndSessionAndFiltersStreams(t *testing.T) {
	owner, _ := newFutuBrowserFixture(t)
	root := owner.root
	users := &investmentUsers{active: map[string]bool{"owner": true, "alice": true, "bob": true}}
	root.deps.Users = users
	handler := root.personalHTTP((*Module).serveFutuStream)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := investorContext(r.URL.Query().Get("user"))
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer server.Close()
	open := func(user string) futuBrowser {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?user="+user, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var ready struct {
			Stream struct{ Owner, Generation string }
		}
		if err := conn.ReadJSON(&ready); err != nil {
			t.Fatal(err)
		}
		return futuBrowser{conn: conn, owner: ready.Stream.Owner, generation: ready.Stream.Generation}
	}
	a, b := open("alice"), open("bob")
	command := func(ctx context.Context, browser futuBrowser, symbol string) bool {
		t.Helper()
		_, applied, err := root.futu.browserCommand(ctx, "ADD", browser.owner, browser.generation, symbol, "5")
		if err != nil {
			t.Fatal(err)
		}
		return applied
	}
	if command(investorContext("bob"), a, "AAPL") {
		t.Fatal("Bob controlled Alice's WebSocket")
	}
	wrongSession := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: "alice", Role: contracts.RoleUser, SessionID: "different-session"})
	if command(wrongSession, a, "AAPL") {
		t.Fatal("another login controlled an existing lease")
	}
	if !command(investorContext("alice"), a, "AAPL") || !command(investorContext("bob"), b, "MSFT") {
		t.Fatal("own subscription rejected")
	}
	root.futu.broadcastKL("AAPL", "5", futuBar{Time: 1, Close: 10})
	root.futu.broadcastKL("MSFT", "5", futuBar{Time: 2, Close: 20})
	readSymbol := func(browser futuBrowser) string {
		t.Helper()
		var message struct{ Data []struct{ Symbol string } }
		if err := browser.conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if len(message.Data) != 1 {
			t.Fatal(message)
		}
		return message.Data[0].Symbol
	}
	if got := readSymbol(a); got != "AAPL" {
		t.Fatal(got)
	}
	if got := readSymbol(b); got != "MSFT" {
		t.Fatalf("Bob received Alice subscription: %s", got)
	}
	users.disable("alice")
	_ = a.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := a.conn.ReadMessage(); err == nil {
		t.Fatal("disabled user's stream remained open or received Bob's subscription")
	}
	waitFutuBrowserRemoved(t, root.futu, a.owner, 0, 0)
	root.futu.broadcastKL("MSFT", "5", futuBar{Time: 3, Close: 21})
	_ = b.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if got := readSymbol(b); got != "MSFT" {
		t.Fatal(got)
	}
	if command(investorContext("bob"), a, "AAPL") {
		t.Fatal("revoked owner accepted command")
	}
}

func TestSchwabProxyUsesCallerAndRejectsForeignAccount(t *testing.T) {
	root, router, _ := multiInvestment(t)
	var mu sync.Mutex
	orders := map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer token-")
		if r.URL.Path == "/trader/v1/accounts/accountNumbers" {
			_ = json.NewEncoder(w).Encode([]map[string]string{{"accountNumber": "123", "hashValue": "hash-" + owner}})
			return
		}
		if r.Method == "POST" {
			mu.Lock()
			orders[owner]++
			mu.Unlock()
			w.WriteHeader(201)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	root.schwabAPI = upstream.URL
	if err := root.queries.SaveSchwabApp(testContext(context.Background()), investmentsqlc.SaveSchwabAppParams{AppKey: "key", AppSecret: "secret", CallbackUrl: "https://workbench.test/oauth/schwab"}); err != nil {
		t.Fatal(err)
	}
	app, _ := root.queries.GetSchwabApp(context.Background())
	for _, user := range []string{"alice", "bob"} {
		if err := root.userModule(user).storeSchwab(investorContext(user), schwabRecord{AppVersion: app.Version, AccessToken: "token-" + user, RefreshToken: "refresh-" + user, TokenExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	prefix := "/api/modules/investment/schwab/trader/v1/"
	requireStatus(t, investorRequest(router, investorContext("alice"), "POST", prefix+"accounts/hash-bob/orders", `{}`), 403)
	requireStatus(t, investorRequest(router, investorContext("alice"), "PATCH", prefix+"accounts/hash-alice/orders/1", `{}`), 400)
	for _, user := range []string{"alice", "bob"} {
		requireStatus(t, investorRequest(router, investorContext(user), "POST", prefix+"accounts/hash-"+user+"/orders", `{}`), 201)
	}
	mu.Lock()
	defer mu.Unlock()
	if orders["alice"] != 1 || orders["bob"] != 1 {
		t.Fatal(orders)
	}
	var count int
	if err := root.deps.DB.QueryRow("SELECT COUNT(*) FROM investment_trade_audit WHERE outcome='responded' AND http_status=201").Scan(&count); err != nil || count != 2 {
		t.Fatalf("audit=%d %v", count, err)
	}
}
