package investment

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"net/http"
	"time"

	"workbench/internal/contracts"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Dependencies struct {
	DB            *sql.DB
	Events        contracts.EventPublisher
	Notifications contracts.NotificationService
	AI            contracts.TextGenerator
	Quotes        QuoteProvider
}

type Module struct {
	deps    Dependencies
	queries *investmentsqlc.Queries
}

func New(deps Dependencies) (*Module, error) {
	if deps.DB == nil || deps.Events == nil || deps.Notifications == nil || deps.AI == nil || deps.Quotes == nil {
		return nil, errors.New("investment dependencies are required")
	}
	return &Module{deps: deps, queries: investmentsqlc.New(deps.DB)}, nil
}

func (m *Module) Manifest() contracts.ModuleManifest {
	return contracts.ModuleManifest{ID: "investment", Name: "模拟行情", Version: "0.1.0", ContractVersion: 1, Icon: "investment.chart",
		Navigation: []contracts.NavigationItem{{Label: "模拟行情", Route: "/investment", PageKey: "investment.overview", Order: 20}}}
}

func (m *Module) Migrations() contracts.MigrationSet {
	return contracts.MigrationSet{FS: migrations, Dir: "migrations"}
}

func (m *Module) Register(r contracts.ModuleRegistrar) error {
	return errors.Join(
		r.Handle("GET", "/api/modules/investment/overview", http.HandlerFunc(m.overview)),
		r.Handle("POST", "/api/modules/investment/sync", http.HandlerFunc(m.syncHTTP)),
		r.Handle("POST", "/api/modules/investment/summary", http.HandlerFunc(m.summarize)),
		r.Widget(contracts.WidgetDefinition{ID: "investment.overview", Module: "investment", SchemaVersion: 1,
			Title: "模拟行情", WidgetKind: "investment.overview", DataRoute: "/api/modules/investment/overview", Size: contracts.WidgetMedium, Order: 20}),
		r.Job(contracts.JobDefinition{ID: "investment.sync", Module: "investment", Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: 5 * time.Minute},
			TimeZone: "UTC", Timeout: 30 * time.Second, OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce,
			Retry: contracts.RetryPolicy{MaxAttempts: 3, InitialWait: time.Second, MaxWait: time.Minute}, Handler: func(ctx context.Context, _ contracts.JobRun) error { return m.Sync(ctx) }}),
		r.Consume(contracts.EventConsumer{ID: "investment.price_alert", Module: "investment", Topics: []string{"investment.price.updated"},
			Timeout: 10 * time.Second, MaxAttempts: 3, Handler: m.priceAlert}),
	)
}
