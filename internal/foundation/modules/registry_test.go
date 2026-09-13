package modules

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
)

type testModule struct {
	id       contracts.ModuleID
	register func(contracts.ModuleRegistrar) error
}

func (m testModule) Manifest() contracts.ModuleManifest {
	return contracts.ModuleManifest{ID: m.id, Name: string(m.id), Version: "0.1.0", ContractVersion: 1}
}

func (m testModule) Migrations() contracts.MigrationSet { return contracts.MigrationSet{} }

func (m testModule) Register(registrar contracts.ModuleRegistrar) error {
	if m.register != nil {
		return m.register(registrar)
	}
	return nil
}

func TestRegistryPersistsEnabledStateAndGatesRoutes(t *testing.T) {
	ctx := context.Background()
	database, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}

	module := testModule{id: "todo", register: func(r contracts.ModuleRegistrar) error {
		return r.Handle(http.MethodGet, "/api/modules/todo/tasks", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
	}}
	registry, err := Initialize(ctx, database, []contracts.Module{module})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if !registry.IsEnabled("todo") {
		t.Fatal("new module should be enabled")
	}
	if err := registry.SetEnabled(ctx, "todo", false); err != nil {
		t.Fatal(err)
	}

	route := registry.Catalog().Routes[0]
	recorder := httptest.NewRecorder()
	registry.Gate("todo", route.Handler).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, route.Pattern, nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled route status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}

	reloaded, err := Initialize(ctx, database, []contracts.Module{module})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	if reloaded.IsEnabled("todo") {
		t.Fatal("disabled state was not preserved across initialization")
	}
}

func TestRegistryRejectsDuplicateResources(t *testing.T) {
	ctx := context.Background()
	database, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}

	module := testModule{id: "todo", register: func(r contracts.ModuleRegistrar) error {
		handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
		if err := r.Handle(http.MethodGet, "/api/modules/todo/tasks", handler); err != nil {
			return err
		}
		return r.Handle(http.MethodGet, "/api/modules/todo/tasks", handler)
	}}
	if _, err := Initialize(ctx, database, []contracts.Module{module}); err == nil {
		t.Fatal("Initialize() succeeded with duplicate route")
	}
	var count int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM modules WHERE id = 'todo'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed registration persisted %d module rows, want 0", count)
	}
}

func TestRegistryRejectsUnversionedAndCrossModuleResources(t *testing.T) {
	tests := []struct {
		name     string
		register func(contracts.ModuleRegistrar) error
	}{
		{
			name: "unversioned tool",
			register: func(r contracts.ModuleRegistrar) error {
				return r.Tool(contracts.AITool{Name: "todo.create_task", Module: "todo", Handler: func(context.Context, contracts.ToolCall) (contracts.ToolResult, error) {
					return contracts.ToolResult{}, nil
				}})
			},
		},
		{
			name: "cross-module widget route",
			register: func(r contracts.ModuleRegistrar) error {
				return r.Widget(contracts.WidgetDefinition{ID: "todo.summary", Module: "todo", SchemaVersion: 1, WidgetKind: "todo.summary", DataRoute: "/api/modules/investment/summary"})
			},
		},
		{
			name: "foreign job id",
			register: func(r contracts.ModuleRegistrar) error {
				return r.Job(contracts.JobDefinition{ID: "investment.sync", Module: "todo", Handler: func(context.Context, contracts.JobRun) error { return nil }})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module := testModule{id: "todo", register: test.register}
			_, err := buildCatalog([]contracts.Module{module})
			if err == nil {
				t.Fatal("invalid resource registration succeeded")
			}
		})
	}
}
