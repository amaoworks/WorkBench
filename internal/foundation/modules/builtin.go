package modules

import (
	"context"
	"net/http"
	"sync"

	"workbench/internal/contracts"
	dbsqlc "workbench/internal/foundation/database/sqlc"
)

// ctrl serializes persistence and lifecycle effects for one module. State fields
// are protected by Registry.mu so status reads never wait for a slow callback.
type builtinModule struct {
	ctrl      sync.Mutex
	lifecycle contracts.ModuleLifecycle
	observed  *bool
	pending   bool
	lastError string
}

func (r *Registry) SetEnabled(ctx context.Context, id contracts.ModuleID, enabled bool) error {
	r.mu.RLock()
	builtin := r.builtins[id]
	r.mu.RUnlock()
	if builtin == nil {
		return apiError(http.StatusNotFound, "module_not_found", "模块不存在")
	}
	builtin.ctrl.Lock()
	defer builtin.ctrl.Unlock()
	if r.closed.Load() {
		return apiError(http.StatusServiceUnavailable, "module_registry_closed", "模块管理已关闭")
	}
	value := int64(0)
	if enabled {
		value = 1
	}
	rows, err := r.queries.SetModuleEnabled(ctx, dbsqlc.SetModuleEnabledParams{
		Enabled: value, UpdatedAt: r.now().UnixMilli(), ID: string(id),
	})
	if err != nil || rows != 1 {
		return apiError(http.StatusInternalServerError, "module_state_failed", "无法保存模块状态")
	}
	// Persist intent first; a failed callback must neither reopen the gate nor
	// pretend to have rolled back potentially partial external side effects.
	return r.applyBuiltin(ctx, id, builtin, enabled)
}

func (r *Registry) applyBuiltin(ctx context.Context, id contracts.ModuleID, builtin *builtinModule, enabled bool) error {
	r.mu.Lock()
	r.enabled[id] = enabled
	builtin.pending, builtin.observed, builtin.lastError = true, nil, ""
	r.mu.Unlock()
	var err error
	if builtin.lifecycle != nil {
		err = builtin.lifecycle.OnEnabledChanged(ctx, enabled)
	}
	r.mu.Lock()
	builtin.pending = false
	if err != nil {
		builtin.lastError = "模块状态应用失败，业务入口已关闭，请重试"
	} else {
		observed := enabled
		builtin.observed = &observed
	}
	r.mu.Unlock()
	if err != nil {
		r.logger.Error("module lifecycle failed", "module", id, "enabled", enabled, "error", err)
		return apiError(http.StatusServiceUnavailable, "module_lifecycle_failed", "模块状态应用失败，业务入口已关闭，请重试")
	}
	return nil
}

// Caller holds Registry.mu for reading.
func (r *Registry) listedBuiltin(manifest contracts.ModuleManifest) ListedModule {
	state := r.builtins[manifest.ID]
	health := contracts.HealthUnknown
	if state.lastError != "" {
		health = contracts.HealthDegraded
	} else if state.observed != nil && *state.observed && !state.pending {
		health = contracts.HealthReady
	}
	return ListedModule{
		ID: manifest.ID, Kind: KindBuiltin, Name: manifest.Name, Version: manifest.Version,
		ContractVersion: manifest.ContractVersion, Icon: manifest.Icon,
		Navigation: append([]contracts.NavigationItem{}, manifest.Navigation...),
		Enabled:    r.enabled[manifest.ID], ObservedEnabled: copyBool(state.observed),
		Health: health, Pending: state.pending, LastError: state.lastError,
	}
}
