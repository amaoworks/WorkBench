package todo

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
	"workbench/internal/modules/todo/sqlc"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Module struct {
	db            *sql.DB
	events        contracts.EventPublisher
	notifications contracts.NotificationService
	queries       *todosqlc.Queries
	now           func() time.Time
	wallosMu      sync.Mutex
}

func New(db *sql.DB, events contracts.EventPublisher, notifications contracts.NotificationService) (*Module, error) {
	if db == nil || events == nil || notifications == nil {
		return nil, errors.New("database, events, and notifications are required")
	}
	return &Module{db: db, events: events, notifications: notifications, queries: todosqlc.New(db), now: time.Now}, nil
}

func (m *Module) Manifest() contracts.ModuleManifest {
	return contracts.ModuleManifest{
		ID: "todo", Name: "待办", Version: "0.1.0", ContractVersion: 1, Icon: "check-square",
		Navigation: []contracts.NavigationItem{{Label: "待办", Route: "/todo", PageKey: "todo.list", Order: 10}},
	}
}

func (m *Module) Migrations() contracts.MigrationSet {
	return contracts.MigrationSet{FS: migrations, Dir: "migrations"}
}

func (m *Module) Register(r contracts.ModuleRegistrar) error {
	registrations := []error{
		r.Handle(http.MethodGet, "/api/modules/todo/wallos", http.HandlerFunc(m.getWallos)),
		r.Handle(http.MethodPut, "/api/modules/todo/wallos", http.HandlerFunc(m.saveWallos)),
		r.Handle(http.MethodPost, "/api/modules/todo/wallos/sync", http.HandlerFunc(m.syncWallosHTTP)),
		r.Job(contracts.JobDefinition{
			ID: "todo.sync_wallos", Module: "todo",
			Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: time.Hour},
			TimeZone: "UTC", Timeout: 45 * time.Second,
			OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce,
			Retry:   contracts.RetryPolicy{MaxAttempts: 3, InitialWait: time.Minute, MaxWait: 5 * time.Minute},
			Handler: func(ctx context.Context, _ contracts.JobRun) error { _, err := m.syncWallos(ctx); return err },
		}),
		r.Handle(http.MethodGet, "/api/modules/todo/tasks", http.HandlerFunc(m.listTasks)),
		r.Handle(http.MethodPost, "/api/modules/todo/tasks", http.HandlerFunc(m.createTask)),
		r.Handle(http.MethodPatch, "/api/modules/todo/tasks/{id}", http.HandlerFunc(m.updateTask)),
		r.Handle(http.MethodDelete, "/api/modules/todo/tasks/{id}", http.HandlerFunc(m.deleteTask)),
		r.Handle(http.MethodGet, "/api/modules/todo/widget/summary", http.HandlerFunc(m.summary)),
		r.Widget(contracts.WidgetDefinition{
			ID: "todo.summary", Module: "todo", SchemaVersion: 1, Title: "待办概览", WidgetKind: "todo.summary",
			DataRoute: "/api/modules/todo/widget/summary", Size: contracts.WidgetSmall, Order: 10,
		}),
		r.Job(contracts.JobDefinition{
			ID: "todo.scan_due", Module: "todo",
			Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: time.Minute},
			TimeZone: "UTC", Timeout: 30 * time.Second,
			OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce,
			Retry:   contracts.RetryPolicy{MaxAttempts: 3, InitialWait: time.Second, MaxWait: 30 * time.Second},
			Handler: m.scanDue,
		}),
		r.Tool(contracts.AITool{
			Name: "todo.create_task", Module: "todo", SchemaVersion: 1, Description: "创建一条待办任务",
			ParametersSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"description":{"type":["string","null"]},"dueAt":{"type":["string","null"],"format":"date-time"}},"required":["title","description","dueAt"],"additionalProperties":false}`),
			Risk:             contracts.ToolRiskLowWrite, Handler: m.createTaskTool,
		}),
	}
	return errors.Join(registrations...)
}
