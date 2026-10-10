package app

import (
	"context"
	"testing"

	"workbench/internal/contracts"
)

type startupTestModule struct {
	contractLifecycleModule
	start func(context.Context)
}

func (m *startupTestModule) OnStarted(ctx context.Context) { m.start(ctx) }

func TestModuleStartupUsesDeclaredHookAndRunContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var observed context.Context
	module := &startupTestModule{start: func(ctx context.Context) { observed = ctx }}
	a := &App{builtins: []builtinBinding{
		{module: &contractLifecycleModule{}},
		{module: module},
	}}
	a.startModules(ctx)
	if observed != ctx {
		t.Fatal("startup hook did not receive the Run context")
	}
	cancel()
	if observed.Err() != context.Canceled {
		t.Fatal("module startup work outlived the Run context")
	}
	var _ contracts.ModuleStartup = module
}
