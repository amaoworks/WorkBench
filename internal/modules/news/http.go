package news

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
	"workbench/internal/modules/news/sqlc"
)

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, sql.ErrNoRows) {
		status = http.StatusNotFound
		err = errors.New("资讯记录不存在")
	}
	if errors.Is(err, contracts.ErrAIUnavailable) {
		status = http.StatusServiceUnavailable
		err = errors.New("AI 未启用")
	}
	if errors.Is(err, ErrAILimit) {
		status = http.StatusTooManyRequests
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusRequestTimeout
		err = errors.New("资讯操作已取消或超时")
	}
	httpapi.Error(w, status, "news_operation_failed", err.Error())
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if httpapi.Decode(w, r, target, 64*1024) != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "资讯请求格式无效")
		return false
	}
	return true
}
func (m *Module) listSourcesHTTP(w http.ResponseWriter, r *http.Request) {
	v, e := m.Sources(r.Context())
	if e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 200, map[string]any{"items": v})
}
func (m *Module) saveSourceHTTP(w http.ResponseWriter, r *http.Request) {
	var in SourceInput
	if !decode(w, r, &in) {
		return
	}
	v, e := m.SaveSource(r.Context(), r.PathValue("id"), in)
	if e != nil {
		writeError(w, e)
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	httpapi.Write(w, status, v)
}
func (m *Module) deleteSourceHTTP(w http.ResponseWriter, r *http.Request) {
	if e := m.DeleteSource(r.Context(), r.PathValue("id")); e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 204, nil)
}
func (m *Module) refreshSourceHTTP(w http.ResponseWriter, r *http.Request) {
	v, e := m.RefreshSource(r.Context(), r.PathValue("id"))
	if e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 200, v)
}
func (m *Module) refreshHTTP(w http.ResponseWriter, r *http.Request) {
	v, e := m.Refresh(r.Context(), false)
	if e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 200, v)
}
func (m *Module) listRulesHTTP(w http.ResponseWriter, r *http.Request) {
	v, e := m.Rules(r.Context())
	if e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 200, map[string]any{"items": v})
}
func (m *Module) saveRuleHTTP(w http.ResponseWriter, r *http.Request) {
	var in RuleInput
	if !decode(w, r, &in) {
		return
	}
	v, e := m.SaveRule(r.Context(), r.PathValue("id"), in)
	if e != nil {
		writeError(w, e)
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	httpapi.Write(w, status, v)
}
func (m *Module) deleteRuleHTTP(w http.ResponseWriter, r *http.Request) {
	if e := m.DeleteRule(r.Context(), r.PathValue("id")); e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 204, nil)
}
func (m *Module) settingsHTTP(w http.ResponseWriter, r *http.Request) {
	v, e := m.Settings(r.Context())
	if e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 200, v)
}
func (m *Module) saveSettingsHTTP(w http.ResponseWriter, r *http.Request) {
	var in Settings
	if !decode(w, r, &in) {
		return
	}
	v, e := m.SaveSettings(r.Context(), in)
	if e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 200, v)
}
func (m *Module) statsHTTP(w http.ResponseWriter, r *http.Request) {
	v, e := m.Stats(r.Context())
	if e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 200, v)
}
func (m *Module) articleHTTP(w http.ResponseWriter, r *http.Request) {
	v, e := m.Article(r.Context(), r.PathValue("id"))
	if e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 200, v)
}
func (m *Module) analyzeHTTP(w http.ResponseWriter, r *http.Request) {
	v, e := m.Analyze(r.Context(), r.PathValue("id"))
	if e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 200, v)
}
func (m *Module) updateArticleHTTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Read    *bool `json:"read"`
		Starred *bool `json:"starred"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Read == nil && in.Starred == nil {
		writeError(w, errors.New("请选择已读或收藏状态"))
		return
	}
	id := r.PathValue("id")
	err := m.mutate(r.Context(), "news.article.changed", id, func(q *newssqlc.Queries) error {
		if _, err := q.GetArticle(r.Context(), id); err != nil {
			return err
		}
		if in.Read != nil {
			if err := q.SetArticleRead(r.Context(), newssqlc.SetArticleReadParams{ID: id, IsRead: bit(*in.Read)}); err != nil {
				return err
			}
		}
		if in.Starred != nil {
			if err := q.SetArticleStarred(r.Context(), newssqlc.SetArticleStarredParams{ID: id, Starred: bit(*in.Starred)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	m.articleHTTP(w, r)
}
func pageParams(r *http.Request, def int) (pageCursor, int, error) {
	c, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		return c, 0, err
	}
	n := def
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err = strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return c, 0, errors.New("分页条数应为 1 至 100")
		}
	}
	return c, n, nil
}
func (m *Module) listArticlesHTTP(w http.ResponseWriter, r *http.Request) {
	c, n, e := pageParams(r, 30)
	if e != nil {
		writeError(w, e)
		return
	}
	query := r.URL.Query()
	search := strings.TrimSpace(query.Get("q"))
	if len(search) > 500 {
		writeError(w, errors.New("搜索词过长"))
		return
	}
	rows, e := m.q.ListArticlesPage(r.Context(), newssqlc.ListArticlesPageParams{Search: search, Pattern: escapedPattern(search), SourceID: query.Get("sourceId"), Topic: query.Get("topic"), Unread: bit(query.Get("unread") == "true"), Starred: bit(query.Get("starred") == "true"), Matched: bit(query.Get("matched") == "true"), CursorTime: c.Time, CursorID: c.ID, PageLimit: int64(n + 1)})
	if e != nil {
		writeError(w, e)
		return
	}
	out := struct {
		Items      []Article `json:"items"`
		NextCursor string    `json:"nextCursor,omitempty"`
	}{Items: []Article{}}
	if len(rows) > n {
		rows = rows[:n]
		last := rows[n-1]
		out.NextCursor = encodeCursor(last.PublishedAt, last.ID)
	}
	for _, v := range rows {
		a, e := m.articleDTO(r.Context(), v)
		if e != nil {
			writeError(w, e)
			return
		}
		out.Items = append(out.Items, a)
	}
	httpapi.Write(w, 200, out)
}
func (m *Module) listDigestsHTTP(w http.ResponseWriter, r *http.Request) {
	c, n, e := pageParams(r, 20)
	if e != nil {
		writeError(w, e)
		return
	}
	rows, e := m.q.ListDigestsPage(r.Context(), newssqlc.ListDigestsPageParams{CursorTime: c.Time, CursorID: c.ID, PageLimit: int64(n + 1)})
	if e != nil {
		writeError(w, e)
		return
	}
	out := struct {
		Items      []Digest `json:"items"`
		NextCursor string   `json:"nextCursor,omitempty"`
	}{Items: []Digest{}}
	if len(rows) > n {
		rows = rows[:n]
		last := rows[n-1]
		out.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	for _, v := range rows {
		d, e := digestDTO(v)
		if e != nil {
			writeError(w, e)
			return
		}
		out.Items = append(out.Items, d)
	}
	httpapi.Write(w, 200, out)
}
func (m *Module) createDigestHTTP(w http.ResponseWriter, r *http.Request) {
	var in DigestInput
	if !decode(w, r, &in) {
		return
	}
	v, e := m.CreateDigest(r.Context(), in, "")
	if e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 201, v)
}
func (m *Module) pushDigestHTTP(w http.ResponseWriter, r *http.Request) {
	v, e := m.PushDigest(r.Context(), r.PathValue("id"))
	if e != nil {
		writeError(w, e)
		return
	}
	httpapi.Write(w, 200, v)
}
