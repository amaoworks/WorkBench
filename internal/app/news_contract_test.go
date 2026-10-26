package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"workbench/internal/contracts"
)

type newsProviderFixture struct{}

func (newsProviderFixture) Generate(_ context.Context, r contracts.GenerateRequest) (contracts.GenerateResult, error) {
	text := "公司收入增长10%。[1]"
	if len(r.Messages) > 0 && strings.Contains(string(r.Messages[0].Content), "translatedTitle") {
		text = `{"translatedTitle":"公司财报","translatedContent":"公司收入增长10%。","summary":"收入增长10%。"}`
	}
	return contracts.GenerateResult{Text: text}, nil
}
func (newsProviderFixture) Stream(context.Context, contracts.GenerateRequest) (contracts.AIStream, error) {
	return nil, errors.New("unused fixture stream")
}
func captureNewsContracts(t *testing.T, a *App, capture func(string, string, string, string, string, int) json.RawMessage) {
	t.Helper()
	a.textAI.SetProvider(newsProviderFixture{})
	now := time.Now().UTC().Format(time.RFC1123Z)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken" {
			w.WriteHeader(429)
			return
		}
		if r.Header.Get("If-None-Match") == `"fixture"` {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"fixture"`)
		fmt.Fprintf(w, `<rss><channel><item><guid>one</guid><title>Company earnings</title><link>https://media.example/one</link><pubDate>%s</pubDate><description>Revenue increased 10%%.</description></item><item><guid>two</guid><title>Other news</title><link>https://media.example/two</link><pubDate>%s</pubDate><description>Second article.</description></item></channel></rss>`, now, now)
	}))
	defer server.Close()
	prefix := "/api/modules/news"
	capture("news.sources.empty", "newsSources", "GET", prefix+"/sources", "", 200)
	capture("news.brief.empty", "newsBrief", "GET", prefix+"/brief", "", 200)
	sourceRaw := capture("news.source.created", "newsSource", "POST", prefix+"/sources", fmt.Sprintf(`{"name":"Finance","kind":"rss","url":%q,"query":"","topic":"市场","enabled":true,"intervalMinutes":30}`, server.URL), 201)
	var src struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(sourceRaw, &src); err != nil {
		t.Fatal(err)
	}
	capture("news.source.updated", "newsSource", "PUT", prefix+"/sources/"+src.ID, fmt.Sprintf(`{"name":"Finance","kind":"rss","url":%q,"query":"","topic":"市场","enabled":true,"intervalMinutes":60}`, server.URL), 200)
	capture("news.fetch", "newsRefreshSource", "POST", prefix+"/sources/"+src.ID+"/refresh", "{}", 200)
	capture("news.fetch.cached", "newsRefreshSource", "POST", prefix+"/sources/"+src.ID+"/refresh", "{}", 200)
	capture("news.refresh", "newsRefreshAll", "POST", prefix+"/refresh", "{}", 200)
	capture("news.sources.populated", "newsSources", "GET", prefix+"/sources", "", 200)
	capture("news.settings", "newsSettings", "GET", prefix+"/settings", "", 200)
	capture("news.settings.saved", "newsSettings", "PUT", prefix+"/settings", `{"autoAI":false,"targetLanguage":"简体中文","dailyAILimit":10,"retentionDays":30,"digestEnabled":false,"digestHour":8,"digestMinute":30,"timeZone":"UTC","digestMaxItems":10,"digestTopic":"","pushEnabled":false}`, 200)
	capture("news.rules.empty", "newsRules", "GET", prefix+"/rules", "", 200)
	capture("news.rule", "newsRule", "POST", prefix+"/rules", fmt.Sprintf(`{"name":"财报","includeKeywords":["earnings"],"excludeKeywords":["广告"],"sourceIds":[%q],"topic":"财报","enabled":true}`, src.ID), 201)
	capture("news.rules.populated", "newsRules", "GET", prefix+"/rules", "", 200)
	raw := capture("news.articles.page", "newsArticles", "GET", prefix+"/articles?limit=1", "", 200)
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Next string `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &page); err != nil || len(page.Items) != 1 {
		t.Fatal("news page fixture", err)
	}
	capture("news.articles.next", "newsArticles", "GET", prefix+"/articles?limit=1&cursor="+page.Next, "", 200)
	raw = capture("news.articles.matched", "newsArticles", "GET", prefix+"/articles?matched=true", "", 200)
	if err := json.Unmarshal(raw, &page); err != nil || len(page.Items) != 1 {
		t.Fatal("matched fixture", err)
	}
	id := page.Items[0].ID
	raw = capture("news.articles.search", "newsArticles", "GET", prefix+"/articles?q=company%20EARNINGS", "", 200)
	if err := json.Unmarshal(raw, &page); err != nil || len(page.Items) != 1 {
		t.Fatal("search must ignore case", err, string(raw))
	}
	raw = capture("news.articles.literal.search", "newsArticles", "GET", prefix+"/articles?q=10%25", "", 200)
	if err := json.Unmarshal(raw, &page); err != nil || len(page.Items) != 1 {
		t.Fatal("search percent must be literal", err, string(raw))
	}
	capture("news.article", "newsArticle", "GET", prefix+"/articles/"+id, "", 200)
	capture("news.article.read", "newsArticle", "PATCH", prefix+"/articles/"+id, `{"read":true,"starred":true}`, 200)
	capture("news.article.analyzed", "newsArticle", "POST", prefix+"/articles/"+id+"/analyze", "{}", 200)
	capture("news.digests.empty", "newsDigests", "GET", prefix+"/digests", "", 200)
	raw = capture("news.digest.created", "newsDigest", "POST", prefix+"/digests", `{"topic":"财报","periodHours":24,"maxItems":5,"push":false}`, 201)
	var d struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	capture("news.digest.pushed", "newsDigest", "POST", prefix+"/digests/"+d.ID+"/push", "{}", 200)
	capture("news.digest.pushed.again", "newsDigest", "POST", prefix+"/digests/"+d.ID+"/push", "{}", 200)
	capture("news.digests.populated", "newsDigests", "GET", prefix+"/digests", "", 200)
	capture("news.stats", "newsStats", "GET", prefix+"/stats", "", 200)
	capture("news.brief.populated", "newsBrief", "GET", prefix+"/brief?symbols=AAPL,NVDA", "", 200)
	capture("news.brief.symbols", "newsBrief", "GET", prefix+"/brief?symbols=BRK%2FB,BRK-B,%24SPX,%2FES", "", 200)
	capture("news.brief.invalid", "error", "GET", prefix+"/brief?symbols=bad%20symbol", "", 400)
	capture("news.invalid.source", "error", "POST", prefix+"/sources", `{"name":"Bad","kind":"rss","url":"file:///tmp/data","enabled":true,"intervalMinutes":30}`, 400)
	capture("news.invalid.cursor", "error", "GET", prefix+"/articles?cursor=bad", "", 400)
}
