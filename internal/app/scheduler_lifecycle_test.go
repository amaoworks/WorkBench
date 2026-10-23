package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"workbench/internal/capabilities/scheduler"
	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
	"workbench/internal/foundation/events"
	"workbench/internal/foundation/modules"
)

type schedulerLifecycleModule struct {
	contractLifecycleModule
	job    contracts.JobDefinition
	closed chan struct{}
}

func (m *schedulerLifecycleModule) Register(r contracts.ModuleRegistrar) error { return r.Job(m.job) }
func (m *schedulerLifecycleModule) Close()                                     { close(m.closed) }

func lifecycleReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for application lifecycle barrier")
		var zero T
		return zero
	}
}

func newSchedulerLifecycleApp(t *testing.T, handler func(context.Context, contracts.JobRun) error) (*App, *schedulerLifecycleModule) {
	t.Helper()
	ctx := context.Background()
	database, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	module := &schedulerLifecycleModule{closed: make(chan struct{}), job: contracts.JobDefinition{
		ID: "fixture.lifecycle", Module: "fixture",
		Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: time.Hour},
		TimeZone: "UTC", Timeout: time.Minute, OverlapPolicy: contracts.OverlapSkip,
		MisfirePolicy: contracts.MisfireRunOnce, Retry: contracts.RetryPolicy{MaxAttempts: 1}, Handler: handler,
	}}
	registry, err := modules.Initialize(ctx, database, []contracts.Module{module})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	jobs := registry.Catalog().Jobs
	first, err := scheduler.New(ctx, database.SQL(), jobs, registry.IsEnabled, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().Exec("UPDATE scheduled_jobs SET next_run_at = ?", time.Now().Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	scheduled, err := scheduler.New(ctx, database.SQL(), jobs, registry.IsEnabled, logger)
	if err != nil {
		t.Fatal(err)
	}
	store := events.NewStore(database.SQL())
	dispatcher, err := events.NewDispatcher(database.SQL(), store, nil, registry.IsEnabled, logger)
	if err != nil {
		t.Fatal(err)
	}
	telegramDispatcher, err := events.NewDispatcher(database.SQL(), store, nil, registry.IsEnabled, logger)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{
		config: Config{ListenAddress: "127.0.0.1:0", ShutdownGrace: 3 * time.Second}, logger: logger,
		database: database, registry: registry, scheduler: scheduled,
		dispatcher: dispatcher, telegramDispatcher: telegramDispatcher, server: &http.Server{},
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, module
}

func TestAppCloseWaitsForSchedulerBeforeClosingResources(t *testing.T) {
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	cleaned := make(chan error, 1)
	var a *App
	var module *schedulerLifecycleModule
	a, module = newSchedulerLifecycleApp(t, func(ctx context.Context, _ contracts.JobRun) error {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		select {
		case <-module.closed:
			cleaned <- errors.New("module closed before its scheduled job finished")
		default:
			cleaned <- a.DB().PingContext(context.Background())
		}
		return ctx.Err()
	})
	t.Cleanup(func() { close(release) })
	if err := a.scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	lifecycleReceive(t, entered)
	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	lifecycleReceive(t, canceled)
	select {
	case <-module.closed:
		t.Fatal("Close released the module before scheduled job cleanup")
	default:
	}
	if err := a.DB().PingContext(context.Background()); err != nil {
		t.Fatalf("database closed while the job was still cleaning up: %v", err)
	}
	release <- struct{}{}
	if err := lifecycleReceive(t, cleaned); err != nil {
		t.Fatal(err)
	}
	if err := lifecycleReceive(t, closed); err != nil {
		t.Fatal(err)
	}
	lifecycleReceive(t, module.closed)
	if err := a.DB().PingContext(context.Background()); err == nil {
		t.Fatal("Close left the database open")
	}
}

func TestAppRunStopsSchedulerBeforeModulesAndLongLivedHTTP(t *testing.T) {
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a, module := newSchedulerLifecycleApp(t, func(ctx context.Context, _ contracts.JobRun) error {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	})
	t.Cleanup(func() { close(release) })
	address := make(chan string, 1)
	a.server.BaseContext = func(listener net.Listener) context.Context {
		address <- "http://" + listener.Addr().String()
		return context.Background()
	}
	requestEntered := make(chan struct{})
	// Module-owned long-lived handlers must be released by Registry.Close before HTTP shutdown waits.
	a.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestEntered)
		<-module.closed
		w.WriteHeader(http.StatusOK)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(ctx) }()
	url := lifecycleReceive(t, address)
	lifecycleReceive(t, entered)
	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		response, err := client.Get(url)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				err = errors.New("long-lived request failed during shutdown")
			}
		}
		requestDone <- err
	}()
	lifecycleReceive(t, requestEntered)
	cancel()
	lifecycleReceive(t, canceled)
	select {
	case <-module.closed:
		t.Fatal("Run closed modules before scheduled job cleanup")
	default:
	}
	release <- struct{}{}
	if err := lifecycleReceive(t, runDone); err != nil {
		t.Fatal(err)
	}
	if err := lifecycleReceive(t, requestDone); err != nil {
		t.Fatal(err)
	}
	lifecycleReceive(t, module.closed)
	if err := a.DB().PingContext(context.Background()); err != nil {
		t.Fatalf("Run closed the database before App.Close: %v", err)
	}
}
