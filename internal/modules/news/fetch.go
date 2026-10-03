package news

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/modules/news/sqlc"
)

type FetchResult struct {
	Fetched     int  `json:"fetched"`
	Added       int  `json:"added"`
	NotModified bool `json:"notModified"`
}
type RefreshResult struct {
	Fetched int `json:"fetched"`
	Added   int `json:"added"`
	Failed  int `json:"failed"`
}

func hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func (m *Module) RefreshSource(ctx context.Context, id string) (FetchResult, error) {
	ctx, cancel := m.operation(ctx)
	defer cancel()
	m.fetchMu.Lock()
	defer m.fetchMu.Unlock()
	src, err := m.q.GetSource(ctx, id)
	if err != nil {
		return FetchResult{}, err
	}
	if src.Enabled != 1 {
		return FetchResult{}, errors.New("来源已停用")
	}
	return m.fetch(ctx, src)
}
func (m *Module) Refresh(ctx context.Context, dueOnly bool) (RefreshResult, error) {
	ctx, cancel := m.operation(ctx)
	defer cancel()
	m.fetchMu.Lock()
	defer m.fetchMu.Unlock()
	rows, err := m.q.ListSources(ctx)
	if err != nil {
		return RefreshResult{}, err
	}
	result := RefreshResult{}
	for _, src := range rows {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if src.Enabled != 1 || (dueOnly && src.NextFetchAt > m.now().UnixMilli()) {
			continue
		}
		r, e := m.fetch(ctx, src)
		if e != nil {
			result.Failed++
		} else {
			result.Fetched += r.Fetched
			result.Added += r.Added
		}
	}
	return result, nil
}
func (m *Module) fetch(ctx context.Context, src newssqlc.NewsSource) (FetchResult, error) {
	now := m.now().UTC()
	result := FetchResult{}
	if err := validateURL(src.Url); err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, src.Url, nil)
	if err != nil {
		return result, err
	}
	request.Header.Set("User-Agent", "Workbench/0.1 RSS Reader")
	request.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml")
	if src.Etag != "" {
		request.Header.Set("If-None-Match", src.Etag)
	}
	if src.LastModified != "" {
		request.Header.Set("If-Modified-Since", src.LastModified)
	}
	response, err := m.client.Do(request)
	retryAfter := time.Duration(0)
	if err == nil && response.StatusCode == http.StatusNotModified {
		response.Body.Close()
		result.NotModified = true
		err = m.q.RecordFetch(ctx, newssqlc.RecordFetchParams{ID: src.ID, FetchedAt: now.UnixMilli(), NextFetchAt: now.Add(time.Duration(src.IntervalMinutes) * time.Minute).UnixMilli(), Etag: src.Etag, LastModified: src.LastModified, ItemCount: src.ItemCount})
		return result, err
	}
	var items []feedItem
	if err == nil {
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			err = fmt.Errorf("来源返回 HTTP %d", response.StatusCode)
			if seconds, e := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 64); e == nil && seconds > 0 {
				retryAfter = time.Duration(min(seconds, 86400)) * time.Second
			} else if at, e := http.ParseTime(response.Header.Get("Retry-After")); e == nil {
				retryAfter = min(at.Sub(now), 24*time.Hour)
			}
		} else {
			raw, e := io.ReadAll(io.LimitReader(response.Body, 5*1024*1024+1))
			err = e
			if err == nil && len(raw) > 5*1024*1024 {
				err = errors.New("订阅内容超过 5 MB")
			}
			if err == nil {
				items, err = parseFeed(raw, response.Request.URL.String(), now)
			}
		}
	} else {
		err = errors.New("无法连接资讯源")
	}
	if err != nil {
		delay := time.Duration(src.IntervalMinutes) * time.Minute * time.Duration(1<<min(src.FailureCount+1, 5))
		delay = min(delay, 24*time.Hour)
		delay = max(delay, retryAfter)
		if recordErr := m.q.RecordFetchFailure(ctx, newssqlc.RecordFetchFailureParams{ID: src.ID, NextFetchAt: now.Add(delay).UnixMilli(), LastError: err.Error()}); recordErr != nil {
			return result, recordErr
		}
		return result, err
	}
	settings, err := m.Settings(ctx)
	if err != nil {
		return result, err
	}
	err = m.mutate(ctx, "news.articles.ingested", src.ID, func(q *newssqlc.Queries) error {
		// The source lock prevents configuration changes during a network fetch.
		rulesRows, e := q.ListRules(ctx)
		if e != nil {
			return e
		}
		rules, e := rulesDTO(rulesRows)
		if e != nil {
			return e
		}
		for _, item := range items {
			if item.Title == "" || item.PublishedAt.Before(now.Add(-time.Duration(settings.RetentionDays)*24*time.Hour)) {
				continue
			}
			result.Fetched++
			key := item.GUID
			if key == "" {
				key = item.URL
			}
			if key == "" {
				key = hash(item.Title + "\n" + item.Content)
			}
			key = hash(key)
			id, e := q.GetOriginArticle(ctx, newssqlc.GetOriginArticleParams{SourceID: src.ID, ItemKey: key})
			known := e == nil
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if !known {
				if item.URL != "" {
					id = hash(item.URL)
					other, e := q.GetArticleByURL(ctx, item.URL)
					if e == nil {
						id = other.ID
					} else if !errors.Is(e, sql.ErrNoRows) {
						return e
					}
				} else {
					id = hash(src.ID + ":" + key)
				}
			}
			if known && item.URL != "" {
				other, e := q.GetArticleByURL(ctx, item.URL)
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return e
				}
				if e == nil && other.ID != id {
					previous, e := q.GetArticle(ctx, id)
					if e != nil {
						return e
					}
					if e = q.MergeArticleState(ctx, newssqlc.MergeArticleStateParams{ID: other.ID, IsRead: previous.IsRead, Starred: previous.Starred}); e != nil {
						return e
					}
					if e = q.MoveOrigins(ctx, newssqlc.MoveOriginsParams{Target: other.ID, Previous: id}); e != nil {
						return e
					}
					if e = q.DeleteArticle(ctx, id); e != nil {
						return e
					}
					id = other.ID
					known = false
				}
			}
			publisher := item.Publisher
			if publisher == "" {
				publisher = src.Name
			}
			matched, topic := matchArticle(item.Title, item.Content, []string{src.ID}, src.Topic, rules)
			n, e := q.InsertArticle(ctx, newssqlc.InsertArticleParams{ID: id, Title: item.Title, Url: item.URL, SourceName: publisher, Content: item.Content, Language: item.Language, Topic: topic, Matched: bit(matched), PublishedAt: item.PublishedAt.UnixMilli(), CreatedAt: now.UnixMilli()})
			if e != nil {
				return e
			}
			result.Added += int(n)
			if known {
				old, e := q.GetArticle(ctx, id)
				if e != nil {
					return e
				}
				if old.Title != item.Title || old.Content != item.Content || old.Url != item.URL {
					if e = q.UpdateArticleText(ctx, newssqlc.UpdateArticleTextParams{ID: id, Title: item.Title, Content: item.Content, Language: item.Language, Url: item.URL}); e != nil {
						return e
					}
				}
			}
			if e = q.SaveOrigin(ctx, newssqlc.SaveOriginParams{SourceID: src.ID, ItemKey: key, ArticleID: id, SourceName: src.Name, Url: item.URL}); e != nil {
				return e
			}
			article, e := q.GetArticle(ctx, id)
			if e != nil {
				return e
			}
			origins, e := q.ListOrigins(ctx, id)
			if e != nil {
				return e
			}
			ids := []string{}
			for _, o := range origins {
				ids = append(ids, o.SourceID)
			}
			matched, topic = matchArticle(article.Title, article.Content, ids, article.Topic, rules)
			if e = q.SetArticleMatch(ctx, newssqlc.SetArticleMatchParams{ID: id, Matched: bit(matched), Topic: topic}); e != nil {
				return e
			}
		}
		return q.RecordFetch(ctx, newssqlc.RecordFetchParams{ID: src.ID, FetchedAt: now.UnixMilli(), NextFetchAt: now.Add(time.Duration(src.IntervalMinutes) * time.Minute).UnixMilli(), Etag: response.Header.Get("ETag"), LastModified: response.Header.Get("Last-Modified"), ItemCount: int64(len(items))})
	})
	return result, err
}
func (m *Module) fetchJob(ctx context.Context, _ contracts.JobRun) error {
	_, err := m.Refresh(ctx, true)
	return err
}

func escapedPattern(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(s)
	return "%" + s + "%"
}
