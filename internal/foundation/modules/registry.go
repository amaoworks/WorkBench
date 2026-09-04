package modules

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
	"workbench/internal/foundation/database/sqlc"
)

const supportedContractVersion = 1

var moduleIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type Route struct {
	Module  contracts.ModuleID
	Method  string
	Pattern string
	Handler http.Handler
}

type Catalog struct {
	Manifests []contracts.ModuleManifest
	Routes    []Route
	Consumers []contracts.EventConsumer
	Tools     []contracts.AITool
	Widgets   []contracts.WidgetDefinition
	Jobs      []contracts.JobDefinition
}

type Registry struct {
	queries *dbsqlc.Queries
	catalog Catalog
	mu      sync.RWMutex
	enabled map[contracts.ModuleID]bool
}

func Initialize(ctx context.Context, database *workbenchdb.Database, definitions []contracts.Module) (*Registry, error) {
	if database == nil {
		return nil, errors.New("database is required")
	}
	migrations, err := collectMigrations(definitions)
	if err != nil {
		return nil, err
	}
	for _, migration := range migrations {
		if err := database.MigrateModule(ctx, migration.id, migration.set); err != nil {
			return nil, fmt.Errorf("migrate module %q: %w", migration.id, err)
		}
	}
	catalog, err := buildCatalog(definitions)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().UnixMilli()
	tx, err := database.SQL().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin module sync: %w", err)
	}
	defer tx.Rollback()
	queries := dbsqlc.New(tx)
	for _, manifest := range catalog.Manifests {
		err := queries.UpsertModule(ctx, dbsqlc.UpsertModuleParams{
			ID: string(manifest.ID), Name: manifest.Name, Version: manifest.Version,
			ContractVersion: int64(manifest.ContractVersion), InstalledAt: now, UpdatedAt: now,
		})
		if err != nil {
			return nil, fmt.Errorf("sync module %q: %w", manifest.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit module sync: %w", err)
	}

	registry := &Registry{
		queries: dbsqlc.New(database.SQL()),
		catalog: catalog,
		enabled: make(map[contracts.ModuleID]bool, len(catalog.Manifests)),
	}
	if err := registry.reloadEnabled(ctx); err != nil {
		return nil, err
	}
	return registry, nil
}

type moduleMigration struct {
	id  contracts.ModuleID
	set contracts.MigrationSet
}

func collectMigrations(definitions []contracts.Module) ([]moduleMigration, error) {
	migrations := make([]moduleMigration, 0, len(definitions))
	moduleIDs := make(map[contracts.ModuleID]struct{}, len(definitions))
	for index, definition := range definitions {
		if definition == nil {
			return nil, fmt.Errorf("module at index %d is nil", index)
		}
		manifest := definition.Manifest()
		if err := validateManifest(manifest); err != nil {
			return nil, err
		}
		if _, exists := moduleIDs[manifest.ID]; exists {
			return nil, fmt.Errorf("duplicate module id %q", manifest.ID)
		}
		moduleIDs[manifest.ID] = struct{}{}
		migrations = append(migrations, moduleMigration{id: manifest.ID, set: definition.Migrations()})
	}
	return migrations, nil
}

func buildCatalog(definitions []contracts.Module) (Catalog, error) {
	var catalog Catalog
	moduleIDs := make(map[contracts.ModuleID]struct{}, len(definitions))
	resourceIDs := make(map[string]string)

	for index, definition := range definitions {
		if definition == nil {
			return Catalog{}, fmt.Errorf("module at index %d is nil", index)
		}
		manifest := definition.Manifest()
		if err := validateManifest(manifest); err != nil {
			return Catalog{}, err
		}
		if _, exists := moduleIDs[manifest.ID]; exists {
			return Catalog{}, fmt.Errorf("duplicate module id %q", manifest.ID)
		}
		moduleIDs[manifest.ID] = struct{}{}

		stage := &stagedRegistrar{module: manifest.ID, resourceIDs: resourceIDs}
		if err := definition.Register(stage); err != nil {
			return Catalog{}, fmt.Errorf("register module %q: %w", manifest.ID, err)
		}
		if err := stage.validate(); err != nil {
			return Catalog{}, fmt.Errorf("register module %q: %w", manifest.ID, err)
		}

		catalog.Manifests = append(catalog.Manifests, manifest)
		catalog.Routes = append(catalog.Routes, stage.routes...)
		catalog.Consumers = append(catalog.Consumers, stage.consumers...)
		catalog.Tools = append(catalog.Tools, stage.tools...)
		catalog.Widgets = append(catalog.Widgets, stage.widgets...)
		catalog.Jobs = append(catalog.Jobs, stage.jobs...)
	}

	slices.SortFunc(catalog.Manifests, func(a, b contracts.ModuleManifest) int {
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return catalog, nil
}

func validateManifest(manifest contracts.ModuleManifest) error {
	if !moduleIDPattern.MatchString(string(manifest.ID)) {
		return fmt.Errorf("invalid module id %q", manifest.ID)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return fmt.Errorf("module %q has an empty name", manifest.ID)
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return fmt.Errorf("module %q has an empty version", manifest.ID)
	}
	if manifest.ContractVersion != supportedContractVersion {
		return fmt.Errorf("module %q contract version %d is unsupported", manifest.ID, manifest.ContractVersion)
	}
	for _, item := range manifest.Navigation {
		if !strings.HasPrefix(item.Route, "/") || !strings.HasPrefix(item.PageKey, string(manifest.ID)+".") || item.Label == "" {
			return fmt.Errorf("module %q has invalid navigation", manifest.ID)
		}
	}
	return nil
}

func (r *Registry) Catalog() Catalog { return r.catalog }

func (r *Registry) IsEnabled(id contracts.ModuleID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enabled[id]
}

func (r *Registry) SetEnabled(ctx context.Context, id contracts.ModuleID, enabled bool) error {
	r.mu.RLock()
	_, known := r.enabled[id]
	r.mu.RUnlock()
	if !known {
		return fmt.Errorf("unknown module %q", id)
	}
	value := 0
	if enabled {
		value = 1
	}
	rows, err := r.queries.SetModuleEnabled(ctx, dbsqlc.SetModuleEnabledParams{
		Enabled: int64(value), UpdatedAt: time.Now().UTC().UnixMilli(), ID: string(id),
	})
	if err != nil {
		return fmt.Errorf("update module %q: %w", id, err)
	}
	if rows != 1 {
		return fmt.Errorf("module %q was not updated", id)
	}
	r.mu.Lock()
	r.enabled[id] = enabled
	r.mu.Unlock()
	return nil
}

func (r *Registry) reloadEnabled(ctx context.Context) error {
	rows, err := r.queries.ListModuleStates(ctx)
	if err != nil {
		return fmt.Errorf("load module states: %w", err)
	}
	known := make(map[contracts.ModuleID]struct{}, len(r.catalog.Manifests))
	for _, manifest := range r.catalog.Manifests {
		known[manifest.ID] = struct{}{}
	}
	states := make(map[contracts.ModuleID]bool, len(known))
	for _, row := range rows {
		id := contracts.ModuleID(row.ID)
		if _, exists := known[id]; exists {
			states[id] = row.Enabled != 0
		}
	}
	for id := range known {
		if _, exists := states[id]; !exists {
			return fmt.Errorf("module %q state is missing after sync", id)
		}
	}
	r.mu.Lock()
	r.enabled = states
	r.mu.Unlock()
	return nil
}

func (r *Registry) Gate(id contracts.ModuleID, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !r.IsEnabled(id) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"module_disabled","message":"module is disabled"}`))
			return
		}
		next.ServeHTTP(w, request)
	})
}

type stagedRegistrar struct {
	module      contracts.ModuleID
	resourceIDs map[string]string
	routes      []Route
	consumers   []contracts.EventConsumer
	tools       []contracts.AITool
	widgets     []contracts.WidgetDefinition
	jobs        []contracts.JobDefinition
	errs        []error
}

func (s *stagedRegistrar) reserve(kind, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%s id is empty", kind)
	}
	key := kind + ":" + id
	if owner, exists := s.resourceIDs[key]; exists {
		return fmt.Errorf("duplicate %s %q already registered by %s", kind, id, owner)
	}
	s.resourceIDs[key] = string(s.module)
	return nil
}

func (s *stagedRegistrar) Handle(method, pattern string, handler http.Handler) error {
	method = strings.ToUpper(strings.TrimSpace(method))
	expectedPrefix := "/api/modules/" + string(s.module)
	if method == "" || !strings.HasPrefix(pattern, expectedPrefix) || handler == nil {
		return fmt.Errorf("invalid route %s %q", method, pattern)
	}
	if err := s.reserve("route", method+" "+pattern); err != nil {
		return err
	}
	s.routes = append(s.routes, Route{Module: s.module, Method: method, Pattern: pattern, Handler: handler})
	return nil
}

func (s *stagedRegistrar) Consume(consumer contracts.EventConsumer) error {
	if consumer.Module != s.module || !strings.HasPrefix(string(consumer.ID), string(s.module)+".") || consumer.Handler == nil || len(consumer.Topics) == 0 {
		return fmt.Errorf("invalid consumer %q", consumer.ID)
	}
	if err := s.reserve("consumer", string(consumer.ID)); err != nil {
		return err
	}
	s.consumers = append(s.consumers, consumer)
	return nil
}

func (s *stagedRegistrar) Tool(tool contracts.AITool) error {
	if tool.Module != s.module || tool.SchemaVersion < 1 || tool.Handler == nil || !strings.HasPrefix(tool.Name, string(s.module)+".") {
		return fmt.Errorf("invalid AI tool %q", tool.Name)
	}
	if err := s.reserve("tool", tool.Name); err != nil {
		return err
	}
	s.tools = append(s.tools, tool)
	return nil
}

func (s *stagedRegistrar) Widget(widget contracts.WidgetDefinition) error {
	prefix := string(s.module) + "."
	routePrefix := "/api/modules/" + string(s.module) + "/"
	if widget.Module != s.module || widget.SchemaVersion < 1 || !strings.HasPrefix(widget.ID, prefix) ||
		!strings.HasPrefix(widget.WidgetKind, prefix) || !strings.HasPrefix(widget.DataRoute, routePrefix) {
		return fmt.Errorf("invalid widget %q", widget.ID)
	}
	if err := s.reserve("widget", widget.ID); err != nil {
		return err
	}
	s.widgets = append(s.widgets, widget)
	return nil
}

func (s *stagedRegistrar) Job(job contracts.JobDefinition) error {
	if job.Module != s.module || !strings.HasPrefix(string(job.ID), string(s.module)+".") || job.Handler == nil {
		return fmt.Errorf("invalid job %q", job.ID)
	}
	if err := s.reserve("job", string(job.ID)); err != nil {
		return err
	}
	s.jobs = append(s.jobs, job)
	return nil
}

func (s *stagedRegistrar) validate() error {
	return errors.Join(s.errs...)
}
