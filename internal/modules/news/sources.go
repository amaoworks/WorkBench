package news

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"

	"workbench/internal/foundation/identity"
	"workbench/internal/modules/news/sqlc"
)

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || len(raw) > 4096 {
		return errors.New("请输入 HTTP 或 HTTPS 订阅地址")
	}
	return nil
}
func normalizeSource(in SourceInput) (SourceInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Query = strings.TrimSpace(in.Query)
	in.Topic = strings.TrimSpace(in.Topic)
	in.URL = strings.TrimSpace(in.URL)
	if in.Name == "" || len(in.Name) > 200 || len(in.Topic) > 120 || len(in.Query) > 500 || in.IntervalMinutes < 5 || in.IntervalMinutes > 10080 {
		return in, errors.New("来源名称、主题或抓取间隔无效")
	}
	switch in.Kind {
	case "google":
		if in.Query == "" {
			return in, errors.New("请输入 Google News 搜索词")
		}
		in.URL = "https://news.google.com/rss/search?" + url.Values{"q": {in.Query}, "hl": {"en-US"}, "gl": {"US"}, "ceid": {"US:en"}}.Encode()
	case "yahoo":
		in.URL = "https://finance.yahoo.com/news/rssindex"
		in.Query = ""
	case "rss", "rsshub":
		in.Query = ""
	default:
		return in, errors.New("来源类型无效")
	}
	return in, validateURL(in.URL)
}
func (m *Module) Sources(ctx context.Context) ([]Source, error) {
	rows, err := m.q.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Source, 0, len(rows))
	for _, v := range rows {
		out = append(out, sourceDTO(v))
	}
	return out, nil
}
func (m *Module) SaveSource(ctx context.Context, id string, in SourceInput) (Source, error) {
	in, err := normalizeSource(in)
	if err != nil {
		return Source{}, err
	}
	m.sourceMu.Lock()
	defer m.sourceMu.Unlock()
	if id == "" {
		count, e := m.q.CountSources(ctx)
		if e != nil {
			return Source{}, e
		}
		if count >= 100 {
			return Source{}, errors.New("最多添加 100 个资讯源")
		}
		id, err = identity.New()
		if err != nil {
			return Source{}, err
		}
	} else if _, err = m.q.GetSource(ctx, id); err != nil {
		return Source{}, err
	}
	err = m.mutate(ctx, "news.source.changed", id, func(q *newssqlc.Queries) error {
		if err := q.SaveSource(ctx, newssqlc.SaveSourceParams{ID: id, Name: in.Name, Kind: in.Kind, Url: in.URL, Query: in.Query, Topic: in.Topic, Enabled: bit(in.Enabled), IntervalMinutes: in.IntervalMinutes}); err != nil {
			return err
		}
		return m.rematch(ctx, q)
	})
	if err != nil {
		return Source{}, err
	}
	row, err := m.q.GetSource(ctx, id)
	return sourceDTO(row), err
}
func (m *Module) DeleteSource(ctx context.Context, id string) error {
	m.sourceMu.Lock()
	defer m.sourceMu.Unlock()
	return m.mutate(ctx, "news.source.deleted", id, func(q *newssqlc.Queries) error {
		n, err := q.DeleteSource(ctx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return sql.ErrNoRows
		}
		return m.rematch(ctx, q)
	})
}

func canonicalURL(raw string) string {
	if validateURL(raw) != nil {
		return ""
	}
	u, _ := url.Parse(raw)
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	p := u.Query()
	for k := range p {
		low := strings.ToLower(k)
		if strings.HasPrefix(low, "utm_") || low == "fbclid" || low == "gclid" {
			p.Del(k)
		}
	}
	u.RawQuery = p.Encode()
	return u.String()
}
