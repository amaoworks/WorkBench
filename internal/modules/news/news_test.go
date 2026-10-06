package news

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"workbench/internal/capabilities/notifications"
	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
	"workbench/internal/foundation/events"
	"workbench/internal/modules/news/sqlc"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type textFixture struct {
	mu    sync.Mutex
	calls int
	fn    func(context.Context, contracts.TextRequest) (string, error)
}

func (f *textFixture) GenerateText(ctx context.Context, r contracts.TextRequest) (string, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(ctx, r)
	}
	return `{"translatedTitle":"市场消息","translatedContent":"收入增长百分之十。","summary":"公司收入增长10%。"}`, nil
}
func (f *textFixture) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }
func openModule(t *testing.T) (*Module, *workbenchdb.Database, *textFixture) {
	t.Helper()
	ctx := context.Background()
	db, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = db.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	store := events.NewStore(db.SQL())
	ns, err := notifications.NewService(db.SQL(), store)
	if err != nil {
		t.Fatal(err)
	}
	ai := &textFixture{}
	m, err := New(Dependencies{DB: db.SQL(), Events: store, Notifications: ns, AI: ai})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	if err = db.MigrateModule(ctx, "news", m.Migrations()); err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }
	return m, db, ai
}
func response(r *http.Request, status int, body string) *http.Response {
	return &http.Response{Request: r, StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func rss(now time.Time, guid, link, title, body string) string {
	return fmt.Sprintf(`<rss version="2.0"><channel><language>en</language><item><guid>%s</guid><title>%s</title><link>%s</link><pubDate>%s</pubDate><description><![CDATA[%s]]></description></item></channel></rss>`, guid, title, link, now.Format(time.RFC1123Z), body)
}
func addSource(t *testing.T, m *Module, name string) Source {
	t.Helper()
	s, err := m.SaveSource(context.Background(), "", SourceInput{Name: name, Kind: "rss", URL: "https://example.com/" + name, Topic: "市场", Enabled: true, IntervalMinutes: 30})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func firstArticle(t *testing.T, m *Module) newssqlc.NewsArticle {
	t.Helper()
	rows, err := m.q.ListAllArticles(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatalf("articles=%v, err=%v", rows, err)
	}
	return rows[0]
}
func ingest(t *testing.T, m *Module) Source {
	t.Helper()
	s := addSource(t, m, "feed")
	m.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return response(r, 200, rss(m.now(), "guid-1", "https://media.example/story?utm_source=x", "Company earnings", "Revenue increased 10%.")), nil
	})}
	if _, err := m.RefreshSource(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFeedFormatsAndTextSafety(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	raw := `<rss xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><language>en</language><item><guid>identifier</guid><title>A &amp; B</title><link>/story?utm_source=feed&amp;page=2</link><description>fallback</description><content:encoded><![CDATA[<p>Revenue <b>10%</b></p><script>bad()</script>]]></content:encoded><pubDate>Thu, 01 Oct 2026 12:00:00 +0000</pubDate></item></channel></rss>`
	items, err := parseFeed([]byte(raw), "https://media.example/feed", now)
	if err != nil || len(items) != 1 || items[0].Content != "Revenue 10%" || items[0].Title != "A & B" || items[0].URL != "https://media.example/story?page=2" {
		t.Fatalf("RSS=%+v err=%v", items, err)
	}
	atom := `<feed xmlns="http://www.w3.org/2005/Atom" xml:lang="en"><entry><id>urn:story:1</id><title type="html">A &amp;amp; B</title><link rel="self" href="https://media.example/feed"/><link rel="alternate" href="/story"/><updated>2026-10-01T11:30:00Z</updated><content type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml"><p>Original <b>body</b></p></div></content></entry></feed>`
	items, err = parseFeed([]byte(atom), "https://media.example/feed", now)
	if err != nil || len(items) != 1 || items[0].Content != "Original body" || items[0].URL != "https://media.example/story" || items[0].GUID != "urn:story:1" {
		t.Fatalf("Atom=%+v err=%v", items, err)
	}
	for _, bad := range []string{`<html><body>blocked</body></html>`, `<rss>`} {
		if _, err = parseFeed([]byte(bad), "https://example.com", now); err == nil {
			t.Fatal("accepted invalid feed")
		}
	}
	items, err = parseFeed([]byte(`<rss><channel><item><guid>opaque-id</guid><title>Without link</title></item></channel></rss>`), "https://example.com/feed", now)
	if err != nil || items[0].URL != "" {
		t.Fatal("invented a URL from an opaque guid", items, err)
	}
}

func TestFetchDeduplicatesAcrossSourcesAndPreservesReadingState(t *testing.T) {
	m, _, _ := openModule(t)
	ctx := context.Background()
	s := ingest(t, m)
	a := firstArticle(t, m)
	if err := m.q.SetArticleRead(ctx, newssqlc.SetArticleReadParams{ID: a.ID, IsRead: 1}); err != nil {
		t.Fatal(err)
	}
	if err := m.q.SetArticleStarred(ctx, newssqlc.SetArticleStarredParams{ID: a.ID, Starred: 1}); err != nil {
		t.Fatal(err)
	}
	other := addSource(t, m, "other")
	m.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return response(r, 200, rss(m.now(), "another-guid", "https://media.example/story?fbclid=x", "Company earnings", "Revenue increased 10%.")), nil
	})
	result, err := m.RefreshSource(ctx, other.ID)
	if err != nil || result.Added != 0 {
		t.Fatal(result, err)
	}
	got, err := m.Article(ctx, a.ID)
	if err != nil || len(got.Origins) != 2 || !got.Read || !got.Starred {
		t.Fatal(got, err)
	}
	// Same GUID remains the same article even if a publisher changes the URL.
	m.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return response(r, 200, rss(m.now(), "guid-1", "https://media.example/renamed", "Company earnings revised", "Revenue increased 12%.")), nil
	})
	result, err = m.RefreshSource(ctx, s.ID)
	if err != nil || result.Added != 0 {
		t.Fatal(result, err)
	}
	updated := firstArticle(t, m)
	if updated.ID != a.ID || updated.Url != "https://media.example/renamed" || updated.Content != "Revenue increased 12%." || updated.IsRead != 1 || updated.Starred != 1 {
		t.Fatal(updated)
	}
	if err = m.DeleteSource(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	updated = firstArticle(t, m)
	if updated.Starred != 1 {
		t.Fatal("source removal deleted retained reading state")
	}
}

func TestConditionalFetchAndPersistentBackoff(t *testing.T) {
	m, _, _ := openModule(t)
	s := addSource(t, m, "conditional")
	calls := 0
	m.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			v := response(r, 200, rss(m.now(), "one", "https://media.example/one", "First", "Body"))
			v.Header.Set("ETag", `"v1"`)
			return v, nil
		}
		if r.Header.Get("If-None-Match") != `"v1"` {
			t.Error("missing conditional header")
		}
		if calls == 2 {
			return response(r, 304, ""), nil
		}
		v := response(r, 429, "")
		v.Header.Set("Retry-After", "7200")
		return v, nil
	})
	if _, err := m.RefreshSource(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	r, err := m.RefreshSource(context.Background(), s.ID)
	if err != nil || !r.NotModified {
		t.Fatal(r, err)
	}
	if _, err = m.RefreshSource(context.Background(), s.ID); err == nil {
		t.Fatal("accepted HTTP 429")
	}
	rows, err := m.Sources(context.Background())
	if err != nil || !strings.Contains(rows[0].LastError, "429") || rows[0].NextFetchAt.Before(m.now().Add(2*time.Hour)) {
		t.Fatal(rows, err)
	}
	before := calls
	if _, err = m.Refresh(context.Background(), true); err != nil || before != calls {
		t.Fatal("ignored persistent backoff", calls, err)
	}
}

func TestRulesRematchExistingArticlesBeforeAI(t *testing.T) {
	m, _, _ := openModule(t)
	s := ingest(t, m)
	ctx := context.Background()
	rule, err := m.SaveRule(ctx, "", RuleInput{Name: "Focus", IncludeKeywords: []string{"earnings"}, ExcludeKeywords: []string{"advertisement"}, SourceIDs: []string{s.ID}, Topic: "财报", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	a := firstArticle(t, m)
	if a.Matched != 1 || a.Topic != "财报" {
		t.Fatal(a)
	}
	rule.IncludeKeywords = []string{"unrelated"}
	if _, err = m.SaveRule(ctx, rule.ID, rule.RuleInput); err != nil {
		t.Fatal(err)
	}
	a = firstArticle(t, m)
	if a.Matched != 0 || a.Topic != "市场" {
		t.Fatal("rule edit did not rematch", a)
	}
	if err = m.DeleteRule(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	if firstArticle(t, m).Matched != 1 {
		t.Fatal("empty rules should match all")
	}
	// Equal JSON strings must still decode into each distinct field.
	r, e := ruleDTO(newssqlc.NewsRule{IncludeJson: `["same"]`, ExcludeJson: `["same"]`, SourcesJson: `["same"]`})
	if e != nil || len(r.IncludeKeywords) != 1 || len(r.ExcludeKeywords) != 1 || len(r.SourceIDs) != 1 {
		t.Fatal(r, e)
	}
}

func TestAICacheQuotaAndLanguageInvalidation(t *testing.T) {
	m, _, ai := openModule(t)
	ingest(t, m)
	ctx := context.Background()
	id := firstArticle(t, m).ID
	s, _ := m.Settings(ctx)
	s.DailyAILimit = 1
	if _, err := m.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	a, err := m.Analyze(ctx, id)
	if err != nil || a.AIStatus != "ready" || a.TranslatedTitle == "" {
		t.Fatal(a, err)
	}
	if _, err = m.Analyze(ctx, id); err != nil || ai.count() != 1 {
		t.Fatal("cache made an extra request", ai.count(), err)
	}
	s.TargetLanguage = "日本語"
	if _, err = m.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Analyze(ctx, id); !errors.Is(err, ErrAILimit) || ai.count() != 1 {
		t.Fatal("quota did not block generation", ai.count(), err)
	}
	stats, err := m.Stats(ctx)
	if err != nil || stats.AICallsToday != 1 {
		t.Fatal(stats, err)
	}
	tomorrow := m.now().Add(24 * time.Hour)
	m.now = func() time.Time { return tomorrow }
	if _, err = m.Analyze(ctx, id); err != nil || ai.count() != 2 {
		t.Fatal("next-day quota did not reset", ai.count(), err)
	}
}

func TestUnavailableAIDoesNotConsumeBudgetAndDigestKeepsLinks(t *testing.T) {
	m, _, ai := openModule(t)
	ingest(t, m)
	ai.fn = func(context.Context, contracts.TextRequest) (string, error) { return "", contracts.ErrAIUnavailable }
	ctx := context.Background()
	if _, err := m.Analyze(ctx, firstArticle(t, m).ID); !errors.Is(err, contracts.ErrAIUnavailable) {
		t.Fatal(err)
	}
	d, err := m.CreateDigest(ctx, DigestInput{PeriodHours: 24, MaxItems: 5}, "")
	if err != nil || d.AIUsed || len(d.Items) != 1 || d.Items[0].URL != "https://media.example/story" {
		t.Fatal(d, err)
	}
	stats, err := m.Stats(ctx)
	if err != nil || stats.AICallsToday != 0 {
		t.Fatal(stats, err)
	}
}

func TestScheduledDigestAndPushAreIdempotentAndAtomic(t *testing.T) {
	m, db, ai := openModule(t)
	// UTC noon is after the scheduled hour and keeps expiry ahead of the notification wall clock.
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	m.now = func() time.Time { return now }
	ingest(t, m)
	ai.fn = func(context.Context, contracts.TextRequest) (string, error) {
		return "公司收入增长10%。[1]", nil
	}
	ctx := context.Background()
	s, _ := m.Settings(ctx)
	s.DigestEnabled = true
	s.PushEnabled = true
	s.DigestHour = 8
	s.TimeZone = "UTC"
	if _, err := m.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := m.digestJob(ctx, contracts.JobRun{}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := m.q.ListDigestsPage(ctx, newssqlc.ListDigestsPageParams{CursorTime: 1 << 62, CursorID: "z", PageLimit: 10})
	if err != nil || len(rows) != 1 || ai.count() != 1 {
		t.Fatal(rows, ai.count(), err)
	}
	d, err := m.PushDigest(ctx, rows[0].ID)
	if err != nil || d.NotificationID == "" {
		t.Fatal(d, err)
	}
	var count, external int
	var expires, created int64
	if err = db.SQL().QueryRow("SELECT COUNT(*),MAX(external),MAX(expires_at),MAX(created_at) FROM notifications WHERE source_module='news'").Scan(&count, &external, &expires, &created); err != nil || count != 1 || external != 1 || expires <= created {
		t.Fatal(count, external, expires, created, err)
	}
	m.events = brokenPublisher{}
	if _, err = m.CreateDigest(ctx, DigestInput{PeriodHours: 24, MaxItems: 5, Push: true}, ""); err == nil {
		t.Fatal("created digest despite domain event failure")
	}
	if err = db.SQL().QueryRow("SELECT COUNT(*) FROM notifications WHERE source_module='news'").Scan(&count); err != nil || count != 1 {
		t.Fatal("notification escaped rollback", count, err)
	}
	if err = db.SQL().QueryRow("SELECT COUNT(*) FROM news_digests").Scan(&count); err != nil || count != 1 {
		t.Fatal("digest escaped rollback", count, err)
	}
}

type brokenPublisher struct{}

func (brokenPublisher) Publish(context.Context, contracts.NewEvent) (contracts.EventID, error) {
	return "", errors.New("event failure")
}
func (brokenPublisher) PublishTx(context.Context, *sql.Tx, contracts.NewEvent) (contracts.EventID, error) {
	return "", errors.New("event failure")
}

func TestDisableCancelsInFlightAIAndReenableRestoresWork(t *testing.T) {
	m, _, ai := openModule(t)
	ingest(t, m)
	id := firstArticle(t, m).ID
	started := make(chan struct{})
	done := make(chan error, 1)
	ai.fn = func(ctx context.Context, _ contracts.TextRequest) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	go func() { _, err := m.Analyze(context.Background(), id); done <- err }()
	<-started
	if err := m.OnEnabledChanged(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled request succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disable did not cancel AI")
	}
	if err := m.OnEnabledChanged(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	ai.fn = nil
	if _, err := m.Analyze(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionPreservesFavorites(t *testing.T) {
	m, _, _ := openModule(t)
	ingest(t, m)
	ctx := context.Background()
	id := firstArticle(t, m).ID
	if err := m.q.SetArticleStarred(ctx, newssqlc.SetArticleStarredParams{ID: id, Starred: 1}); err != nil {
		t.Fatal(err)
	}
	later := m.now().Add(100 * 24 * time.Hour)
	m.now = func() time.Time { return later }
	s, _ := m.Settings(ctx)
	if err := m.cleanup(ctx, s); err != nil {
		t.Fatal(err)
	}
	firstArticle(t, m)
	if err := m.q.SetArticleStarred(ctx, newssqlc.SetArticleStarredParams{ID: id, Starred: 0}); err != nil {
		t.Fatal(err)
	}
	if err := m.cleanup(ctx, s); err != nil {
		t.Fatal(err)
	}
	rows, err := m.q.ListAllArticles(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

func TestChangedGUIDURLMergesExistingArticles(t *testing.T) {
	m, _, _ := openModule(t)
	ctx := context.Background()
	left := ingest(t, m)
	old := firstArticle(t, m)
	if err := m.q.SetArticleStarred(ctx, newssqlc.SetArticleStarredParams{ID: old.ID, Starred: 1}); err != nil {
		t.Fatal(err)
	}
	right := addSource(t, m, "second")
	m.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return response(r, 200, rss(m.now(), "second-guid", "https://media.example/second", "Second", "Other body")), nil
	})
	if _, err := m.RefreshSource(ctx, right.ID); err != nil {
		t.Fatal(err)
	}
	m.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return response(r, 200, rss(m.now(), "guid-1", "https://media.example/second", "Company earnings", "Moved body")), nil
	})
	if _, err := m.RefreshSource(ctx, left.ID); err != nil {
		t.Fatal(err)
	}
	a := firstArticle(t, m)
	origins, err := m.q.ListOrigins(ctx, a.ID)
	if err != nil || len(origins) != 2 || a.Starred != 1 || a.Url != "https://media.example/second" {
		t.Fatal(a, origins, err)
	}
	if _, err = m.RefreshSource(ctx, left.ID); err != nil {
		t.Fatal("repeat merge", err)
	}
	firstArticle(t, m)
}

func TestScrapedMetadataAndAIInputAreBoundedBeforeQuota(t *testing.T) {
	m, _, ai := openModule(t)
	ctx := context.Background()
	src := addSource(t, m, "large")
	body := strings.Replace(rss(m.now(), strings.Repeat("x", 10000), "https://media.example/meta", "Title", "Body"), "</item>", "<source>"+strings.Repeat("publisher", 15000)+"</source></item>", 1)
	m.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) { return response(r, 200, body), nil })
	if _, err := m.RefreshSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	a := firstArticle(t, m)
	if len([]rune(a.SourceName)) > 200 {
		t.Fatal("unbounded publisher")
	}
	ai.fn = func(_ context.Context, r contracts.TextRequest) (string, error) {
		if len(r.Input) > 64000 {
			t.Error("oversized request")
		}
		return `{"translatedTitle":"标题","translatedContent":"正文","summary":"摘要"}`, nil
	}
	if _, err := m.Analyze(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Settings(ctx)
	before, _ := m.Stats(ctx)
	if _, err := m.generate(ctx, s, "test", strings.Repeat("x", 64001)); err == nil {
		t.Fatal("accepted oversized request")
	}
	after, _ := m.Stats(ctx)
	if before.AICallsToday != after.AICallsToday || ai.count() != 1 {
		t.Fatal("invalid request consumed quota", before, after)
	}
}

func TestFeedWriteAndItsEventRollBackTogether(t *testing.T) {
	m, _, _ := openModule(t)
	ctx := context.Background()
	src := addSource(t, m, "rollback")
	m.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		v := response(r, 200, rss(m.now(), "first", "https://media.example/rollback", "Title", "Body"))
		v.Header.Set("ETag", `"rollback"`)
		return v, nil
	})
	m.events = brokenPublisher{}
	if _, err := m.RefreshSource(ctx, src.ID); err == nil {
		t.Fatal("accepted failed ingestion event")
	}
	articles, err := m.q.ListAllArticles(ctx)
	if err != nil || len(articles) != 0 {
		t.Fatal("article escaped rollback", articles, err)
	}
	row, err := m.q.GetSource(ctx, src.ID)
	if err != nil || row.Etag != "" {
		t.Fatal("fetch checkpoint escaped rollback", row, err)
	}
}
