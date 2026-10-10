package news

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/modules/news/sqlc"
)

//go:embed migrations/*.sql migrations-postgres/*.sql
var migrations embed.FS

type Dependencies struct {
	DB            *sql.DB
	Events        contracts.EventPublisher
	Notifications contracts.NotificationService
	AI            contracts.TextGenerator
	HTTPClient    *http.Client
}

type Module struct {
	db            *sql.DB
	q             *newssqlc.Queries
	events        contracts.EventPublisher
	notifications contracts.NotificationService
	ai            contracts.TextGenerator
	client        *http.Client
	now           func() time.Time
	sourceMu      sync.Mutex
	settingsMu    sync.Mutex
	work          workLocks
	stateMu       sync.Mutex
	life          context.Context
	cancel        context.CancelFunc
}

func New(d Dependencies) (*Module, error) {
	if d.DB == nil || d.Events == nil || d.Notifications == nil || d.AI == nil {
		return nil, errors.New("news requires database, events, notifications and text generator")
	}
	client := d.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return validateURL(r.URL.String())
		}}
	}
	life, cancel := context.WithCancel(context.Background())
	return &Module{db: d.DB, q: newssqlc.New(d.DB), events: d.Events, notifications: d.Notifications, ai: d.AI, client: client, now: time.Now, life: life, cancel: cancel}, nil
}

func (*Module) Manifest() contracts.ModuleManifest {
	return contracts.ModuleManifest{ID: "news", Name: "资讯", Version: "0.1.0", ContractVersion: 1, Icon: "newspaper", Navigation: []contracts.NavigationItem{{Label: "资讯", Route: "/news", PageKey: "news.list", Order: 30}}}
}
func (*Module) Migrations() contracts.MigrationSet {
	return contracts.MigrationSet{FS: migrations, Dir: "migrations", PostgresDir: "migrations-postgres"}
}
func (m *Module) OnEnabledChanged(_ context.Context, enabled bool) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if enabled {
		if m.life.Err() != nil {
			m.life, m.cancel = context.WithCancel(context.Background())
		}
	} else {
		m.cancel()
	}
	return nil
}
func (m *Module) Close() { m.stateMu.Lock(); defer m.stateMu.Unlock(); m.cancel() }
func (m *Module) operation(ctx context.Context) (context.Context, context.CancelFunc) {
	m.stateMu.Lock()
	life := m.life
	m.stateMu.Unlock()
	c, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(life, cancel)
	if life.Err() != nil {
		cancel()
	}
	return c, func() { stop(); cancel() }
}
func (m *Module) Register(r contracts.ModuleRegistrar) error {
	routes := []struct {
		method, path string
		handler      http.HandlerFunc
	}{
		{"GET", "sources", m.listSourcesHTTP}, {"POST", "sources", m.saveSourceHTTP}, {"PUT", "sources/{id}", m.saveSourceHTTP}, {"DELETE", "sources/{id}", m.deleteSourceHTTP},
		{"POST", "sources/{id}/refresh", m.refreshSourceHTTP}, {"POST", "refresh", m.refreshHTTP},
		{"GET", "rules", m.listRulesHTTP}, {"POST", "rules", m.saveRuleHTTP}, {"PUT", "rules/{id}", m.saveRuleHTTP}, {"DELETE", "rules/{id}", m.deleteRuleHTTP},
		{"GET", "articles", m.listArticlesHTTP}, {"GET", "articles/{id}", m.articleHTTP}, {"PATCH", "articles/{id}", m.updateArticleHTTP}, {"POST", "articles/{id}/analyze", m.analyzeHTTP},
		{"GET", "settings", m.settingsHTTP}, {"PUT", "settings", m.saveSettingsHTTP}, {"GET", "stats", m.statsHTTP},
		{"GET", "digests", m.listDigestsHTTP}, {"POST", "digests", m.createDigestHTTP}, {"POST", "digests/{id}/push", m.pushDigestHTTP},
	}
	for _, route := range routes {
		handler := route.handler
		wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := m.operation(r.Context())
			defer cancel()
			handler(w, r.WithContext(ctx))
		})
		if err := r.Handle(route.method, "/api/modules/news/"+route.path, wrapped); err != nil {
			return err
		}
	}
	// The overview card reads the existing stats route; the frontend adds the latest headlines itself.
	if err := r.Widget(contracts.WidgetDefinition{
		ID: "news.summary", Module: "news", SchemaVersion: 1, Title: "资讯概览", WidgetKind: "news.summary",
		DataRoute: "/api/modules/news/stats", Size: contracts.WidgetSmall, Order: 30,
	}); err != nil {
		return err
	}
	for _, job := range []struct {
		id      string
		timeout time.Duration
		handler func(context.Context, contracts.JobRun) error
	}{
		{"news.fetch", 3 * time.Minute, m.fetchJob}, {"news.process", 3 * time.Minute, m.processJob}, {"news.digest", 2 * time.Minute, m.digestJob},
	} {
		if err := r.Job(contracts.JobDefinition{ID: contracts.JobID(job.id), Module: "news", Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: time.Minute}, TimeZone: "UTC", Timeout: job.timeout, OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce, Retry: contracts.RetryPolicy{MaxAttempts: 3, InitialWait: time.Minute, MaxWait: 5 * time.Minute}, Handler: job.handler}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) mutate(ctx context.Context, topic, id string, fn func(*newssqlc.Queries) error) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(m.q.WithTx(tx)); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"id": id})
	if _, err = m.events.PublishTx(ctx, tx, contracts.NewEvent{Topic: topic, SchemaVersion: 1, SourceModule: "news", AggregateID: id, Payload: payload}); err != nil {
		return err
	}
	return tx.Commit()
}

func bit(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
func optionalTime(v int64) *time.Time {
	if v == 0 {
		return nil
	}
	t := time.UnixMilli(v).UTC()
	return &t
}
