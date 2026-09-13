package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"workbench/internal/capabilities/ai"
	"workbench/internal/capabilities/conversation"
	"workbench/internal/capabilities/dashboard"
	"workbench/internal/capabilities/notifications"
	"workbench/internal/capabilities/scheduler"
	"workbench/internal/contracts"
	"workbench/internal/foundation/auth"
	workbenchdb "workbench/internal/foundation/database"
	"workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/events"
	"workbench/internal/foundation/httpapi"
	"workbench/internal/foundation/logging"
	"workbench/internal/foundation/modules"
	"workbench/internal/modules/investment"
	"workbench/internal/modules/todo"
	"workbench/internal/webui"
)

type builtinBinding struct {
	id        contracts.ModuleID
	module    contracts.Module
	routes    contracts.ModuleRouteProvider
	lifecycle contracts.ModuleLifecycle
}

type App struct {
	config        Config
	logger        *slog.Logger
	logLevel      *slog.LevelVar
	database      *workbenchdb.Database
	registry      *modules.Registry
	dispatcher    *events.Dispatcher
	scheduler     *scheduler.Scheduler
	auth          *auth.Service
	handler       http.Handler
	server        *http.Server
	closeOnce     sync.Once
	schedulerOnce sync.Once
	schedulerErr  error
	settingsMu    sync.Mutex
	aiSettings    AISettings
	appearance    Appearance
	logging       LoggingSettings
	gateway       *ai.Gateway
	textAI        *ai.TextService
	builtins      []builtinBinding
}

func New(ctx context.Context, cfg Config, logger *slog.Logger) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.PublicURL != "" {
		origin, _ := url.Parse(cfg.PublicURL) // Validated above.
		cfg.AllowedHosts = append(append([]string(nil), cfg.AllowedHosts...), origin.Host)
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	logger, logLevel, err := logging.New(logger, cfg.LogLevel)
	if err != nil {
		return nil, err
	}
	logger = logger.With("service", "workbench")
	database, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: cfg.DataPath, MaxConnections: 4})
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*App, error) {
		_ = database.Close()
		return nil, err
	}
	if err := database.MigrateCore(ctx); err != nil {
		return fail(err)
	}

	eventStore := events.NewStore(database.SQL())
	notificationService, err := notifications.NewService(database.SQL(), eventStore)
	if err != nil {
		return fail(err)
	}
	todoModule, err := todo.New(database.SQL(), eventStore, notificationService)
	if err != nil {
		return fail(err)
	}
	textAI := ai.NewTextService()
	investmentModule, err := investment.New(investment.Dependencies{DB: database.SQL(), Logger: logger.With("component", "investment")})
	if err != nil {
		return fail(err)
	}
	definitions := []contracts.Module{todoModule, investmentModule}
	registry, err := modules.InitializeWith(ctx, database, definitions, modules.Options{
		Logger: logger.With("component", "modules"),
	})
	if err != nil {
		return fail(err)
	}
	builtins := bindBuiltinModules(definitions)

	hub := notifications.NewHub()
	consumers := append(registry.Catalog().Consumers, hub.Consumer())
	enabled := func(id contracts.ModuleID) bool { return id == "core" || registry.IsEnabled(id) }
	dispatcher, err := events.NewDispatcher(database.SQL(), eventStore, consumers, enabled, logger.With("component", "events"))
	if err != nil {
		return fail(err)
	}
	jobs := append(registry.Catalog().Jobs, maintenanceJob(database.SQL()))
	scheduled, err := scheduler.New(ctx, database.SQL(), jobs, enabled, logger.With("component", "scheduler"))
	if err != nil {
		return fail(err)
	}
	authService, err := auth.New(ctx, database.SQL(), auth.Config{
		Mode: cfg.AuthMode, ListenAddress: cfg.ListenAddress, PublicHTTPS: cfg.PublicHTTPS(),
		InitialPassword: cfg.Password, AllowedHosts: cfg.AllowedHosts,
		Logger: logger.With("component", "auth"),
	})
	if err != nil {
		_ = scheduled.Shutdown(context.Background())
		return fail(err)
	}
	ui, err := webui.New()
	if err != nil {
		_ = scheduled.Shutdown(context.Background())
		return fail(err)
	}
	dashboardService := dashboard.New(database.SQL(), registry.Catalog().Widgets, registry.IsEnabled)
	notificationHTTP := notifications.NewHTTPHandler(notificationService, hub)
	toolRuntime, err := ai.NewToolRuntime(database.SQL(), registry.Catalog().Tools, registry.IsEnabled)
	if err != nil {
		_ = scheduled.Shutdown(context.Background())
		return fail(err)
	}
	gateway, err := ai.NewGateway(nil, toolRuntime)
	if err != nil {
		_ = scheduled.Shutdown(context.Background())
		return fail(err)
	}
	conversationService, err := conversation.NewService(database.SQL(), gateway)
	if err != nil {
		_ = scheduled.Shutdown(context.Background())
		return fail(err)
	}
	conversationHTTP := conversation.NewHTTPHandler(conversationService)

	application := &App{
		config: cfg, logger: logger, logLevel: logLevel, database: database, registry: registry,
		dispatcher: dispatcher, scheduler: scheduled, auth: authService,
		builtins: builtins,
	}
	application.gateway = gateway
	application.textAI = textAI
	if err := application.loadSettings(ctx); err != nil {
		_ = scheduled.Shutdown(context.Background())
		return fail(err)
	}
	application.handler = application.routes(dashboardService, notificationHTTP, conversationHTTP, ui)
	application.server = &http.Server{
		Addr: cfg.ListenAddress, Handler: application.handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 0, IdleTimeout: 2 * time.Minute,
		ErrorLog: slog.NewLogLogger(logger.With("component", "http").Handler(), slog.LevelError),
	}
	logger.Debug("workspace initialized", "component", "app", "modules", len(registry.Catalog().Manifests))
	return application, nil
}

func (a *App) routes(
	dashboardService *dashboard.Service,
	notificationHTTP *notifications.HTTPHandler,
	conversationHTTP *conversation.HTTPHandler,
	ui *webui.Handler,
) http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(a.requestLogger)
	router.Use(a.recoverPanic)
	router.Use(a.auth.Security)
	router.Use(a.auth.LoadAndSave)
	router.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		httpapi.Write(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	router.Get("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := a.database.SQL().PingContext(r.Context()); err != nil {
			httpapi.Error(w, http.StatusServiceUnavailable, "database_unavailable", "database unavailable")
			return
		}
		httpapi.Write(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	router.Route("/api/auth", func(r chi.Router) {
		r.Get("/status", a.auth.StatusHandler)
		r.Get("/csrf", a.auth.CSRFTokenHandler)
		r.Post("/login", a.auth.LoginHandler)
		r.Post("/logout", a.auth.LogoutHandler)
	})
	for _, binding := range a.builtins {
		if binding.routes == nil {
			continue
		}
		for _, route := range binding.routes.Routes() {
			if route.Class == contracts.RoutePublicCallback {
				router.Method(route.Method, route.Pattern, route.Handler)
			}
		}
	}

	router.Group(func(protected chi.Router) {
		protected.Use(a.auth.Require)
		protected.Get("/api/modules", a.listModules)
		protected.Post("/api/modules/external", a.attachExternal)
		protected.Delete("/api/modules/external/{id}", a.unregisterExternal)
		protected.Put("/api/modules/{id}/enabled", a.setModuleEnabled)
		protected.Put("/api/modules/{id}/connection", a.updateExternalConnection)
		protected.Post("/api/modules/{id}/refresh", a.refreshExternal)
		protected.Get("/api/modules/{id}/status", a.getModuleStatus)
		protected.Get("/api/modules/{id}/config", a.getExternalConfig)
		protected.Put("/api/modules/{id}/config", a.putExternalConfig)
		protected.Handle("/modules/{id}/ui/*", a.registry.UIHandler())
		protected.Method(http.MethodGet, "/modules/{id}/settings/*", a.registry.SettingsHandler())
		protected.Method(http.MethodHead, "/modules/{id}/settings/*", a.registry.SettingsHandler())
		protected.Handle("/api/modules/{id}/proxy/*", a.registry.APIProxyHandler())
		protected.Get("/api/dashboard", dashboardService.ServeHTTP)
		protected.Get("/api/dashboard/widgets", dashboardService.Catalog)
		protected.Put("/api/dashboard/layout", dashboardService.Save)
		protected.Delete("/api/dashboard/layout", dashboardService.Reset)
		protected.Get("/api/notifications", notificationHTTP.List)
		protected.Get("/api/notifications/unread-count", notificationHTTP.UnreadCount)
		protected.Put("/api/notifications/read", notificationHTTP.MarkRead)
		protected.Put("/api/notifications/read-all", notificationHTTP.MarkAllRead)
		protected.Put("/api/notifications/archive", notificationHTTP.Archive)
		protected.Get("/api/notifications/stream", notificationHTTP.Stream)
		protected.Get("/api/ai/status", conversationHTTP.Status)
		protected.Post("/api/chat", conversationHTTP.Chat)
		protected.Post("/api/chat/stream", conversationHTTP.Stream)
		protected.Post("/api/system/backup", a.backup)
		protected.Get("/api/settings", a.getSettings)
		protected.Put("/api/settings/ai", a.saveAISettings)
		protected.Post("/api/settings/ai/test", a.testAISettings)
		protected.Put("/api/settings/appearance", a.saveAppearance)
		protected.Put("/api/settings/logging", a.saveLoggingSettings)
		protected.Put("/api/settings/password", a.auth.ChangePasswordHandler)
		for _, route := range a.registry.Catalog().Routes {
			protected.Method(route.Method, route.Pattern, a.registry.Gate(route.Module, route.Handler))
		}
		for _, binding := range a.builtins {
			if binding.routes == nil {
				continue
			}
			for _, route := range binding.routes.Routes() {
				if route.Class == contracts.RoutePublicCallback {
					continue
				}
				protected.Method(route.Method, route.Pattern, a.registry.Gate(binding.id, route.Handler))
			}
		}
	})
	router.NotFound(ui.ServeHTTP)
	return router
}

func bindBuiltinModules(definitions []contracts.Module) []builtinBinding {
	bindings := make([]builtinBinding, 0, len(definitions))
	for _, definition := range definitions {
		binding := builtinBinding{id: definition.Manifest().ID, module: definition}
		if routes, ok := definition.(contracts.ModuleRouteProvider); ok {
			binding.routes = routes
		}
		if lifecycle, ok := definition.(contracts.ModuleLifecycle); ok {
			binding.lifecycle = lifecycle
		}
		bindings = append(bindings, binding)
	}
	return bindings
}

func (a *App) listModules(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, map[string]any{"items": a.registry.List()})
}

func (a *App) setModuleEnabled(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := httpapi.Decode(w, r, &input, 4096); err != nil || input.Enabled == nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "enabled is required")
		return
	}
	id := contracts.ModuleID(r.PathValue("id"))
	if a.registry.IsExternal(id) {
		result, err := a.registry.SetExternalEnabled(r.Context(), id, *input.Enabled)
		if err != nil {
			writeModuleError(w, err)
			return
		}
		status := http.StatusOK
		if result.Pending {
			status = http.StatusAccepted
		}
		httpapi.Write(w, status, result.Module)
		return
	}
	if err := a.registry.SetEnabled(r.Context(), id, *input.Enabled); err != nil {
		httpapi.Error(w, http.StatusNotFound, "module_not_found", err.Error())
		return
	}
	for _, binding := range a.builtins {
		if binding.id == id && binding.lifecycle != nil {
			if err := binding.lifecycle.OnEnabledChanged(r.Context(), *input.Enabled); err != nil {
				a.logger.Error("module lifecycle failed", "component", "modules", "module", id, "error", err)
			}
			break
		}
	}
	a.logger.Info("module state changed", "component", "modules", "module", id, "enabled", *input.Enabled)
	listed, _ := a.registry.GetListed(id)
	httpapi.Write(w, http.StatusOK, listed)
}

func (a *App) attachExternal(w http.ResponseWriter, r *http.Request) {
	var input modules.ConnectionInput
	if err := httpapi.Decode(w, r, &input, 8192); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "连接配置格式不正确")
		return
	}
	listed, err := a.registry.Attach(r.Context(), input)
	if err != nil {
		writeModuleError(w, err)
		return
	}
	httpapi.Write(w, http.StatusCreated, listed)
}

func (a *App) updateExternalConnection(w http.ResponseWriter, r *http.Request) {
	var input modules.ConnectionInput
	if err := httpapi.Decode(w, r, &input, 8192); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "连接配置格式不正确")
		return
	}
	listed, err := a.registry.UpdateConnection(r.Context(), contracts.ModuleID(r.PathValue("id")), input)
	if err != nil {
		writeModuleError(w, err)
		return
	}
	httpapi.Write(w, http.StatusOK, listed)
}

func (a *App) refreshExternal(w http.ResponseWriter, r *http.Request) {
	listed, err := a.registry.Refresh(r.Context(), contracts.ModuleID(r.PathValue("id")))
	if err != nil {
		writeModuleError(w, err)
		return
	}
	httpapi.Write(w, http.StatusOK, listed)
}

func (a *App) getModuleStatus(w http.ResponseWriter, r *http.Request) {
	listed, ok := a.registry.GetListed(contracts.ModuleID(r.PathValue("id")))
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "module_not_found", "模块不存在")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, listed)
}

func (a *App) getExternalConfig(w http.ResponseWriter, r *http.Request) {
	body, err := a.registry.GetConfig(r.Context(), contracts.ModuleID(r.PathValue("id")))
	if err != nil {
		writeModuleError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (a *App) putExternalConfig(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := httpapi.Decode(w, r, &raw, 64<<10); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "配置格式不正确")
		return
	}
	body, err := a.registry.PutConfig(r.Context(), contracts.ModuleID(r.PathValue("id")), raw)
	if err != nil {
		writeModuleError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (a *App) unregisterExternal(w http.ResponseWriter, r *http.Request) {
	if err := a.registry.Unregister(r.Context(), contracts.ModuleID(r.PathValue("id"))); err != nil {
		writeModuleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeModuleError(w http.ResponseWriter, err error) {
	var apiErr *modules.Error
	if errors.As(err, &apiErr) {
		httpapi.Error(w, apiErr.Status, apiErr.Code, apiErr.Message)
		return
	}
	httpapi.Error(w, http.StatusInternalServerError, "internal_error", "模块操作失败")
}

func (a *App) Handler() http.Handler { return a.handler }
func (a *App) DB() *sql.DB           { return a.database.SQL() }

func maintenanceJob(db *sql.DB) contracts.JobDefinition {
	return contracts.JobDefinition{
		ID: "core.maintenance", Module: "core",
		Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: 24 * time.Hour},
		TimeZone: "UTC", Timeout: time.Minute,
		OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce,
		Retry: contracts.RetryPolicy{MaxAttempts: 3, InitialWait: time.Minute, MaxWait: 15 * time.Minute},
		Handler: func(ctx context.Context, _ contracts.JobRun) error {
			now := time.Now().UTC()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			queries := dbsqlc.New(tx)
			if err := queries.DeleteExpiredSessionsForMaintenance(ctx, now.UnixMilli()); err != nil {
				return err
			}
			cutoff := now.Add(-90 * 24 * time.Hour).UnixMilli()
			if err := queries.DeleteOldHandledNotifications(ctx, cutoff); err != nil {
				return err
			}
			return tx.Commit()
		},
	}
}

func (a *App) backup(w http.ResponseWriter, r *http.Request) {
	backupDir := filepath.Join(filepath.Dir(a.config.DataPath), "backups")
	name := "workbench-" + time.Now().UTC().Format("20060102T150405.000Z") + ".db"
	destination := filepath.Join(backupDir, name)
	if err := a.database.Backup(r.Context(), destination); err != nil {
		a.logger.Error("online backup failed", "component", "database", "error", err)
		httpapi.Error(w, http.StatusInternalServerError, "backup_failed", "could not create backup")
		return
	}
	a.logger.Info("online backup created", "component", "database", "file", name)
	httpapi.Write(w, http.StatusCreated, map[string]string{"file": name})
}

func (a *App) Run(ctx context.Context) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	listener, err := net.Listen("tcp", a.config.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", a.config.ListenAddress, err)
	}
	defer listener.Close()
	if err := a.scheduler.Start(runCtx); err != nil {
		_ = a.shutdownScheduler(context.Background())
		return fmt.Errorf("start scheduler: %w", err)
	}
	dispatchDone := make(chan error, 1)
	go func() { dispatchDone <- a.dispatcher.Run(runCtx) }()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- a.server.Serve(listener)
	}()
	a.logger.Info("workbench started", "component", "app", "address", listener.Addr().String(), "authMode", a.config.AuthMode, "publicURL", a.config.PublicURL)

	select {
	case <-ctx.Done():
	case err := <-serverDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
	case err := <-dispatchDone:
		if err != nil {
			return fmt.Errorf("event dispatcher: %w", err)
		}
	}
	cancelRun()
	a.logger.Info("workbench stopping", "component", "app")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.config.ShutdownGrace)
	defer cancel()
	a.closeLifecycles()
	_ = a.registry.Close()
	serverErr := a.server.Shutdown(shutdownCtx)
	schedulerErr := a.shutdownScheduler(shutdownCtx)
	if serverErr == nil && schedulerErr == nil {
		a.logger.Info("workbench stopped", "component", "app")
	}
	return errors.Join(serverErr, schedulerErr)
}

func (a *App) shutdownScheduler(ctx context.Context) error {
	a.schedulerOnce.Do(func() { a.schedulerErr = a.scheduler.Shutdown(ctx) })
	return a.schedulerErr
}

func (a *App) closeLifecycles() {
	for _, binding := range a.builtins {
		if binding.lifecycle != nil {
			binding.lifecycle.Close()
		}
	}
}

func (a *App) Close() error {
	var result error
	a.closeOnce.Do(func() {
		a.closeLifecycles()
		result = errors.Join(a.registry.Close(), a.shutdownScheduler(context.Background()), a.database.Close())
	})
	return result
}
