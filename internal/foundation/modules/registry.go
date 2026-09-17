package modules

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
	dbsqlc "workbench/internal/foundation/database/sqlc"
)

const supportedContractVersion = 1

var moduleIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type Timeouts struct {
	Control       time.Duration
	ProbeInterval time.Duration
	RetryMax      time.Duration
	MaxProbes     int
}

func DefaultTimeouts() Timeouts {
	return Timeouts{
		Control:       5 * time.Second,
		ProbeInterval: 30 * time.Second,
		RetryMax:      30 * time.Second,
		MaxProbes:     4,
	}
}

func (t Timeouts) withDefaults() Timeouts {
	defaults := DefaultTimeouts()
	if t.Control <= 0 {
		t.Control = defaults.Control
	}
	if t.ProbeInterval <= 0 {
		t.ProbeInterval = defaults.ProbeInterval
	}
	if t.RetryMax <= 0 {
		t.RetryMax = defaults.RetryMax
	}
	if t.MaxProbes <= 0 {
		t.MaxProbes = defaults.MaxProbes
	}
	return t
}

type Options struct {
	Clock      func() time.Time
	Timeouts   Timeouts
	HTTPClient *http.Client
	Logger     *slog.Logger
}

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
	db           *sql.DB
	queries      *dbsqlc.Queries
	catalog      Catalog
	mu           sync.RWMutex
	enabled      map[contracts.ModuleID]bool
	builtinOrder []contracts.ModuleID
	builtins     map[contracts.ModuleID]*builtinModule
	external     map[contracts.ModuleID]*externalModule

	clock    func() time.Time
	timeouts Timeouts
	client   *http.Client
	logger   *slog.Logger

	probeSem   chan struct{}
	bgCtx      context.Context
	bgCancel   context.CancelFunc
	wg         sync.WaitGroup
	closed     atomic.Bool
	sockets    *socketSet
	transports *proxyTransports
}

func Initialize(ctx context.Context, database *workbenchdb.Database, definitions []contracts.Module) (*Registry, error) {
	return InitializeWith(ctx, database, definitions, Options{})
}

func InitializeWith(ctx context.Context, database *workbenchdb.Database, definitions []contracts.Module, opts Options) (*Registry, error) {
	// Ownership of constructed module resources transfers to the registry, even
	// when validation or initialization fails.
	ready := false
	defer func() {
		if !ready {
			for i := len(definitions) - 1; i >= 0; i-- {
				if lifecycle, ok := definitions[i].(contracts.ModuleLifecycle); ok {
					lifecycle.Close()
				}
			}
		}
	}()
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
		rows, err := queries.UpsertBuiltinModule(ctx, dbsqlc.UpsertBuiltinModuleParams{
			ID: string(manifest.ID), Name: manifest.Name, Version: manifest.Version,
			ContractVersion: int64(manifest.ContractVersion), InstalledAt: now, UpdatedAt: now,
		})
		if err != nil {
			return nil, fmt.Errorf("sync module %q: %w", manifest.ID, err)
		}
		if rows != 1 {
			return nil, fmt.Errorf("module id %q conflicts with an external module", manifest.ID)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit module sync: %w", err)
	}

	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	timeouts := opts.Timeouts.withDefaults()
	bgCtx, bgCancel := context.WithCancel(context.Background())
	registry := &Registry{
		db:         database.SQL(),
		queries:    dbsqlc.New(database.SQL()),
		catalog:    catalog,
		enabled:    make(map[contracts.ModuleID]bool, len(catalog.Manifests)),
		builtins:   make(map[contracts.ModuleID]*builtinModule, len(definitions)),
		external:   make(map[contracts.ModuleID]*externalModule),
		clock:      clock,
		timeouts:   timeouts,
		client:     opts.HTTPClient,
		logger:     logger,
		probeSem:   make(chan struct{}, timeouts.MaxProbes),
		bgCtx:      bgCtx,
		bgCancel:   bgCancel,
		sockets:    newSocketSet(),
		transports: newProxyTransports(),
	}
	if err := registry.reloadEnabled(ctx); err != nil {
		bgCancel()
		return nil, err
	}
	for _, definition := range definitions {
		id := definition.Manifest().ID
		lifecycle, _ := definition.(contracts.ModuleLifecycle)
		registry.builtins[id] = &builtinModule{lifecycle: lifecycle}
		registry.builtinOrder = append(registry.builtinOrder, id)
		if err := registry.applyBuiltin(ctx, id, registry.builtins[id], registry.enabled[id]); err != nil {
			bgCancel()
			return nil, fmt.Errorf("restore module %q: %w", id, err)
		}
	}
	if err := registry.loadExternals(ctx); err != nil {
		bgCancel()
		return nil, err
	}
	registry.wg.Add(1)
	go registry.probeLoop()
	ready = true
	return registry, nil
}

func (r *Registry) Close() error {
	if r == nil {
		return nil
	}
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	if r.bgCancel != nil {
		r.bgCancel()
	}
	r.sockets.closeAll()
	r.transports.closeAll()
	r.wg.Wait()
	for i := len(r.builtinOrder) - 1; i >= 0; i-- {
		builtin := r.builtins[r.builtinOrder[i]]
		if builtin == nil {
			continue
		}
		builtin.ctrl.Lock()
		if builtin.lifecycle != nil {
			builtin.lifecycle.Close()
		}
		builtin.ctrl.Unlock()
	}
	return nil
}

func (r *Registry) SetTimeouts(t Timeouts) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timeouts = t.withDefaults()
}

func (r *Registry) currentTimeouts() Timeouts {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.timeouts
}

func (r *Registry) now() time.Time {
	if r.clock != nil {
		return r.clock()
	}
	return time.Now().UTC()
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
	if r.closed.Load() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if enabled, ok := r.enabled[id]; ok {
		state := r.builtins[id]
		return enabled && state != nil && state.observed != nil && *state.observed && !state.pending && state.lastError == ""
	}
	if ext, ok := r.external[id]; ok {
		return ext.rec.Enabled
	}
	return false
}

func (r *Registry) IsExternal(id contracts.ModuleID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.external[id]
	return ok
}

func (r *Registry) IsBuiltin(id contracts.ModuleID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.enabled[id]
	return ok
}

func (r *Registry) List() []ListedModule {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]ListedModule, 0, len(r.catalog.Manifests)+len(r.external))
	for _, manifest := range r.catalog.Manifests {
		items = append(items, r.listedBuiltin(manifest))
	}
	for _, ext := range r.external {
		items = append(items, ext.listed())
	}
	slices.SortFunc(items, func(a, b ListedModule) int {
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return items
}

func (r *Registry) GetListed(id contracts.ModuleID) (ListedModule, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ext, ok := r.external[id]; ok {
		return ext.listed(), true
	}
	for _, manifest := range r.catalog.Manifests {
		if manifest.ID == id {
			return r.listedBuiltin(manifest), true
		}
	}
	return ListedModule{}, false
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
		if row.Kind == "external" || id == reservedModuleID {
			continue
		}
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
