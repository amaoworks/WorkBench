package news

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"workbench/internal/modules/news/sqlc"
)

type Settings struct {
	AutoAI         bool   `json:"autoAI"`
	TargetLanguage string `json:"targetLanguage"`
	DailyAILimit   int64  `json:"dailyAILimit"`
	RetentionDays  int64  `json:"retentionDays"`
	DigestEnabled  bool   `json:"digestEnabled"`
	DigestHour     int    `json:"digestHour"`
	DigestMinute   int    `json:"digestMinute"`
	TimeZone       string `json:"timeZone"`
	DigestMaxItems int64  `json:"digestMaxItems"`
	DigestTopic    string `json:"digestTopic"`
	PushEnabled    bool   `json:"pushEnabled"`
}

func defaults() Settings {
	return Settings{TargetLanguage: "简体中文", DailyAILimit: 50, RetentionDays: 90, DigestHour: 8, TimeZone: "Asia/Shanghai", DigestMaxItems: 10}
}
func settingsDTO(raw string) (Settings, error) {
	s := defaults()
	err := json.Unmarshal([]byte(raw), &s)
	return s, err
}
func (m *Module) Settings(ctx context.Context) (Settings, error) {
	v, err := m.q.GetSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	return settingsDTO(v.SettingsJson)
}
func (m *Module) SaveSettings(ctx context.Context, s Settings) (Settings, error) {
	s.TargetLanguage = strings.TrimSpace(s.TargetLanguage)
	s.TimeZone = strings.TrimSpace(s.TimeZone)
	s.DigestTopic = strings.TrimSpace(s.DigestTopic)
	if s.TargetLanguage == "" || len(s.TargetLanguage) > 80 || s.DailyAILimit < 0 || s.DailyAILimit > 1000 || s.RetentionDays < 1 || s.RetentionDays > 3650 || s.DigestHour < 0 || s.DigestHour > 23 || s.DigestMinute < 0 || s.DigestMinute > 59 || s.DigestMaxItems < 1 || s.DigestMaxItems > 30 || len(s.DigestTopic) > 120 {
		return s, errors.New("资讯设置无效")
	}
	if _, err := time.LoadLocation(s.TimeZone); err != nil {
		return s, errors.New("时区无效")
	}
	m.aiMu.Lock()
	defer m.aiMu.Unlock()
	raw, _ := json.Marshal(s)
	err := m.mutate(ctx, "news.settings.changed", "settings", func(q *newssqlc.Queries) error {
		old, err := q.GetSettings(ctx)
		if err != nil {
			return err
		}
		before, err := settingsDTO(old.SettingsJson)
		if err != nil {
			return err
		}
		if before.TargetLanguage != s.TargetLanguage {
			if err = q.ResetArticleAI(ctx); err != nil {
				return err
			}
		}
		return q.SaveSettings(ctx, string(raw))
	})
	return s, err
}

type Stats struct {
	Sources         int64      `json:"sources"`
	Articles        int64      `json:"articles"`
	Unread          int64      `json:"unread"`
	Starred         int64      `json:"starred"`
	AICallsToday    int64      `json:"aiCallsToday"`
	AILimit         int64      `json:"aiLimit"`
	LastDigestAt    *time.Time `json:"lastDigestAt,omitempty"`
	LastDigestError string     `json:"lastDigestError"`
}

func (m *Module) Stats(ctx context.Context) (Stats, error) {
	row, err := m.q.NewsStats(ctx)
	if err != nil {
		return Stats{}, err
	}
	sources, err := m.q.CountSources(ctx)
	if err != nil {
		return Stats{}, err
	}
	state, err := m.q.GetSettings(ctx)
	if err != nil {
		return Stats{}, err
	}
	s, err := settingsDTO(state.SettingsJson)
	if err != nil {
		return Stats{}, err
	}
	calls, err := m.q.GetAIUsage(ctx, m.usageDay(s))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Stats{}, err
	}
	return Stats{Sources: sources, Articles: row.Articles, Unread: row.Unread, Starred: row.Starred, AICallsToday: calls, AILimit: s.DailyAILimit, LastDigestAt: optionalTime(state.LastDigestAt), LastDigestError: state.LastDigestError}, nil
}
func (m *Module) usageDay(s Settings) string {
	loc, _ := time.LoadLocation(s.TimeZone)
	if loc == nil {
		loc = time.UTC
	}
	return m.now().In(loc).Format("2006-01-02")
}
