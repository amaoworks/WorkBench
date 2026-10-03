package news

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"workbench/internal/foundation/identity"
	"workbench/internal/modules/news/sqlc"
)

func (m *Module) Rules(ctx context.Context) ([]Rule, error) {
	rows, err := m.q.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	return rulesDTO(rows)
}
func cleanList(items []string, max int) ([]string, error) {
	if len(items) > max {
		return nil, errors.New("规则项目过多")
	}
	out := []string{}
	for _, s := range items {
		s = strings.TrimSpace(s)
		if len(s) > 200 {
			return nil, errors.New("规则关键词过长")
		}
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out, nil
}
func (m *Module) SaveRule(ctx context.Context, id string, in RuleInput) (Rule, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Topic = strings.TrimSpace(in.Topic)
	if in.Name == "" || len(in.Name) > 200 || len(in.Topic) > 120 {
		return Rule{}, errors.New("规则名称或主题无效")
	}
	var err error
	in.IncludeKeywords, err = cleanList(in.IncludeKeywords, 100)
	if err != nil {
		return Rule{}, err
	}
	in.ExcludeKeywords, err = cleanList(in.ExcludeKeywords, 100)
	if err != nil {
		return Rule{}, err
	}
	in.SourceIDs, err = cleanList(in.SourceIDs, 100)
	if err != nil {
		return Rule{}, err
	}
	if id == "" {
		id, err = identity.New()
		if err != nil {
			return Rule{}, err
		}
	} else if _, err = m.q.GetRule(ctx, id); err != nil {
		return Rule{}, err
	}
	a, _ := json.Marshal(in.IncludeKeywords)
	b, _ := json.Marshal(in.ExcludeKeywords)
	c, _ := json.Marshal(in.SourceIDs)
	err = m.mutate(ctx, "news.rule.changed", id, func(q *newssqlc.Queries) error {
		for _, src := range in.SourceIDs {
			if _, e := q.GetSource(ctx, src); e != nil {
				return errors.New("规则包含不存在的来源")
			}
		}
		rows, e := q.ListRules(ctx)
		if e != nil {
			return e
		}
		if len(rows) >= 100 {
			if _, e = q.GetRule(ctx, id); errors.Is(e, sql.ErrNoRows) {
				return errors.New("最多添加 100 条关注规则")
			}
		}
		if e = q.SaveRule(ctx, newssqlc.SaveRuleParams{ID: id, Name: in.Name, IncludeJson: string(a), ExcludeJson: string(b), SourcesJson: string(c), Topic: in.Topic, Enabled: bit(in.Enabled)}); e != nil {
			return e
		}
		return m.rematch(ctx, q)
	})
	return Rule{ID: id, RuleInput: in}, err
}
func (m *Module) DeleteRule(ctx context.Context, id string) error {
	return m.mutate(ctx, "news.rule.deleted", id, func(q *newssqlc.Queries) error {
		n, err := q.DeleteRule(ctx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return sql.ErrNoRows
		}
		return m.rematch(ctx, q)
	})
}
func matchArticle(title, content string, sourceIDs []string, baseTopic string, rules []Rule) (bool, string) {
	text := strings.ToLower(title + " " + content)
	active := 0
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		active++
		if len(r.SourceIDs) > 0 && !slices.ContainsFunc(r.SourceIDs, func(id string) bool { return slices.Contains(sourceIDs, id) }) {
			continue
		}
		if slices.ContainsFunc(r.ExcludeKeywords, func(word string) bool { return strings.Contains(text, strings.ToLower(word)) }) {
			continue
		}
		if len(r.IncludeKeywords) > 0 && !slices.ContainsFunc(r.IncludeKeywords, func(word string) bool { return strings.Contains(text, strings.ToLower(word)) }) {
			continue
		}
		topic := baseTopic
		if r.Topic != "" {
			topic = r.Topic
		}
		return true, topic
	}
	return active == 0, baseTopic
}
func (m *Module) rematch(ctx context.Context, q *newssqlc.Queries) error {
	rows, err := q.ListRules(ctx)
	if err != nil {
		return err
	}
	rules, err := rulesDTO(rows)
	if err != nil {
		return err
	}
	sources, err := q.ListSources(ctx)
	if err != nil {
		return err
	}
	topics := map[string]string{}
	for _, s := range sources {
		topics[s.ID] = s.Topic
	}
	articles, err := q.ListAllArticles(ctx)
	if err != nil {
		return err
	}
	for _, a := range articles {
		origins, err := q.ListOrigins(ctx, a.ID)
		if err != nil {
			return err
		}
		ids := []string{}
		topic := ""
		for _, o := range origins {
			ids = append(ids, o.SourceID)
			if topic == "" {
				topic = topics[o.SourceID]
			}
		}
		matched, topic := matchArticle(a.Title, a.Content, ids, topic, rules)
		if err = q.SetArticleMatch(ctx, newssqlc.SetArticleMatchParams{ID: a.ID, Topic: topic, Matched: bit(matched)}); err != nil {
			return err
		}
	}
	return nil
}
