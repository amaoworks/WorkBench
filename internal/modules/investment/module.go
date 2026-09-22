package investment

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"log/slog"
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
	DB                *sql.DB
	Notifications     contracts.NotificationService
	Logger            *slog.Logger
	FutuConfigDir     string
	FutuRuntimeDir    string
	FutuOpenDBinary   string
	FutuOpenDAddress  string
	FutuAllowNonLocal bool
}

type HTTPRoute struct {
	Method  string
	Pattern string
	Handler http.Handler
}

type Module struct {
	deps           Dependencies
	queries        *investmentsqlc.Queries
	now            func() time.Time
	httpClient     *http.Client
	schwabAPI      string
	tvOrigin       string
	tvProxy        *httputil.ReverseProxy
	tokenMu        sync.Mutex
	streamer       *streamer
	futu           *futuGateway
	opend          *openDService
	moduleEnabled  bool // guarded by futu.connectMu
	logger         *slog.Logger
	monitorMu      sync.Mutex
	monitorEnabled bool
	scanMu         sync.Mutex
	marketLocation *time.Location
	marketHours    marketHoursCache // guarded by scanMu
}

func New(deps Dependencies) (*Module, error) {
	if deps.DB == nil {
		return nil, errors.New("investment dependencies are required")
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	module := &Module{
		deps:    deps,
		logger:  deps.Logger,
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
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil, err
	}
	module.marketLocation = location
	module.monitorEnabled = true
	if _, _, err := module.futuConnection(); err != nil {
		return nil, err
	}
	module.tvProxy.ErrorLog = slog.NewLogLogger(deps.Logger.Handler(), slog.LevelError)
	module.streamer = newStreamer(module)
	module.futu = newFutuGateway(module)
	module.moduleEnabled = true
	if deps.FutuConfigDir == "" && deps.FutuRuntimeDir != "" {
		module.opend = newOpenDService(deps.FutuRuntimeDir, deps.FutuOpenDBinary)
	}
	return module, nil
}

func (m *Module) Manifest() contracts.ModuleManifest {
	return contracts.ModuleManifest{ID: "investment", Name: "投资", Version: "0.3.0", ContractVersion: 1, Icon: "investment.chart",
		Navigation: []contracts.NavigationItem{{Label: "投资", Route: "/investment", PageKey: "investment.overview", Order: 20}}}
}

func (m *Module) Migrations() contracts.MigrationSet {
	return contracts.MigrationSet{FS: migrations, Dir: "migrations"}
}

func (m *Module) Register(r contracts.ModuleRegistrar) error {
	proxyMethods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	regs := []error{
		r.Handle("GET", "/api/modules/investment/monitor", http.HandlerFunc(m.getPriceMonitor)),
		r.Handle("POST", "/api/modules/investment/monitor/rules", http.HandlerFunc(m.savePriceRule)),
		r.Handle("PUT", "/api/modules/investment/monitor/rules/{id}", http.HandlerFunc(m.savePriceRule)),
		r.Handle("DELETE", "/api/modules/investment/monitor/rules/{id}", http.HandlerFunc(m.deletePriceRule)),
		r.Handle("GET", "/api/modules/investment/monitor/history", http.HandlerFunc(m.getPriceHistory)),
		r.Job(m.monitorJob()),
		r.Handle("GET", "/api/modules/investment/schwab", http.HandlerFunc(m.getSchwab)),
		r.Handle("PUT", "/api/modules/investment/schwab", http.HandlerFunc(m.saveSchwab)),
		r.Handle("POST", "/api/modules/investment/schwab/disconnect", http.HandlerFunc(m.disconnectSchwab)),
		r.Handle("GET", "/api/modules/investment/schwab/oauth/login", http.HandlerFunc(m.oauthLogin)),
		r.Handle("POST", "/api/modules/investment/schwab/oauth/refresh", http.HandlerFunc(m.oauthRefreshHTTP)),
		r.Handle("GET", "/api/modules/investment/schwab/trader/ws", http.HandlerFunc(m.serveStreamer)),
		r.Handle("GET", "/api/modules/investment/schwab/marketdata/ws", http.HandlerFunc(m.serveStreamer)),
		r.Handle("POST", "/api/modules/investment/schwab/marketdata/ws/command", http.HandlerFunc(m.streamerCommand)),
		r.Handle("GET", "/api/modules/investment/futu", http.HandlerFunc(m.getFutu)),
		r.Handle("PUT", "/api/modules/investment/futu", http.HandlerFunc(m.saveFutu)),
		r.Handle("POST", "/api/modules/investment/futu/disconnect", http.HandlerFunc(m.disconnectFutu)),
		r.Handle("GET", "/api/modules/investment/overnight", http.HandlerFunc(m.getOvernight)),
		r.Handle("PUT", "/api/modules/investment/overnight", http.HandlerFunc(m.saveOvernight)),
		r.Handle("GET", "/api/modules/investment/futu/kline", http.HandlerFunc(m.getFutuKline)),
		r.Handle("GET", "/api/modules/investment/futu/quote/ws", http.HandlerFunc(m.serveFutuStream)),
		r.Handle("POST", "/api/modules/investment/futu/quote/ws/command", http.HandlerFunc(m.futuCommand)),
		r.Widget(contracts.WidgetDefinition{ID: "investment.overview", Module: "investment", SchemaVersion: 1,
			Title: "投资", WidgetKind: "investment.overview", DataRoute: "/api/modules/investment/schwab", Size: contracts.WidgetMedium, Order: 20}),
		r.Job(contracts.JobDefinition{ID: "investment.schwab_refresh", Module: "investment", Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: 20 * time.Minute},
			TimeZone: "UTC", Timeout: 30 * time.Second, OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce,
			Retry: contracts.RetryPolicy{MaxAttempts: 3, InitialWait: time.Second, MaxWait: time.Minute}, Handler: func(ctx context.Context, _ contracts.JobRun) error {
				err := m.refreshAccessToken(ctx)
				if errors.Is(err, errSchwabReauthorizationRequired) {
					return nil // Await interactive authorization; retries cannot repair this state.
				}
				return err
			}}),
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

func (m *Module) Routes() []contracts.ProvidedRoute {
	routes := make([]contracts.ProvidedRoute, 0, 5)
	for _, route := range m.PublicRoutes() {
		routes = append(routes, contracts.ProvidedRoute{Method: route.Method, Pattern: route.Pattern, Handler: route.Handler, Class: contracts.RoutePublicCallback})
	}
	for _, route := range m.AssetRoutes() {
		routes = append(routes, contracts.ProvidedRoute{Method: route.Method, Pattern: route.Pattern, Handler: route.Handler, Class: contracts.RouteProtectedAsset})
	}
	return routes
}

func (m *Module) OnEnabledChanged(ctx context.Context, enabled bool) error {
	m.monitorMu.Lock()
	m.monitorEnabled = enabled
	m.monitorMu.Unlock()
	m.futu.connectMu.Lock()
	defer m.futu.connectMu.Unlock()
	m.moduleEnabled = enabled
	if !enabled {
		// Stopping must not depend on a live request context or a database read.
		m.syncOpenD(futuRecord{})
		m.ResetStream()
		return m.publishFutuLogin(futuRecord{})
	}
	rec, err := m.loadFutu(ctx)
	if err != nil {
		return err
	}
	if err := m.publishFutuLogin(rec); err != nil {
		return err
	}
	m.syncOpenD(rec)
	return nil
}

// ResetStream invalidates existing streams when the module is disabled.
func (m *Module) ResetStream() {
	m.streamer.reset()
	m.futu.reset()
}

// Close releases upgraded connections, which http.Server.Shutdown does not close.
func (m *Module) Close() {
	m.monitorMu.Lock()
	m.monitorEnabled = false
	m.monitorMu.Unlock()
	m.futu.connectMu.Lock()
	if m.opend != nil {
		m.opend.close()
	}
	m.futu.connectMu.Unlock()
	m.streamer.close()
	m.futu.close()
}
