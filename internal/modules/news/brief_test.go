package news

import (
	"context"
	"net/http"
	"slices"
	"testing"
)

func TestParseBriefSymbols(t *testing.T) {
	got, err := parseBriefSymbols(" aapl,NVDA,,aapl,BRK.B,BRK/B,BRK-B,$SPX,/ES,ABCDEFGHIJKLMNO ")
	want := []string{"AAPL", "NVDA", "BRK.B", "BRK/B", "BRK-B", "$SPX", "/ES", "ABCDEFGHIJKLMNO"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("symbols %v %v", got, err)
	}
	for _, bad := range []string{"bad symbol", "A;B", "1ABC", "ABCDEFGHIJKLMNOP"} {
		if _, err := parseBriefSymbols(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestBriefHidesWithoutSourcesAndFindsHoldingMentions(t *testing.T) {
	m, _, _ := openModule(t)
	ctx := context.Background()
	empty, err := m.Brief(ctx, []string{"AAPL"})
	if err != nil || empty.Sources != 0 || len(empty.Latest) != 0 || empty.Digest != nil {
		t.Fatalf("empty brief %+v %v", empty, err)
	}
	s := addSource(t, m, "feed")
	m.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body := `<rss version="2.0"><channel>` +
			item(m, "g1", "https://media.example/1", "Apple (AAPL) beats estimates", "Strong iPhone sales.") +
			item(m, "g2", "https://media.example/2", "Markets turn on rate news", "Stocks moved on the Fed decision.") +
			item(m, "g3", "https://media.example/3", "Chip rally", "Investors bought $NVDA shares.") +
			`</channel></rss>`
		return response(r, 200, body), nil
	})}
	if _, err := m.RefreshSource(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	brief, err := m.Brief(ctx, []string{"AAPL", "NVDA", "ON"})
	if err != nil || brief.Sources != 1 || len(brief.Latest) != 3 || brief.Unread != 3 {
		t.Fatalf("brief %+v %v", brief, err)
	}
	if len(brief.Related) != 2 {
		t.Fatalf("related %+v", brief.Related)
	}
	for _, article := range brief.Related {
		if len(article.Symbols) != 1 || article.Symbols[0] == "ON" {
			t.Fatalf("lower-case words must not match tickers: %+v", article)
		}
	}
}

func item(m *Module, guid, link, title, body string) string {
	return `<item><guid>` + guid + `</guid><title>` + title + `</title><link>` + link + `</link><pubDate>` +
		m.now().Format(http.TimeFormat) + `</pubDate><description><![CDATA[` + body + `]]></description></item>`
}
