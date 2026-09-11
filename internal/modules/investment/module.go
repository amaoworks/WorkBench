package investment

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"workbench/internal/contracts"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed chart/*
var chartAssets embed.FS

const (
	defaultSchwabAPI = "https://api.schwabapi.com"
	defaultTVOrigin  = "https://trading-terminal.tradingview-widget.com"
)

type Dependencies struct {
	DB *sql.DB
}

type HTTPRoute struct {
	Method  string
	Pattern string
	Handler http.Handler
}

type Module struct {
	deps       Dependencies
	queries    *investmentsqlc.Queries
	now        func() time.Time
	httpClient *http.Client
	schwabAPI  string
	tvOrigin   string
	tvProxy    *httputil.ReverseProxy
	tokenMu    sync.Mutex
	streamer   *streamer
}

func New(deps Dependencies) (*Module, error) {
	if deps.DB == nil {
		return nil, errors.New("investment dependencies are required")
	}
	module := &Module{
		deps:    deps,
		queries: investmentsqlc.New(deps.DB),
		now:     time.Now,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		schwabAPI: defaultSchwabAPI,
		tvOrigin:  defaultTVOrigin,
	}
	module.tvProxy = newTVProxy(module.tvOrigin)
	module.streamer = newStreamer(module)
	return module, nil
}

func (m *Module) Manifest() contracts.ModuleManifest {
	return contracts.ModuleManifest{ID: "investment", Name: "投资", Version: "0.2.0", ContractVersion: 1, Icon: "investment.chart",
		Navigation: []contracts.NavigationItem{{Label: "投资", Route: "/investment", PageKey: "investment.overview", Order: 20}}}
}

func (m *Module) Migrations() contracts.MigrationSet {
	return contracts.MigrationSet{FS: migrations, Dir: "migrations"}
}

func (m *Module) Register(r contracts.ModuleRegistrar) error {
	proxyMethods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	regs := []error{
		r.Handle("GET", "/api/modules/investment/schwab", http.HandlerFunc(m.getSchwab)),
		r.Handle("PUT", "/api/modules/investment/schwab", http.HandlerFunc(m.saveSchwab)),
		r.Handle("POST", "/api/modules/investment/schwab/disconnect", http.HandlerFunc(m.disconnectSchwab)),
		r.Handle("GET", "/api/modules/investment/schwab/oauth/login", http.HandlerFunc(m.oauthLogin)),
		r.Handle("POST", "/api/modules/investment/schwab/oauth/refresh", http.HandlerFunc(m.oauthRefreshHTTP)),
		r.Handle("GET", "/api/modules/investment/schwab/trader/ws", http.HandlerFunc(m.serveStreamer)),
		r.Handle("GET", "/api/modules/investment/schwab/marketdata/ws", http.HandlerFunc(m.serveStreamer)),
		r.Handle("POST", "/api/modules/investment/schwab/marketdata/ws/command", http.HandlerFunc(m.streamerCommand)),
		r.Widget(contracts.WidgetDefinition{ID: "investment.overview", Module: "investment", SchemaVersion: 1,
			Title: "投资", WidgetKind: "investment.overview", DataRoute: "/api/modules/investment/schwab", Size: contracts.WidgetMedium, Order: 20}),
		r.Job(contracts.JobDefinition{ID: "investment.schwab_refresh", Module: "investment", Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: 20 * time.Minute},
			TimeZone: "UTC", Timeout: 30 * time.Second, OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce,
			Retry: contracts.RetryPolicy{MaxAttempts: 3, InitialWait: time.Second, MaxWait: time.Minute}, Handler: func(ctx context.Context, _ contracts.JobRun) error { return m.refreshAccessToken(ctx) }}),
	}
	for _, method := range proxyMethods {
		regs = append(regs,
			r.Handle(method, "/api/modules/investment/schwab/trader/v1/*", m.proxySchwab("/trader/v1")),
			r.Handle(method, "/api/modules/investment/schwab/marketdata/v1/*", m.proxySchwab("/marketdata/v1")),
		)
	}
	return errors.Join(regs...)
}

// PublicRoutes are mounted outside session auth so Schwab's cross-site OAuth redirect can complete.
func (m *Module) PublicRoutes() []HTTPRoute {
	return []HTTPRoute{{Method: http.MethodGet, Pattern: "/oauth/schwab", Handler: http.HandlerFunc(m.oauthCallback)}}
}

// AssetRoutes are same-origin chart pages and the TradingView library reverse proxy.
func (m *Module) AssetRoutes() []HTTPRoute {
	library := http.HandlerFunc(m.proxyChartingLibrary)
	return []HTTPRoute{
		{Method: http.MethodGet, Pattern: "/investment/terminal", Handler: http.HandlerFunc(m.serveTerminal)},
		{Method: http.MethodGet, Pattern: "/investment/terminal/*", Handler: http.HandlerFunc(m.serveTerminal)},
		{Method: http.MethodGet, Pattern: "/charting_library/*", Handler: library},
		{Method: http.MethodHead, Pattern: "/charting_library/*", Handler: library},
	}
}

// ResetStream invalidates existing streams when the module is disabled.
func (m *Module) ResetStream() { m.streamer.reset() }

// Close releases upgraded connections, which http.Server.Shutdown does not close.
func (m *Module) Close() { m.streamer.close() }
