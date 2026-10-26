package news

import (
	"context"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"workbench/internal/foundation/httpapi"
	newssqlc "workbench/internal/modules/news/sqlc"
)

const (
	briefLatest     = 5
	briefRelated    = 4
	briefScan       = 200
	briefMaxSymbols = 20
	briefDigestAge  = 36 * time.Hour
)

var briefSymbolPattern = regexp.MustCompile(`^[A-Z$/][A-Z0-9./-]{0,14}$`)

type BriefArticle struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	SourceName  string    `json:"sourceName"`
	Summary     string    `json:"summary,omitempty"`
	PublishedAt time.Time `json:"publishedAt"`
	Read        bool      `json:"read"`
	Matched     bool      `json:"matched"`
	Symbols     []string  `json:"symbols,omitempty"`
}

// Brief is the overview card: the latest digest, the newest articles and
// articles that mention the requested ticker symbols.
type Brief struct {
	Sources int64          `json:"sources"`
	Unread  int64          `json:"unread"`
	Digest  *Digest        `json:"digest,omitempty"`
	Latest  []BriefArticle `json:"latest"`
	Related []BriefArticle `json:"related"`
}

func briefArticle(v newssqlc.NewsArticle) BriefArticle {
	title := v.TranslatedTitle
	if title == "" {
		title = v.Title
	}
	return BriefArticle{ID: v.ID, Title: title, URL: v.Url, SourceName: v.SourceName, Summary: clip(v.Summary, 240),
		PublishedAt: time.UnixMilli(v.PublishedAt).UTC(), Read: v.IsRead == 1, Matched: v.Matched == 1}
}

func parseBriefSymbols(raw string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		symbol := strings.ToUpper(strings.TrimSpace(part))
		if symbol == "" || seen[symbol] {
			continue
		}
		if !briefSymbolPattern.MatchString(symbol) || len(out) >= briefMaxSymbols {
			return nil, errors.New("股票代码格式无效或数量超过 20 个")
		}
		seen[symbol] = true
		out = append(out, symbol)
	}
	return out, nil
}

// mentions reports which symbols appear as whole words, case-sensitive, so
// short tickers such as "ON" do not match ordinary words in lower case text.
func mentions(text string, patterns map[string]*regexp.Regexp) []string {
	var found []string
	for symbol, pattern := range patterns {
		if pattern.MatchString(text) {
			found = append(found, symbol)
		}
	}
	return found
}

func (m *Module) Brief(ctx context.Context, symbols []string) (Brief, error) {
	sources, err := m.q.CountSources(ctx)
	if err != nil {
		return Brief{}, err
	}
	out := Brief{Sources: sources, Latest: []BriefArticle{}, Related: []BriefArticle{}}
	if sources == 0 {
		return out, nil
	}
	stats, err := m.q.NewsStats(ctx)
	if err != nil {
		return Brief{}, err
	}
	out.Unread = stats.Unread
	digests, err := m.q.ListDigestsPage(ctx, newssqlc.ListDigestsPageParams{CursorTime: math.MaxInt64, CursorID: "\uffff", PageLimit: 1})
	if err != nil {
		return Brief{}, err
	}
	if len(digests) == 1 && m.now().Sub(time.UnixMilli(digests[0].CreatedAt)) < briefDigestAge {
		if digest, err := digestDTO(digests[0]); err == nil {
			if len(digest.Items) > briefLatest {
				digest.Items = digest.Items[:briefLatest]
			}
			digest.Content = clip(digest.Content, 600)
			out.Digest = &digest
		}
	}
	limit := briefLatest
	if len(symbols) > 0 {
		limit = briefScan
	}
	rows, err := m.q.ListArticlesPage(ctx, newssqlc.ListArticlesPageParams{CursorTime: math.MaxInt64, CursorID: "\uffff", PageLimit: int64(limit)})
	if err != nil {
		return Brief{}, err
	}
	patterns := map[string]*regexp.Regexp{}
	for _, symbol := range symbols {
		patterns[symbol] = regexp.MustCompile(`(^|[^A-Za-z0-9])\$?` + regexp.QuoteMeta(symbol) + `([^A-Za-z0-9]|$)`)
	}
	for _, row := range rows {
		if len(out.Latest) < briefLatest {
			out.Latest = append(out.Latest, briefArticle(row))
		}
		if len(patterns) == 0 || len(out.Related) >= briefRelated {
			continue
		}
		if found := mentions(row.Title+"\n"+row.TranslatedTitle+"\n"+clip(row.Content, 4000), patterns); len(found) > 0 {
			article := briefArticle(row)
			article.Symbols = found
			out.Related = append(out.Related, article)
		}
	}
	return out, nil
}

func (m *Module) briefHTTP(w http.ResponseWriter, r *http.Request) {
	symbols, err := parseBriefSymbols(r.URL.Query().Get("symbols"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_symbols", err.Error())
		return
	}
	out, err := m.Brief(r.Context(), symbols)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, out)
}
