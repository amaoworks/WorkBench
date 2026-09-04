package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
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
	"workbench/internal/foundation/modules"
	"workbench/internal/modules/todo"
	"workbench/internal/webui"
)

type App struct {
	config        Config
	logger        *slog.Logger
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
}

func New(ctx context.Context, cfg Config, logger *slog.Logger) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
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
	registry, err := modules.Initialize(ctx, database, []contracts.Module{todoModule})
	if err != nil {
		return fail(err)
	}

	hub := notifications.NewHub()
	consumers := append(registry.Catalog().Consumers, hub.Consumer())
	enabled := func(id contracts.ModuleID) bool { return id == "core" || registry.IsEnabled(id) }
	dispatcher, err := events.NewDispatcher(database.SQL(), eventStore, consumers, enabled, logger)
	if err != nil {
		return fail(err)
	}
	jobs := append(registry.Catalog().Jobs, maintenanceJob(database.SQL()))
	scheduled, err := scheduler.New(ctx, database.SQL(), jobs, enabled, logger)
	if err != nil {
		return fail(err)
	}
	authService, err := auth.New(ctx, database.SQL(), auth.Config{
		Mode: cfg.AuthMode, ListenAddress: cfg.ListenAddress, PublicHTTPS: cfg.HTTPS(),
		InitialPassword: cfg.Password, AllowedHosts: cfg.AllowedHosts,
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
	dashboardService := dashboard.New(registry.Catalog().Widgets, registry.IsEnabled)
	notificationHTTP := notifications.NewHTTPHandler(notificationService, hub)
	toolRuntime, err := ai.NewToolRuntime(database.SQL(), registry.Catalog().Tools, registry.IsEnabled)
	if err != nil {
		_ = scheduled.Shutdown(context.Background())
		return fail(err)
	}
	var aiProvider contracts.AIProvider
	if cfg.OpenAIAPIKey != "" {
		provider, providerErr := ai.NewOpenAIProvider(ai.OpenAIConfig{
			APIKey: cfg.OpenAIAPIKey, BaseURL: cfg.OpenAIBaseURL,
			Profiles: map[string]ai.Profile{"default": {Model: cfg.OpenAIModel, MaxOutputTokens: 2048}},
		})
		if providerErr != nil {
			_ = scheduled.Shutdown(context.Background())
			return fail(providerErr)
		}
		aiProvider = provider
	}
	gateway, err := ai.NewGateway(aiProvider, toolRuntime)
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
		config: cfg, logger: logger, database: database, registry: registry,
		dispatcher: dispatcher, scheduler: scheduled, auth: authService,
	}
	application.handler = application.routes(dashboardService, notificationHTTP, conversationHTTP, ui)
	application.server = &http.Server{
		Addr: cfg.ListenAddress, Handler: application.handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 0, IdleTimeout: 2 * time.Minute,
	}
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
	router.Use(middleware.Recoverer)
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

	router.Group(func(protected chi.Router) {
		protected.Use(a.auth.Require)
		protected.Get("/api/modules", a.listModules)
		protected.Put("/api/modules/{id}/enabled", a.setModuleEnabled)
		protected.Get("/api/dashboard", dashboardService.ServeHTTP)
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
		for _, route := range a.registry.Catalog().Routes {
			protected.Method(route.Method, route.Pattern, a.registry.Gate(route.Module, route.Handler))
		}
	})
	router.NotFound(ui.ServeHTTP)
	return a.auth.Security(a.auth.LoadAndSave(router))
}

func (a *App) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := middleware.GetReqID(r.Context())
		if requestID != "" {
			w.Header().Set("X-Request-ID", requestID)
		}
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		started := time.Now()
		next.ServeHTTP(wrapped, r)
		a.logger.Info("HTTP request",
			"requestId", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.Status(),
			"bytes", wrapped.BytesWritten(),
			"durationMs", time.Since(started).Milliseconds(),
		)
	})
}

func (a *App) listModules(w http.ResponseWriter, _ *http.Request) {
	type moduleInfo struct {
		contracts.ModuleManifest
		Enabled bool `json:"enabled"`
	}
	items := make([]moduleInfo, 0, len(a.registry.Catalog().Manifests))
	for _, manifest := range a.registry.Catalog().Manifests {
		items = append(items, moduleInfo{ModuleManifest: manifest, Enabled: a.registry.IsEnabled(manifest.ID)})
	}
	httpapi.Write(w, http.StatusOK, map[string]any{"items": items})
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
	if err := a.registry.SetEnabled(r.Context(), id, *input.Enabled); err != nil {
		httpapi.Error(w, http.StatusNotFound, "module_not_found", err.Error())
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]any{"id": id, "enabled": *input.Enabled})
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
		a.logger.Error("online backup failed", "error", err)
		httpapi.Error(w, http.StatusInternalServerError, "backup_failed", "could not create backup")
		return
	}
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
		var serveErr error
		if a.config.HTTPS() {
			serveErr = a.server.ServeTLS(listener, a.config.TLSCertFile, a.config.TLSKeyFile)
		} else {
			serveErr = a.server.Serve(listener)
		}
		serverDone <- serveErr
	}()
	a.logger.Info("workbench started", "address", a.config.ListenAddress, "authMode", a.config.AuthMode, "https", a.config.HTTPS())

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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.config.ShutdownGrace)
	defer cancel()
	serverErr := a.server.Shutdown(shutdownCtx)
	schedulerErr := a.shutdownScheduler(shutdownCtx)
	return errors.Join(serverErr, schedulerErr)
}

func (a *App) shutdownScheduler(ctx context.Context) error {
	a.schedulerOnce.Do(func() { a.schedulerErr = a.scheduler.Shutdown(ctx) })
	return a.schedulerErr
}

func (a *App) Close() error {
	var result error
	a.closeOnce.Do(func() {
		result = errors.Join(a.shutdownScheduler(context.Background()), a.database.Close())
	})
	return result
}
