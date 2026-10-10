package news

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"workbench/internal/contracts"
)

func awaitNews[T any](t *testing.T, result <-chan T) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("unrelated operation waited for an in-flight request")
		var zero T
		return zero
	}
}

func holdNews(t *testing.T) (chan struct{}, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	return release, unblock
}

func TestSourceFetchDoesNotBlockOtherSourcesOrConfiguration(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotModified, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			m, _, _ := openModule(t)
			slow := addSource(t, m, "slow")
			fast := addSource(t, m, "fast")
			started := make(chan struct{}, 1)
			release, unblock := holdNews(t)
			m.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "slow") {
					started <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
					return response(r, status, rss(m.now(), "slow", "https://media.example/slow", "Old source", "old")), nil
				}
				return response(r, 200, rss(m.now(), "fast", "https://media.example/fast", "Fast source", "fast")), nil
			})}
			ctx := context.Background()
			done := make(chan error, 1)
			go func() { _, err := m.RefreshSource(ctx, slow.ID); done <- err }()
			awaitNews(t, started)
			other := make(chan error, 1)
			go func() { _, err := m.RefreshSource(ctx, fast.ID); other <- err }()
			if err := awaitNews(t, other); err != nil {
				t.Fatal(err)
			}
			input := slow.SourceInput
			input.URL = "https://example.com/replacement"
			go func() { _, err := m.SaveSource(ctx, slow.ID, input); other <- err }()
			if err := awaitNews(t, other); err != nil {
				t.Fatal(err)
			}
			unblock()
			if err := awaitNews(t, done); err == nil {
				t.Fatal("accepted a result from the replaced source")
			}
			current, err := m.q.GetSource(ctx, slow.ID)
			if err != nil || current.FetchedAt != 0 || current.FailureCount != 0 || current.Url != input.URL {
				t.Fatal("stale fetch changed source state", current, err)
			}
			article := firstArticle(t, m)
			if article.Title != "Fast source" {
				t.Fatal("stale source result was ingested", article)
			}
		})
	}
}

func TestBatchRefreshStartsOtherSourcesWhileOneIsSlow(t *testing.T) {
	m, _, _ := openModule(t)
	addSource(t, m, "a-slow")
	addSource(t, m, "z-fast")
	started := make(chan string, 2)
	release, unblock := holdNews(t)
	m.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		started <- r.URL.Path
		if strings.Contains(r.URL.Path, "slow") {
			<-release
		}
		return response(r, 200, rss(m.now(), r.URL.Path, "https://media.example"+r.URL.Path, r.URL.Path, "body")), nil
	})}
	done := make(chan error, 1)
	go func() { _, err := m.Refresh(context.Background(), false); done <- err }()
	paths := map[string]bool{awaitNews(t, started): true, awaitNews(t, started): true}
	if len(paths) != 2 {
		t.Fatal("did not fetch distinct sources", paths)
	}
	unblock()
	if err := awaitNews(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingSourceRefreshHonorsCancellation(t *testing.T) {
	m, _, _ := openModule(t)
	s := addSource(t, m, "slow")
	started := make(chan struct{}, 1)
	release, unblock := holdNews(t)
	m.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-release
		return response(r, 200, rss(m.now(), "one", "https://media.example/one", "One", "body")), nil
	})}
	done := make(chan error, 1)
	go func() { _, err := m.RefreshSource(context.Background(), s.ID); done <- err }()
	awaitNews(t, started)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waiting := make(chan error, 1)
	go func() { _, err := m.RefreshSource(ctx, s.ID); waiting <- err }()
	if err := awaitNews(t, waiting); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unblock()
	if err := awaitNews(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestAnalysisDoesNotBlockDigestOrSettingsAndDiscardsOldLanguage(t *testing.T) {
	m, _, ai := openModule(t)
	ingest(t, m)
	id := firstArticle(t, m).ID
	started := make(chan struct{}, 1)
	release, unblock := holdNews(t)
	ai.fn = func(_ context.Context, request contracts.TextRequest) (string, error) {
		if strings.Contains(request.Instruction, "仅返回JSON对象") {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			return `{"translatedTitle":"译文","translatedContent":"正文","summary":"摘要"}`, nil
		}
		return "简报 [1]", nil
	}
	ctx := context.Background()
	done := make(chan error, 1)
	go func() { _, err := m.Analyze(ctx, id); done <- err }()
	awaitNews(t, started)
	other := make(chan error, 1)
	go func() {
		_, err := m.CreateDigest(ctx, DigestInput{PeriodHours: 24, MaxItems: 5}, "scheduled:test")
		other <- err
	}()
	if err := awaitNews(t, other); err != nil {
		t.Fatal(err)
	}
	s, err := m.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.TargetLanguage = "日本語"
	go func() { _, err := m.SaveSettings(ctx, s); other <- err }()
	if err := awaitNews(t, other); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err := awaitNews(t, done); err != nil {
		t.Fatal(err)
	}
	article, err := m.Article(ctx, id)
	if err != nil || article.AIStatus != "none" || article.TranslatedTitle != "" {
		t.Fatal("old language result overwrote settings invalidation", article, err)
	}
	if _, err := m.Analyze(ctx, id); err != nil || ai.count() != 3 {
		t.Fatal(ai.count(), err)
	}
}

func TestConcurrentAnalysisAndDigestRespectSharedBudget(t *testing.T) {
	m, _, ai := openModule(t)
	ingest(t, m)
	id := firstArticle(t, m).ID
	ctx := context.Background()
	s, _ := m.Settings(ctx)
	s.DailyAILimit = 1
	if _, err := m.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	release, unblock := holdNews(t)
	ai.fn = func(context.Context, contracts.TextRequest) (string, error) {
		started <- struct{}{}
		<-release
		return `{"translatedTitle":"译文","translatedContent":"正文","summary":"摘要"}`, nil
	}
	done := make(chan error, 2)
	go func() { _, err := m.Analyze(ctx, id); done <- err }()
	awaitNews(t, started)
	go func() { _, err := m.Analyze(ctx, id); done <- err }()
	digests := make(chan Digest, 1)
	go func() {
		d, err := m.CreateDigest(ctx, DigestInput{PeriodHours: 24, MaxItems: 5}, "scheduled:budget")
		if err != nil {
			done <- err
		}
		digests <- d
	}()
	if d := awaitNews(t, digests); d.ID == "" || d.AIUsed {
		t.Fatal("quota fallback failed", d)
	}
	unblock()
	for range 2 {
		if err := awaitNews(t, done); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := m.Stats(ctx)
	if err != nil || stats.AICallsToday != 1 || ai.count() != 1 {
		t.Fatal(stats, ai.count(), err)
	}
}

func TestDigestDiscardsChangedSettingsWithoutSavingOrNotifying(t *testing.T) {
	m, db, ai := openModule(t)
	// Keep pushed notifications valid against the notification service clock.
	now := time.Now().UTC()
	m.now = func() time.Time { return now }
	ingest(t, m)
	started := make(chan struct{}, 1)
	release, unblock := holdNews(t)
	ai.fn = func(context.Context, contracts.TextRequest) (string, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return "简报 [1]", nil
	}
	ctx := context.Background()
	done := make(chan error, 1)
	input := DigestInput{PeriodHours: 24, MaxItems: 5, Push: true}
	go func() { _, err := m.CreateDigest(ctx, input, "scheduled:changed"); done <- err }()
	awaitNews(t, started)
	s, err := m.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.TargetLanguage = "日本語"
	changed := make(chan error, 1)
	go func() { _, err := m.SaveSettings(ctx, s); changed <- err }()
	if err := awaitNews(t, changed); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err := awaitNews(t, done); err == nil {
		t.Fatal("saved a stale digest")
	}
	for _, query := range []string{"SELECT COUNT(*) FROM news_digests", "SELECT COUNT(*) FROM notifications WHERE source_module='news'"} {
		var count int
		if err := db.SQL().QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatal("stale digest escaped", count, err)
		}
	}
	if _, err := m.CreateDigest(ctx, input, "scheduled:changed"); err != nil {
		t.Fatal("changed slot could not be retried", err)
	}
}
