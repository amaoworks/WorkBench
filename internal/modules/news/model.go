package news

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"time"

	"workbench/internal/modules/news/sqlc"
)

type SourceInput struct {
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	URL             string `json:"url"`
	Query           string `json:"query"`
	Topic           string `json:"topic"`
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int64  `json:"intervalMinutes"`
}
type Source struct {
	ID string `json:"id"`
	SourceInput
	LastFetchedAt *time.Time `json:"lastFetchedAt,omitempty"`
	NextFetchAt   *time.Time `json:"nextFetchAt,omitempty"`
	LastError     string     `json:"lastError"`
	ItemCount     int64      `json:"itemCount"`
}

func sourceDTO(v newssqlc.NewsSource) Source {
	return Source{ID: v.ID, SourceInput: SourceInput{Name: v.Name, Kind: v.Kind, URL: v.Url, Query: v.Query, Topic: v.Topic, Enabled: v.Enabled == 1, IntervalMinutes: v.IntervalMinutes}, LastFetchedAt: optionalTime(v.FetchedAt), NextFetchAt: optionalTime(v.NextFetchAt), LastError: v.LastError, ItemCount: v.ItemCount}
}

type RuleInput struct {
	Name            string   `json:"name"`
	IncludeKeywords []string `json:"includeKeywords"`
	ExcludeKeywords []string `json:"excludeKeywords"`
	SourceIDs       []string `json:"sourceIds"`
	Topic           string   `json:"topic"`
	Enabled         bool     `json:"enabled"`
}
type Rule struct {
	ID string `json:"id"`
	RuleInput
}

func ruleDTO(v newssqlc.NewsRule) (Rule, error) {
	r := Rule{ID: v.ID, RuleInput: RuleInput{Name: v.Name, Topic: v.Topic, Enabled: v.Enabled == 1, IncludeKeywords: []string{}, ExcludeKeywords: []string{}, SourceIDs: []string{}}}
	for _, field := range []struct {
		raw    string
		target *[]string
	}{{v.IncludeJson, &r.IncludeKeywords}, {v.ExcludeJson, &r.ExcludeKeywords}, {v.SourcesJson, &r.SourceIDs}} {
		if err := json.Unmarshal([]byte(field.raw), field.target); err != nil {
			return r, err
		}
	}
	return r, nil
}
func rulesDTO(rows []newssqlc.NewsRule) ([]Rule, error) {
	out := make([]Rule, 0, len(rows))
	for _, v := range rows {
		r, err := ruleDTO(v)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

type Origin struct {
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
	URL        string `json:"url"`
}
type Article struct {
	ID                string    `json:"id"`
	Title             string    `json:"title"`
	URL               string    `json:"url"`
	SourceName        string    `json:"sourceName"`
	Topic             string    `json:"topic"`
	Content           string    `json:"content"`
	Language          string    `json:"language"`
	PublishedAt       time.Time `json:"publishedAt"`
	CreatedAt         time.Time `json:"createdAt"`
	Read              bool      `json:"read"`
	Starred           bool      `json:"starred"`
	Matched           bool      `json:"matched"`
	TranslatedTitle   string    `json:"translatedTitle"`
	TranslatedContent string    `json:"translatedContent"`
	Summary           string    `json:"summary"`
	AIStatus          string    `json:"aiStatus"`
	AIError           string    `json:"aiError"`
	Origins           []Origin  `json:"origins"`
}

func (m *Module) articleDTO(ctx context.Context, v newssqlc.NewsArticle) (Article, error) {
	a := Article{ID: v.ID, Title: v.Title, URL: v.Url, SourceName: v.SourceName, Topic: v.Topic, Content: v.Content, Language: v.Language, PublishedAt: time.UnixMilli(v.PublishedAt).UTC(), CreatedAt: time.UnixMilli(v.CreatedAt).UTC(), Read: v.IsRead == 1, Starred: v.Starred == 1, Matched: v.Matched == 1, TranslatedTitle: v.TranslatedTitle, TranslatedContent: v.TranslatedContent, Summary: v.Summary, AIStatus: v.AiStatus, AIError: v.AiError, Origins: []Origin{}}
	origins, err := m.q.ListOrigins(ctx, v.ID)
	if err != nil {
		return a, err
	}
	for _, o := range origins {
		a.Origins = append(a.Origins, Origin{SourceID: o.SourceID, SourceName: o.SourceName, URL: o.Url})
	}
	return a, nil
}
func (m *Module) Article(ctx context.Context, id string) (Article, error) {
	v, err := m.q.GetArticle(ctx, id)
	if err != nil {
		return Article{}, err
	}
	return m.articleDTO(ctx, v)
}

type DigestItem struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	SourceName string `json:"sourceName"`
}
type Digest struct {
	ID             string       `json:"id"`
	Title          string       `json:"title"`
	Topic          string       `json:"topic"`
	Content        string       `json:"content"`
	Items          []DigestItem `json:"items"`
	CreatedAt      time.Time    `json:"createdAt"`
	AIUsed         bool         `json:"aiUsed"`
	NotificationID string       `json:"notificationId,omitempty"`
}

func digestDTO(v newssqlc.NewsDigest) (Digest, error) {
	d := Digest{ID: v.ID, Title: v.Title, Topic: v.Topic, Content: v.Content, CreatedAt: time.UnixMilli(v.CreatedAt).UTC(), AIUsed: v.AiUsed == 1, NotificationID: v.NotificationID, Items: []DigestItem{}}
	err := json.Unmarshal([]byte(v.ItemsJson), &d.Items)
	return d, err
}

type pageCursor struct {
	Time int64  `json:"time"`
	ID   string `json:"id"`
}

func decodeCursor(raw string) (pageCursor, error) {
	c := pageCursor{Time: math.MaxInt64, ID: "\uffff"}
	if raw == "" {
		return c, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return c, errors.New("分页参数无效")
	}
	if json.Unmarshal(b, &c) != nil || c.ID == "" || c.Time <= 0 {
		return c, errors.New("分页参数无效")
	}
	return c, nil
}
func encodeCursor(t int64, id string) string {
	raw, _ := json.Marshal(pageCursor{t, id})
	return base64.RawURLEncoding.EncodeToString(raw)
}
