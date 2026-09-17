package modules

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
)

type lifecycleModule struct {
	testModule
	change func(context.Context, bool) error
	closes atomic.Int32
}

func (m *lifecycleModule) OnEnabledChanged(ctx context.Context, enabled bool) error {
	if m.change != nil {
		return m.change(ctx, enabled)
	}
	return nil
}
func (m *lifecycleModule) Close() { m.closes.Add(1) }

func lifecycleDB(t *testing.T) *workbenchdb.Database {
	t.Helper()
	db, err := workbenchdb.Open(context.Background(), workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestBuiltinLifecycleRestoresEveryModuleAndClosesOnce(t *testing.T) {
	db := lifecycleDB(t)
	first := &lifecycleModule{testModule: testModule{id: "first"}}
	second := &lifecycleModule{testModule: testModule{id: "second"}}
	definitions := []contracts.Module{first, second}
	registry, err := Initialize(context.Background(), db, definitions)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.SetEnabled(context.Background(), "second", false); err != nil {
		t.Fatal(err)
	}
	_ = registry.Close()
	_ = registry.Close()
	if first.closes.Load() != 1 || second.closes.Load() != 1 {
		t.Fatal("resources closed more than once")
	}
	states := map[string]bool{}
	first.change = func(_ context.Context, value bool) error { states["first"] = value; return nil }
	second.change = func(_ context.Context, value bool) error { states["second"] = value; return nil }
	restored, err := Initialize(context.Background(), db, definitions)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if len(states) != 2 || !states["first"] || states["second"] {
		t.Fatalf("restored = %v", states)
	}
}

func TestBuiltinInitializationFailureClosesAllResources(t *testing.T) {
	for _, phase := range []string{"registration", "restore"} {
		t.Run(phase, func(t *testing.T) {
			first := &lifecycleModule{testModule: testModule{id: "first"}}
			second := &lifecycleModule{testModule: testModule{id: "second"}}
			third := &lifecycleModule{testModule: testModule{id: "third"}}
			failure := errors.New("fixture failure")
			if phase == "registration" {
				second.register = func(contracts.ModuleRegistrar) error { return failure }
			} else {
				second.change = func(context.Context, bool) error { return failure }
			}
			_, err := Initialize(context.Background(), lifecycleDB(t), []contracts.Module{first, second, third})
			if err == nil {
				t.Fatal("expected initialization failure")
			}
			for _, module := range []*lifecycleModule{first, second, third} {
				if module.closes.Load() != 1 {
					t.Fatalf("%s not cleaned up exactly once", module.id)
				}
			}
		})
	}
}

func TestBuiltinFailureClosesGateAndCanRetrySameIntent(t *testing.T) {
	for _, desired := range []bool{true, false} {
		t.Run(map[bool]string{true: "enable", false: "disable"}[desired], func(t *testing.T) {
			db := lifecycleDB(t)
			module := &lifecycleModule{testModule: testModule{id: "sample"}}
			registry, err := Initialize(context.Background(), db, []contracts.Module{module})
			if err != nil {
				t.Fatal(err)
			}
			defer registry.Close()
			if err := registry.SetEnabled(context.Background(), module.id, !desired); err != nil {
				t.Fatal(err)
			}
			module.change = func(context.Context, bool) error { return errors.New("private backend failure") }
			err = registry.SetEnabled(context.Background(), module.id, desired)
			var apiErr *Error
			if !errors.As(err, &apiErr) || apiErr.Status != http.StatusServiceUnavailable {
				t.Fatalf("error = %v", err)
			}
			listed, _ := registry.GetListed(module.id)
			if listed.Enabled != desired || listed.ObservedEnabled != nil || listed.Pending || listed.Health != contracts.HealthDegraded || listed.LastError == "" {
				t.Fatalf("failure state = %+v", listed)
			}
			var persisted bool
			if err := db.SQL().QueryRow("SELECT enabled FROM modules WHERE id = ?", module.id).Scan(&persisted); err != nil || persisted != desired {
				t.Fatalf("intent not persisted: %v", err)
			}
			recorder := httptest.NewRecorder()
			registry.Gate(module.id, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("failed module was called") })).ServeHTTP(recorder, httptest.NewRequest("GET", "/", nil))
			if recorder.Code != http.StatusServiceUnavailable || registry.IsEnabled(module.id) {
				t.Fatal("failed module gate open")
			}
			module.change = nil
			if err := registry.SetEnabled(context.Background(), module.id, desired); err != nil {
				t.Fatal(err)
			}
			listed, _ = registry.GetListed(module.id)
			if listed.ObservedEnabled == nil || *listed.ObservedEnabled != desired || listed.LastError != "" || registry.IsEnabled(module.id) != desired {
				t.Fatalf("retry state = %+v", listed)
			}
		})
	}
}

func TestBuiltinChangesSerializeAndRemainGatedWhilePending(t *testing.T) {
	module := &lifecycleModule{testModule: testModule{id: "sample"}}
	registry, err := Initialize(context.Background(), lifecycleDB(t), []contracts.Module{module})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	var active atomic.Int32
	module.change = func(_ context.Context, enabled bool) error {
		if active.Add(1) != 1 {
			t.Error("concurrent callbacks")
		}
		defer active.Add(-1)
		if !enabled {
			close(entered)
			<-release
		}
		return nil
	}
	disabled, enabled := make(chan error, 1), make(chan error, 1)
	go func() { disabled <- registry.SetEnabled(context.Background(), module.id, false) }()
	<-entered
	state, _ := registry.GetListed(module.id)
	if !state.Pending || state.ObservedEnabled != nil || registry.IsEnabled(module.id) {
		t.Error("pending state falsely ready")
	}
	go func() { enabled <- registry.SetEnabled(context.Background(), module.id, true) }()
	close(release)
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	if err := <-enabled; err != nil {
		t.Fatal(err)
	}
	if !registry.IsEnabled(module.id) {
		t.Fatal("last change lost")
	}
	_ = registry.Close()
	if err := registry.SetEnabled(context.Background(), module.id, false); err == nil {
		t.Fatal("closed registry accepted change")
	}
}
