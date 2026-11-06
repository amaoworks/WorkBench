package news

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
	newssqlc "workbench/internal/modules/news/sqlc"
)

func userContext(id string) context.Context {
	return contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: id, Role: contracts.RoleUser})
}

func userSource(t *testing.T, m *Module, ctx context.Context, name string) Source {
	t.Helper()
	s, err := m.SaveSource(ctx, "", SourceInput{Name: name, Kind: "rss", URL: "https://example.com/" + name, Enabled: true, IntervalMinutes: 30})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func userIngest(t *testing.T, m *Module, ctx context.Context, name, url, title string) (Source, Article) {
	t.Helper()
	s := userSource(t, m, ctx, name)
	m.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return response(r, 200, rss(m.now(), "same-guid", url, title, "Private body for "+title)), nil
	})}
	if _, err := m.RefreshSource(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	a, err := m.Article(ctx, hash(url))
	if err != nil {
		t.Fatal(err)
	}
	return s, a
}

type isolationRegistrar struct{ mux *http.ServeMux }

func (r *isolationRegistrar) Handle(method, path string, h http.Handler) error {
	r.mux.Handle(method+" "+path, h)
	return nil
}
func (*isolationRegistrar) Consume(contracts.EventConsumer) error   { return nil }
func (*isolationRegistrar) Tool(contracts.AITool) error             { return nil }
func (*isolationRegistrar) Job(contracts.JobDefinition) error       { return nil }
func (*isolationRegistrar) Widget(contracts.WidgetDefinition) error { return nil }

func newsRouter(t *testing.T, m *Module) *http.ServeMux {
	t.Helper()
	r := &isolationRegistrar{mux: http.NewServeMux()}
	if err := m.Register(r); err != nil {
		t.Fatal(err)
	}
	return r.mux
}
func newsRequest(router http.Handler, ctx context.Context, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/modules/news/"+path, strings.NewReader(body)).WithContext(ctx)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func TestPrivateNewsSourcesRulesArticlesAndSettings(t *testing.T) {
	m, _, ai := openModule(t)
	a, b := userContext("alice"), userContext("bob")
	sa, aa := userIngest(t, m, a, "alice", "https://media.example/shared", "Alice private headline")
	sb, ab := userIngest(t, m, b, "bob", "https://media.example/shared", "Bob private headline")
	if aa.ID != ab.ID {
		t.Fatal("fixture should exercise identical article identifiers across users")
	}
	for _, tc := range []struct {
		ctx          context.Context
		own, foreign Source
		title        string
	}{{a, sa, sb, aa.Title}, {b, sb, sa, ab.Title}} {
		sources, err := m.Sources(tc.ctx)
		if err != nil || len(sources) != 1 || sources[0].ID != tc.own.ID {
			t.Fatalf("sources=%+v err=%v", sources, err)
		}
		if _, err := m.SaveSource(tc.ctx, tc.foreign.ID, tc.foreign.SourceInput); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("foreign source update: %v", err)
		}
		if _, err := m.RefreshSource(tc.ctx, tc.foreign.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("foreign source refresh: %v", err)
		}
		if err := m.DeleteSource(tc.ctx, tc.foreign.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("foreign source delete: %v", err)
		}
		page, err := m.articles(tc.ctx, articleListParams{Limit: 100})
		if err != nil || len(page.Items) != 1 || page.Items[0].Title != tc.title || len(page.Items[0].Origins) != 1 || page.Items[0].Origins[0].SourceID != tc.own.ID {
			t.Fatalf("articles=%+v err=%v", page, err)
		}
		page, err = m.articles(tc.ctx, articleListParams{Limit: 100, SourceID: tc.foreign.ID})
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("foreign source filter=%+v %v", page, err)
		}
	}
	rule, err := m.SaveRule(a, "", RuleInput{Name: "Alice filter", IncludeKeywords: []string{"unmatched"}, SourceIDs: []string{sa.ID}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveRule(b, rule.ID, rule.RuleInput); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign rule update: %v", err)
	}
	if err := m.DeleteRule(b, rule.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign rule deletion: %v", err)
	}
	if _, err := m.SaveRule(b, "", RuleInput{Name: "foreign source", SourceIDs: []string{sa.ID}, Enabled: true}); err == nil {
		t.Fatal("foreign source accepted in rule")
	}
	ar, _ := m.Article(a, aa.ID)
	br, _ := m.Article(b, ab.ID)
	if ar.Matched || !br.Matched {
		t.Fatal("rule rematching crossed owners", ar, br)
	}
	rules, err := m.Rules(b)
	if err != nil || len(rules) != 0 {
		t.Fatal(rules, err)
	}
	if err := m.DeleteRule(a, rule.ID); err != nil {
		t.Fatal(err)
	}

	router := newsRouter(t, m)
	if w := newsRequest(router, a, "PATCH", "articles/"+aa.ID, `{"read":true,"starred":true}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	br, _ = m.Article(b, ab.ID)
	if br.Read || br.Starred {
		t.Fatal("reading/favorite state shared", br)
	}
	st, err := m.Stats(a)
	if err != nil || st.Sources != 1 || st.Articles != 1 || st.Unread != 0 || st.Starred != 1 {
		t.Fatal(st, err)
	}
	st, err = m.Stats(b)
	if err != nil || st.Sources != 1 || st.Articles != 1 || st.Unread != 1 || st.Starred != 0 {
		t.Fatal(st, err)
	}
	brief, err := m.Brief(b, nil)
	if err != nil || len(brief.Latest) != 1 || brief.Latest[0].Title != ab.Title || brief.Unread != 1 {
		t.Fatal(brief, err)
	}

	_, onlyB := userIngest(t, m, b, "bob-exclusive", "https://media.example/bob-only", "Bob secret")
	for _, read := range []func() error{
		func() error { _, e := m.Article(a, onlyB.ID); return e },
		func() error { _, e := m.Analyze(a, onlyB.ID); return e },
	} {
		if err := read(); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("foreign article: %v", err)
		}
	}
	if ai.count() != 0 {
		t.Fatal("foreign article was sent to AI")
	}
	if w := newsRequest(router, a, "PATCH", "articles/"+onlyB.ID, `{"read":true}`); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := newsRequest(router, a, "GET", "articles/"+onlyB.ID, ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	page, err := m.articles(b, articleListParams{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	other, err := m.articles(a, articleListParams{Limit: 1, Cursor: page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range other.Items {
		if item.Title != aa.Title {
			t.Fatal("foreign cursor exposed article", item)
		}
	}

	saSettings := defaults()
	saSettings.DailyAILimit = 1
	if _, err := m.SaveSettings(a, saSettings); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Analyze(a, aa.ID); err != nil {
		t.Fatal(err)
	}
	br, _ = m.Article(b, ab.ID)
	if br.AIStatus != "none" || br.Summary != "" {
		t.Fatal("AI summary shared", br)
	}
	if _, err := m.generate(a, "test", "test"); !errors.Is(err, ErrAILimit) {
		t.Fatal("alice quota", err)
	}
	if _, err := m.Analyze(b, ab.ID); err != nil {
		t.Fatal("bob inherited alice quota", err)
	}
	if ai.count() != 2 {
		t.Fatal("unexpected AI calls", ai.count())
	}
	saSettings.TargetLanguage = "日本語"
	if _, err := m.SaveSettings(a, saSettings); err != nil {
		t.Fatal(err)
	}
	ar, _ = m.Article(a, aa.ID)
	br, _ = m.Article(b, ab.ID)
	if ar.AIStatus != "none" || br.AIStatus != "ready" {
		t.Fatal("language invalidation crossed owners", ar, br)
	}
	bs, err := m.Settings(b)
	if err != nil || bs != defaults() {
		t.Fatal("settings shared", bs, err)
	}
	if err := m.DeleteSource(a, sa.ID); err != nil {
		t.Fatal(err)
	}
	br, err = m.Article(b, ab.ID)
	if err != nil || len(br.Origins) != 1 {
		t.Fatal("source cascade crossed owners", br, err)
	}
}

type recipientNotifications struct {
	contracts.NotificationService
	notes []contracts.Notification
}

func (n *recipientNotifications) CreateTx(ctx context.Context, _ *sql.Tx, in contracts.NewNotification) (contracts.Notification, error) {
	if in.RecipientID == "" || in.RecipientID != contracts.UserID(ctx) {
		return contracts.Notification{}, errors.New("notification missing trusted recipient")
	}
	note := contracts.Notification{ID: "note-" + strconv.Itoa(len(n.notes)), RecipientID: in.RecipientID, IdempotencyKey: in.IdempotencyKey, Content: in.Content}
	n.notes = append(n.notes, note)
	return note, nil
}

func (n *recipientNotifications) List(ctx context.Context, _ contracts.NotificationQuery) (contracts.NotificationPage, error) {
	page := contracts.NotificationPage{}
	for _, note := range n.notes {
		if note.RecipientID == contracts.UserID(ctx) {
			page.Items = append(page.Items, note)
		}
	}
	return page, nil
}

func TestPrivateNewsDigestsToolIdempotencyAndNotifications(t *testing.T) {
	m, _, ai := openModule(t)
	m.notifications = &recipientNotifications{NotificationService: m.notifications}
	a, b := userContext("alice"), userContext("bob")
	userIngest(t, m, a, "alice", "https://media.example/shared", "Alice digest")
	userIngest(t, m, b, "bob", "https://media.example/shared", "Bob digest")
	in := DigestInput{PeriodHours: 24, MaxItems: 10, Push: true}
	da, err := m.CreateDigest(a, in, "same-slot")
	if err != nil {
		t.Fatal(err)
	}
	db, err := m.CreateDigest(b, in, "same-slot")
	if err != nil {
		t.Fatal(err)
	}
	if da.ID == db.ID || da.Items[0].Title != "Alice digest" || db.Items[0].Title != "Bob digest" {
		t.Fatal("digest slot/items shared", da, db)
	}
	again, err := m.CreateDigest(a, in, "same-slot")
	if err != nil || again.ID != da.ID || ai.count() != 2 {
		t.Fatal("digest not idempotent", again, err, ai.count())
	}
	if _, err := m.PushDigest(a, db.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("foreign digest push", err)
	}
	for _, tc := range []struct {
		ctx    context.Context
		digest Digest
	}{{a, da}, {b, db}} {
		page, err := m.notifications.List(tc.ctx, contracts.NotificationQuery{Limit: 100})
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != tc.digest.NotificationID || page.Items[0].RecipientID != contracts.UserID(tc.ctx) {
			t.Fatal("notification recipient mismatch", page, err)
		}
		w := newsRequest(newsRouter(t, m), tc.ctx, "GET", "digests", "")
		if w.Code != 200 || strings.Contains(w.Body.String(), map[bool]string{true: db.ID, false: da.ID}[tc.digest.ID == da.ID]) {
			t.Fatal("digest page leaked", w.Code, w.Body.String())
		}
	}
	call := contracts.ToolCall{IdempotencyKey: "same-key", Arguments: json.RawMessage(`{"topic":"","periodHours":24,"maxItems":10}`)}
	toolA, err := m.createDigestTool(a, call)
	if err != nil {
		t.Fatal(err)
	}
	toolB, err := m.createDigestTool(b, call)
	if err != nil {
		t.Fatal(err)
	}
	var ta, tb Digest
	if json.Unmarshal(toolA.Content, &ta) != nil || json.Unmarshal(toolB.Content, &tb) != nil || ta.ID == tb.ID || ta.Items[0].Title == tb.Items[0].Title {
		t.Fatal("tool idempotency crossed users", ta, tb)
	}
	before := ai.count()
	repeated, err := m.createDigestTool(a, call)
	if err != nil || string(repeated.Content) != string(toolA.Content) || ai.count() != before {
		t.Fatal("tool replay changed result", repeated, err)
	}
}

func TestPrivateNewsRejectsMissingIdentity(t *testing.T) {
	m, _, _ := openModule(t)
	ctx := context.Background()
	for name, run := range map[string]func() error{
		"sources":        func() error { _, e := m.Sources(ctx); return e },
		"save-source":    func() error { _, e := m.SaveSource(ctx, "", SourceInput{}); return e },
		"delete-source":  func() error { return m.DeleteSource(ctx, "id") },
		"refresh":        func() error { _, e := m.Refresh(ctx, false); return e },
		"refresh-source": func() error { _, e := m.RefreshSource(ctx, "id"); return e },
		"rules":          func() error { _, e := m.Rules(ctx); return e },
		"save-rule":      func() error { _, e := m.SaveRule(ctx, "", RuleInput{}); return e },
		"delete-rule":    func() error { return m.DeleteRule(ctx, "id") },
		"article":        func() error { _, e := m.Article(ctx, "id"); return e },
		"articles":       func() error { _, e := m.articles(ctx, articleListParams{Limit: 10}); return e },
		"analyze":        func() error { _, e := m.Analyze(ctx, "id"); return e },
		"settings":       func() error { _, e := m.Settings(ctx); return e },
		"save-settings":  func() error { _, e := m.SaveSettings(ctx, defaults()); return e },
		"stats":          func() error { _, e := m.Stats(ctx); return e },
		"brief":          func() error { _, e := m.Brief(ctx, nil); return e },
		"digest":         func() error { _, e := m.CreateDigest(ctx, DigestInput{}, ""); return e },
		"push":           func() error { _, e := m.PushDigest(ctx, "id"); return e },
		"tool":           func() error { _, e := m.getArticleTool(ctx, contracts.ToolCall{}); return e },
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, contracts.ErrUnauthenticated) {
				t.Fatalf("missing identity: %v", err)
			}
		})
	}
	router := newsRouter(t, m)
	for _, path := range []string{"sources", "rules", "articles", "articles/id", "settings", "stats", "brief", "digests"} {
		if w := newsRequest(router, ctx, "GET", path, ""); w.Code != 401 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
}

type checkedUsers struct {
	listed   []string
	disabled map[string]bool
	checked  []string
}

func (u *checkedUsers) ActiveUserIDs(context.Context) ([]string, error) { return u.listed, nil }
func (u *checkedUsers) IsUserActive(_ context.Context, id string) (bool, error) {
	u.checked = append(u.checked, id)
	return !u.disabled[id], nil
}

func (u *checkedUsers) IsUserActiveTx(ctx context.Context, _ *sql.Tx, id string) (bool, error) {
	return u.IsUserActive(ctx, id)
}

func TestPrivateNewsJobsEnumerateActiveUsers(t *testing.T) {
	m, _, ai := openModule(t)
	m.notifications = &recipientNotifications{NotificationService: m.notifications}
	users := &checkedUsers{listed: []string{"alice", "bob", "disabled"}, disabled: map[string]bool{"disabled": true}}
	m.users = users
	for _, id := range users.listed {
		ctx := userContext(id)
		userSource(t, m, ctx, id)
		s := defaults()
		s.AutoAI = true
		s.DigestEnabled = true
		s.DigestHour = 0
		s.PushEnabled = true
		if _, err := m.SaveSettings(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	m.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return response(r, 200, rss(m.now(), "shared-guid", "https://media.example/shared", strings.TrimPrefix(r.URL.Path, "/"), "body")), nil
	})}
	global := userContext("not-a-job-owner")
	if err := m.fetchJob(global, contracts.JobRun{}); err != nil {
		t.Fatal(err)
	}
	if err := m.processJob(context.Background(), contracts.JobRun{}); err != nil {
		t.Fatal(err)
	}
	if err := m.digestJob(global, contracts.JobRun{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"alice", "bob"} {
		ctx := userContext(id)
		rows, err := m.q.ListAllArticles(ctx, id)
		if err != nil || len(rows) != 1 || rows[0].Title != id || rows[0].AiStatus != "ready" {
			t.Fatal(id, rows, err)
		}
		digests, err := m.q.ListDigestsPage(ctx, newssqlc.ListDigestsPageParams{UserID: id, CursorTime: math.MaxInt64, CursorID: "\uffff", PageLimit: 10})
		if err != nil || len(digests) != 1 {
			t.Fatal(id, digests, err)
		}
		page, err := m.notifications.List(ctx, contracts.NotificationQuery{Limit: 10})
		if err != nil || len(page.Items) != 1 || page.Items[0].RecipientID != id {
			t.Fatal(id, page, err)
		}
	}
	for _, id := range []string{"disabled", "not-a-job-owner"} {
		rows, err := m.q.ListAllArticles(userContext(id), id)
		if err != nil || len(rows) != 0 {
			t.Fatal("inactive/global user processed", id, rows, err)
		}
	}
	if ai.count() != 4 || len(users.checked) != 9 {
		t.Fatal("unexpected job identities", ai.count(), users.checked)
	}
	var visited []string
	sentinel := errors.New("alice failure")
	err := m.forActiveUsers(context.Background(), func(ctx context.Context) error {
		id := contracts.UserID(ctx)
		visited = append(visited, id)
		if id == "alice" {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) || !slices.Equal(visited, []string{"alice", "bob"}) {
		t.Fatal("one user blocked another", visited, err)
	}
}

func TestPrivateNewsCleanupDoesNotDeleteOtherUsers(t *testing.T) {
	m, _, _ := openModule(t)
	for _, id := range []string{"alice", "bob"} {
		ctx := userContext(id)
		if _, err := m.q.InsertArticle(ctx, newssqlc.InsertArticleParams{UserID: id, ID: "same-id", Title: id, Url: "https://media.example/old", SourceName: "source", Content: "body", PublishedAt: 1, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		if err := m.q.InsertDigest(ctx, newssqlc.InsertDigestParams{UserID: id, ID: "same-id", Slot: "same-slot", Title: id, Topic: "", Content: "body", ItemsJson: "[]", CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := m.q.ReserveAI(ctx, newssqlc.ReserveAIParams{UserID: id, Day: "2000-01-01"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.cleanup(userContext("alice"), defaults()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"alice", "bob"} {
		ctx := userContext(id)
		want := id == "bob"
		_, err := m.q.GetArticle(ctx, newssqlc.GetArticleParams{UserID: id, ID: "same-id"})
		if (err == nil) != want {
			t.Fatal(id, "article", err)
		}
		_, err = m.q.GetDigest(ctx, newssqlc.GetDigestParams{UserID: id, ID: "same-id"})
		if (err == nil) != want {
			t.Fatal(id, "digest", err)
		}
		_, err = m.q.GetAIUsage(ctx, newssqlc.GetAIUsageParams{UserID: id, Day: "2000-01-01"})
		if (err == nil) != want {
			t.Fatal(id, "usage", err)
		}
	}
}

func TestPrivateNewsMigrationBackfillsLegacyOwnership(t *testing.T) {
	ctx := testContext()
	db, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "migration.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, file := range []string{"migrations/00001_news.sql", "migrations/00002_users.sql"} {
		raw, err := migrations.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		up := strings.Split(strings.TrimPrefix(string(raw), "-- +goose Up"), "-- +goose Down")[0]
		if _, err := db.SQL().ExecContext(ctx, up); err != nil {
			t.Fatal(file, err)
		}
		if file == "migrations/00001_news.sql" {
			_, err = db.SQL().ExecContext(ctx, `INSERT INTO news_sources(id,name,kind,url) VALUES('source','old source','rss','https://example.com/rss');
INSERT INTO news_articles(id,title,url,source_name,content,published_at,created_at,is_read,starred,summary,ai_status) VALUES('article','old title','https://example.com/story','old source','old body',1,1,1,1,'old summary','ready');
INSERT INTO news_origins(source_id,item_key,article_id,source_name,url) VALUES('source','key','article','old source','https://example.com/story');
INSERT INTO news_rules(id,name,include_json,exclude_json,sources_json) VALUES('rule','old rule','[]','[]','["source"]');
UPDATE news_settings SET settings_json='{"dailyAILimit":7}',last_digest_at=123,last_digest_error='old error';
INSERT INTO news_digests(id,slot,title,topic,content,items_json,created_at,ai_used,notification_id) VALUES('digest','slot','old digest','','old body','[]',1,1,'notification');
INSERT INTO news_ai_usage(day,calls) VALUES('2000-01-01',3);`)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	q := newssqlc.New(db.SQL())
	for _, table := range []string{"news_sources", "news_articles", "news_origins", "news_rules", "news_settings", "news_digests", "news_ai_usage"} {
		var count int
		if err := db.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE user_id='owner'").Scan(&count); err != nil || count != 1 {
			t.Fatal(table, count, err)
		}
	}
	article, err := q.GetArticle(ctx, newssqlc.GetArticleParams{UserID: contracts.LegacyUserID, ID: "article"})
	if err != nil || article.IsRead != 1 || article.Starred != 1 || article.Summary != "old summary" {
		t.Fatal("legacy article state lost", article, err)
	}
	rows, err := q.ListAllArticles(userContext("new-user"), "new-user")
	if err != nil || len(rows) != 0 {
		t.Fatal("legacy data visible to new user", rows, err)
	}
	if _, err := q.InsertArticle(ctx, newssqlc.InsertArticleParams{UserID: "new-user", ID: "article", Title: "new", Url: article.Url, PublishedAt: 1, CreatedAt: 1}); err != nil {
		t.Fatal("URL/id uniqueness still global", err)
	}
	if _, err := q.InsertArticle(ctx, newssqlc.InsertArticleParams{UserID: "new-user", ID: "different", Title: "duplicate", Url: article.Url, PublishedAt: 1, CreatedAt: 1}); err == nil {
		t.Fatal("duplicate URL allowed for same user")
	}
	if err := q.SaveOrigin(ctx, newssqlc.SaveOriginParams{UserID: "new-user", SourceID: "source", ItemKey: "key", ArticleID: "article"}); err == nil {
		t.Fatal("cross-user origin foreign key accepted")
	}
	if err := q.InsertDigest(ctx, newssqlc.InsertDigestParams{UserID: "new-user", ID: "digest", Slot: "slot", ItemsJson: "[]"}); err != nil {
		t.Fatal("digest slot/id uniqueness still global", err)
	}
	if _, err := q.ReserveAI(ctx, newssqlc.ReserveAIParams{UserID: "new-user", Day: "2000-01-01"}); err != nil {
		t.Fatal(err)
	}
	calls, err := q.GetAIUsage(ctx, newssqlc.GetAIUsageParams{UserID: contracts.LegacyUserID, Day: "2000-01-01"})
	if err != nil || calls != 3 {
		t.Fatal("legacy quota changed", calls, err)
	}
	migration, err := migrations.ReadFile("migrations/00002_users.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := strings.Split(string(migration), "-- +goose Down")[1]
	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, down); err == nil {
		t.Fatal("rollback accepted non-owner private data")
	}
	_ = tx.Rollback()
	for _, table := range []string{"news_articles", "news_digests", "news_ai_usage"} {
		if _, err := db.SQL().ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id='new-user'"); err != nil {
			t.Fatal(err)
		}
	}
	tx, err = db.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, down); err != nil {
		t.Fatal("owner-only rollback", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var summary string
	if err := db.SQL().QueryRowContext(ctx, "SELECT summary FROM news_articles WHERE id='article'").Scan(&summary); err != nil || summary != "old summary" {
		t.Fatal("rollback lost legacy state", summary, err)
	}
}
