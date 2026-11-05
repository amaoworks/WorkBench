package investment

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"workbench/internal/contracts"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

//go:embed migrations/*.sql migrations-postgres/*.sql
var migrations embed.FS

//go:embed chart/*
var chartAssets embed.FS

const (
	defaultSchwabAPI = "https://api.schwabapi.com"
	defaultTVOrigin  = "https://trading-terminal.tradingview-widget.com"
)

type Dependencies struct {
	DB                *sql.DB
	Users             contracts.UserDirectory
	Notifications     contracts.NotificationService
	Logger            *slog.Logger
	FutuConfigDir     string
	FutuRuntimeDir    string
	FutuOpenDBinary   string
	FutuOpenDAddress  string
	FutuAllowNonLocal bool
	ChartCacheDir     string
}

type HTTPRoute struct {
	Method  string
	Pattern string
	Handler http.Handler
}

type Module struct {
	deps           Dependencies
	root           *Module
	userID         string
	usersMu        sync.Mutex
	users          map[string]*Module
	appMu          sync.Mutex
	terminalAssets fs.FS
	queries        *investmentsqlc.Queries
	now            func() time.Time
	httpClient     *http.Client
	schwabAPI      string
	tvOrigin       string
	tvProxy        *httputil.ReverseProxy
	tokenMu        sync.Mutex
	oauthMu        sync.Mutex
	oauthSessions  map[string]mobileOAuthSession
	streamer       *streamer
	futu           *futuGateway
	opend          *openDService
	logger         *slog.Logger
	monitorMu      sync.Mutex
	monitorEnabled bool
	netWorthCancel context.CancelFunc
	netWorthWG     sync.WaitGroup
	monitorQuotes  monitorQuoteProvider
	scanMu         sync.Mutex
	marketLocation *time.Location
	marketHours    marketHoursCache // guarded by scanMu
	overview       overviewState
}

func New(deps Dependencies) (*Module, error) {
	if deps.DB == nil || deps.Notifications == nil || deps.Users == nil {
		return nil, errors.New("investment database, notifications and user directory are required")
	}
	terminalAssets, err := terminalChartAssets()
	if err != nil {
		return nil, err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	module := &Module{
		deps:           deps,
		users:          make(map[string]*Module),
		terminalAssets: terminalAssets,
		logger:         deps.Logger,
		queries:        investmentsqlc.New(deps.DB),
		now:            time.Now,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		schwabAPI:     defaultSchwabAPI,
		tvOrigin:      defaultTVOrigin,
		oauthSessions: make(map[string]mobileOAuthSession),
	}
	module.tvProxy = newTVProxy(module.tvOrigin, deps.ChartCacheDir)
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil, err
	}
	module.marketLocation = location
	module.monitorEnabled = true
	module.monitorQuotes = &schwabMonitorProvider{
		accessToken: module.ensureAccessToken,
		session:     module.regularSession,
		quotes:      module.fetchMonitorQuotes,
		guard:       module.withCurrentSchwabToken,
	}
	if _, _, err := module.futuConnection(); err != nil {
		return nil, err
	}
	module.tvProxy.ErrorLog = slog.NewLogLogger(deps.Logger.Handler(), slog.LevelError)
	if deps.FutuConfigDir == "" && deps.FutuRuntimeDir != "" {
		module.opend = newOpenDService(deps.FutuRuntimeDir, deps.FutuOpenDBinary)
	}
	module.streamer = newStreamer(streamerDeps{
		credentials: module.streamerCredentials,
		origin:      func() string { return module.schwabAPI },
		logger:      module.logger,
	})
	module.futu = newFutuGateway(futuGatewayDeps{
		load:    module.loadFutu,
		queries: module.queries,
		opend:   module.opend,
		logger:  module.logger,
	})
	return module, nil
}

func (m *Module) Manifest() contracts.ModuleManifest {
	return contracts.ModuleManifest{ID: "investment", Name: "投资", Version: "0.3.0", ContractVersion: 1, Icon: "investment.chart",
		Navigation: []contracts.NavigationItem{{Label: "投资", Route: "/investment", PageKey: "investment.overview", Order: 20}}}
}

func (m *Module) Migrations() contracts.MigrationSet {
	return contracts.MigrationSet{FS: migrations, Dir: "migrations", PostgresDir: "migrations-postgres"}
}

func (m *Module) Register(r contracts.ModuleRegistrar) error {
	proxyMethods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	regs := []error{
		r.Handle("GET", "/api/modules/investment/positions", m.personalHTTP((*Module).getPositions)),
		r.Handle("GET", "/api/modules/investment/orders", m.personalHTTP((*Module).getOrders)),
		r.Handle("GET", "/api/modules/investment/networth", m.personalHTTP((*Module).getNetWorth)),
		r.Job(m.netWorthJob()),
		r.Handle("GET", "/api/modules/investment/preferences", m.personalHTTP((*Module).getPreferences)),
		r.Handle("PUT", "/api/modules/investment/preferences", m.personalHTTP((*Module).savePreference)),
		r.Handle("GET", "/api/modules/investment/watchlists", m.personalHTTP((*Module).getWatchlists)),
		r.Handle("PUT", "/api/modules/investment/watchlists", m.personalHTTP((*Module).saveWatchlists)),
		r.Handle("GET", "/api/modules/investment/monitor", m.personalHTTP((*Module).getPriceMonitor)),
		r.Handle("POST", "/api/modules/investment/monitor/rules", m.personalHTTP((*Module).savePriceRule)),
		r.Handle("PUT", "/api/modules/investment/monitor/rules/{id}", m.personalHTTP((*Module).savePriceRule)),
		r.Handle("DELETE", "/api/modules/investment/monitor/rules/{id}", m.personalHTTP((*Module).deletePriceRule)),
		r.Handle("GET", "/api/modules/investment/monitor/history", m.personalHTTP((*Module).getPriceHistory)),
		r.Job(m.monitorJob()),
		r.Tool(contracts.AITool{Name: "investment.positions", Module: "investment", SchemaVersion: 1,
			Description:      "查询已连接 Schwab 账户的持仓和已存行情时间",
			ParametersSchema: json.RawMessage(`{"type":"object","properties":{"accountHash":{"type":["string","null"],"minLength":1,"maxLength":200}},"required":["accountHash"],"additionalProperties":false}`),
			Risk:             contracts.ToolRiskReadOnly, Handler: m.personalTool((*Module).positionsTool)}),
		r.Tool(contracts.AITool{Name: "investment.orders", Module: "investment", SchemaVersion: 1,
			Description:      "按时间范围、状态和账户查询 Schwab 订单",
			ParametersSchema: json.RawMessage(`{"type":"object","properties":{"days":{"type":"integer","enum":[1,7,30,90]},"status":{"type":"string","enum":["all","working","filled","canceled"]},"timeZone":{"type":"string","minLength":1,"maxLength":100},"accountHash":{"type":["string","null"],"minLength":1,"maxLength":200}},"required":["days","status","timeZone","accountHash"],"additionalProperties":false}`),
			Risk:             contracts.ToolRiskReadOnly, Handler: m.personalTool((*Module).ordersTool)}),
		r.Tool(contracts.AITool{Name: "investment.list_monitors", Module: "investment", SchemaVersion: 1,
			Description:      "查询价格监控规则及最近检查状态",
			ParametersSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
			Risk:             contracts.ToolRiskReadOnly, Handler: m.personalTool((*Module).listMonitorsTool)}),
		r.Tool(contracts.AITool{Name: "investment.create_monitor", Module: "investment", SchemaVersion: 1,
			Description:      "创建一条美股或 ETF 价格涨跌幅监控规则",
			ParametersSchema: json.RawMessage(`{"type":"object","properties":{"symbol":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9.\\-]{0,14}$"},"direction":{"type":"string","enum":["up","down"]},"thresholdPercent":{"type":"number","exclusiveMinimum":0,"maximum":1000},"enabled":{"type":"boolean"}},"required":["symbol","direction","thresholdPercent","enabled"],"additionalProperties":false}`),
			Risk:             contracts.ToolRiskLowWrite, Handler: m.personalTool((*Module).createMonitorTool)}),
		r.Handle("GET", "/api/modules/investment/schwab", m.personalHTTP((*Module).getSchwab)),
		r.Handle("PUT", "/api/modules/investment/schwab", m.personalHTTP((*Module).saveSchwab)),
		r.Handle("POST", "/api/modules/investment/schwab/disconnect", m.personalHTTP((*Module).disconnectSchwab)),
		r.Handle("GET", "/api/modules/investment/schwab/oauth/login", m.personalHTTP((*Module).oauthLogin)),
		r.Handle("POST", "/api/modules/investment/schwab/oauth/start", m.personalHTTP((*Module).oauthStart)),
		r.Handle("GET", "/api/modules/investment/schwab/oauth/status/{id}", m.personalHTTP((*Module).oauthStatus)),
		r.Handle("POST", "/api/modules/investment/schwab/oauth/refresh", m.personalHTTP((*Module).oauthRefreshHTTP)),
		r.Handle("GET", "/api/modules/investment/schwab/trader/ws", m.personalHTTP((*Module).serveStreamer)),
		r.Handle("GET", "/api/modules/investment/schwab/marketdata/ws", m.personalHTTP((*Module).serveStreamer)),
		r.Handle("POST", "/api/modules/investment/schwab/marketdata/ws/command", m.personalHTTP((*Module).streamerCommand)),
		r.Handle("GET", "/api/modules/investment/futu", m.personalHTTP((*Module).getFutu)),
		r.Handle("PUT", "/api/modules/investment/futu", m.personalHTTP((*Module).saveFutu)),
		r.Handle("POST", "/api/modules/investment/futu/disconnect", m.personalHTTP((*Module).disconnectFutu)),
		r.Handle("GET", "/api/modules/investment/overnight", m.personalHTTP((*Module).getOvernight)),
		r.Handle("PUT", "/api/modules/investment/overnight", m.personalHTTP((*Module).saveOvernight)),
		r.Handle("GET", "/api/modules/investment/market-data", m.personalHTTP((*Module).getMarketData)),
		r.Handle("GET", "/api/modules/investment/futu/quotes", m.personalHTTP((*Module).getFutuQuotes)),
		r.Handle("GET", "/api/modules/investment/futu/symbols", m.personalHTTP((*Module).getFutuSymbols)),
		r.Handle("GET", "/api/modules/investment/futu/kline", m.personalHTTP((*Module).getFutuKline)),
		r.Handle("GET", "/api/modules/investment/futu/quote/ws", m.personalHTTP((*Module).serveFutuStream)),
		r.Handle("POST", "/api/modules/investment/futu/quote/ws/command", m.personalHTTP((*Module).futuCommand)),
		r.Handle("GET", "/api/modules/investment/overview/snapshot", m.personalHTTP((*Module).getOverviewSnapshot)),
		r.Handle("GET", "/api/modules/investment/overview/quotes", m.personalHTTP((*Module).getOverviewQuotes)),
		r.Handle("GET", "/api/modules/investment/overview/alerts", m.personalHTTP((*Module).getOverviewAlerts)),
		r.Widget(contracts.WidgetDefinition{ID: "investment.snapshot", Module: "investment", SchemaVersion: 1,
			Title: "资产快照", WidgetKind: "investment.snapshot", DataRoute: "/api/modules/investment/overview/snapshot", Size: contracts.WidgetLarge, Order: 10}),
		r.Widget(contracts.WidgetDefinition{ID: "investment.quotes", Module: "investment", SchemaVersion: 1,
			Title: "持仓与自选行情", WidgetKind: "investment.quotes", DataRoute: "/api/modules/investment/overview/quotes", Size: contracts.WidgetLarge, Order: 30}),
		r.Widget(contracts.WidgetDefinition{ID: "investment.alerts", Module: "investment", SchemaVersion: 1,
			Title: "提醒与异动", WidgetKind: "investment.alerts", DataRoute: "/api/modules/investment/overview/alerts", Size: contracts.WidgetMedium, Order: 40}),
		r.Job(contracts.JobDefinition{ID: "investment.schwab_refresh", Module: "investment", Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: 20 * time.Minute},
			TimeZone: "UTC", Timeout: 30 * time.Second, OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce,
			Retry: contracts.RetryPolicy{MaxAttempts: 3, InitialWait: time.Second, MaxWait: time.Minute}, Handler: func(ctx context.Context, _ contracts.JobRun) error {
				return m.forEachUser(ctx, func(child *Module, ctx context.Context) error {
					err := child.refreshAccessToken(ctx)
					if errors.Is(err, errSchwabReauthorizationRequired) {
						return nil
					}
					return err
				})
			}}),
	}
	for _, method := range proxyMethods {
		regs = append(regs,
			r.Handle(method, "/api/modules/investment/schwab/trader/v1/*", m.personalHTTP(func(child *Module, w http.ResponseWriter, r *http.Request) {
				child.proxySchwab("/trader/v1").ServeHTTP(w, r)
			})),
			r.Handle(method, "/api/modules/investment/schwab/marketdata/v1/*", m.personalHTTP(func(child *Module, w http.ResponseWriter, r *http.Request) {
				child.proxySchwab("/marketdata/v1").ServeHTTP(w, r)
			})),
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
	library := m.personalHTTP((*Module).proxyChartingLibrary)
	return []HTTPRoute{
		{Method: http.MethodGet, Pattern: "/investment/terminal", Handler: m.personalHTTP((*Module).serveTerminal)},
		{Method: http.MethodGet, Pattern: "/investment/terminal/*", Handler: m.personalHTTP((*Module).serveTerminal)},
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
	if !enabled && m.netWorthCancel != nil {
		m.netWorthCancel()
		m.netWorthCancel = nil
	}
	m.monitorMu.Unlock()
	m.usersMu.Lock()
	for _, child := range m.users {
		child.monitorMu.Lock()
		child.monitorEnabled = enabled
		if !enabled && child.netWorthCancel != nil {
			child.netWorthCancel()
			child.netWorthCancel = nil
		}
		child.monitorMu.Unlock()
	}
	m.usersMu.Unlock()
	m.futu.connectMu.Lock()
	defer m.futu.connectMu.Unlock()
	m.futu.enabled = enabled
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
	return m.forEachUser(ctx, func(child *Module, ctx context.Context) error { child.startNetWorthSnapshot(ctx); return nil })
}

// ResetStream invalidates existing streams when the module is disabled.
func (m *Module) ResetStream() {
	m.streamer.reset()
	m.usersMu.Lock()
	for _, child := range m.users {
		child.streamer.reset()
	}
	m.usersMu.Unlock()
	m.futu.reset()
}

// Close releases upgraded connections, which http.Server.Shutdown does not close.
func (m *Module) Close() {
	m.monitorMu.Lock()
	m.monitorEnabled = false
	if m.netWorthCancel != nil {
		m.netWorthCancel()
		m.netWorthCancel = nil
	}
	m.monitorMu.Unlock()
	m.netWorthWG.Wait()
	m.futu.connectMu.Lock()
	if m.opend != nil {
		m.opend.close()
	}
	m.futu.connectMu.Unlock()
	m.streamer.close()
	m.usersMu.Lock()
	children := make([]*Module, 0, len(m.users))
	for _, child := range m.users {
		children = append(children, child)
	}
	clear(m.users)
	m.usersMu.Unlock()
	for _, child := range children {
		child.monitorMu.Lock()
		child.monitorEnabled = false
		if child.netWorthCancel != nil {
			child.netWorthCancel()
		}
		child.monitorMu.Unlock()
		child.netWorthWG.Wait()
		child.streamer.close()
	}
	m.futu.close()
}
