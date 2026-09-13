package modules

import (
	"context"
	"time"

	"workbench/internal/contracts"
	dbsqlc "workbench/internal/foundation/database/sqlc"
)

func (r *Registry) SetExternalEnabled(ctx context.Context, id contracts.ModuleID, enabled bool) (EnableResult, error) {
	ext, err := r.externalLocked(id)
	if err != nil {
		return EnableResult{}, err
	}
	ext.ctrl.Lock()
	defer ext.ctrl.Unlock()
	now := r.now().UnixMilli()
	nextGeneration := ext.rec.Generation + 1
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return EnableResult{}, apiError(500, "persist_failed", "无法保存模块状态")
	}
	defer tx.Rollback()
	q := dbsqlc.New(tx)
	rows, err := q.SetModuleEnabled(ctx, dbsqlc.SetModuleEnabledParams{
		Enabled: boolToInt(enabled), UpdatedAt: now, ID: string(id),
	})
	if err != nil || rows != 1 {
		return EnableResult{}, apiError(500, "persist_failed", "无法保存模块状态")
	}
	if err := q.UpdateExternalGeneration(ctx, dbsqlc.UpdateExternalGenerationParams{
		Generation: nextGeneration, UpdatedAt: now, ModuleID: string(id),
	}); err != nil {
		return EnableResult{}, apiError(500, "persist_failed", "无法保存模块状态")
	}
	if err := tx.Commit(); err != nil {
		return EnableResult{}, apiError(500, "persist_failed", "无法保存模块状态")
	}

	r.mu.Lock()
	ext.rec.Enabled = enabled
	ext.rec.Generation = nextGeneration
	snap := ext.rec
	r.mu.Unlock()
	if !enabled {
		r.sockets.closeModule(id)
	}
	r.logger.Info("external module intent saved", "component", "modules", "module", id, "enabled", enabled, "generation", nextGeneration)

	pending := r.applyRemoteState(ctx, ext, snap)
	return EnableResult{Module: r.listedLocked(ext), Pending: pending}, nil
}

func (r *Registry) applyRemoteState(ctx context.Context, ext *externalModule, snap externalRecord) bool {
	status, err := r.putState(ctx, snap)
	now := r.now().UnixMilli()
	if err != nil {
		r.persistObserved(ext, snap, contracts.ExternalStatus{}, err, now)
		r.scheduleReconcile(ext)
		return true
	}
	applied := r.acceptStatus(ext, snap, status, now)
	if !applied || snap.Enabled != status.Enabled || status.Generation != snap.Generation {
		r.scheduleReconcile(ext)
		return true
	}
	return false
}

func (r *Registry) acceptStatus(ext *externalModule, snap externalRecord, status contracts.ExternalStatus, now int64) bool {
	if status.RegistrationID != "" && status.RegistrationID != snap.RegistrationID {
		return false
	}
	if status.Generation != snap.Generation {
		return false
	}
	health := status.Health
	if health == "" {
		health = contracts.HealthReady
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ext.rec.Epoch != snap.Epoch || ext.rec.ConnectionRevision != snap.ConnectionRevision {
		return false
	}
	if ext.rec.Generation != snap.Generation {
		return false
	}
	if ext.rec.InstanceID != "" && status.InstanceID != "" && ext.rec.InstanceID != status.InstanceID && ext.rec.InstanceID != snap.InstanceID {
		return false
	}
	enabled := status.Enabled
	ext.rec.ObservedEnabled = &enabled
	ext.rec.ObservedGeneration = status.Generation
	if ext.rec.Incompatible {
		health = contracts.HealthIncompatible
	}
	ext.rec.Health = health
	ext.rec.InstanceID = status.InstanceID
	ext.rec.LastCheckedAt = &now
	if status.Error == "" {
		ext.rec.LastError = ""
		ext.rec.LastSuccessAt = &now
	} else {
		ext.rec.LastError = status.Error
	}
	go r.persistObservedAsync(ext.rec)
	if !ext.rec.businessAllowed() {
		go r.sockets.closeModule(ext.rec.ID)
	}
	return true
}

func (r *Registry) persistObserved(ext *externalModule, snap externalRecord, status contracts.ExternalStatus, cause error, now int64) {
	r.mu.Lock()
	if ext.rec.Epoch != snap.Epoch || ext.rec.ConnectionRevision != snap.ConnectionRevision || ext.rec.Generation != snap.Generation {
		r.mu.Unlock()
		return
	}
	if cause != nil {
		if !ext.rec.Incompatible && ext.rec.Health != contracts.HealthIncompatible {
			ext.rec.Health = contracts.HealthOffline
		}
		message := "服务不可达"
		if e, ok := cause.(*Error); ok && e.Message != "" {
			message = e.Message
		}
		ext.rec.LastError = message
		ext.rec.LastCheckedAt = &now
		copyRec := ext.rec
		r.mu.Unlock()
		_ = r.queries.UpdateExternalObserved(r.bgCtx, observedParams(copyRec, now))
		return
	}
	r.mu.Unlock()
	r.acceptStatus(ext, snap, status, now)
}

func (r *Registry) persistObservedAsync(rec externalRecord) {
	now := r.now().UnixMilli()
	_ = r.queries.UpdateExternalObserved(r.bgCtx, observedParams(rec, now))
}

func observedParams(rec externalRecord, now int64) dbsqlc.UpdateExternalObservedParams {
	return dbsqlc.UpdateExternalObservedParams{
		ObservedGeneration: nullInt(rec.ObservedGeneration),
		ObservedEnabled:    nullBool(rec.ObservedEnabled),
		Health:             rec.Health,
		LastError:          nullString(rec.LastError),
		LastCheckedAt:      nullIntPtr(rec.LastCheckedAt),
		LastSuccessAt:      nullIntPtr(rec.LastSuccessAt),
		InstanceID:         nullString(rec.InstanceID),
		UpdatedAt:          now,
		ModuleID:           string(rec.ID),
	}
}

func (r *Registry) scheduleReconcile(ext *externalModule) {
	if r.closed.Load() {
		return
	}
	epoch := ext.rec.Epoch
	generation := ext.rec.Generation
	revision := ext.rec.ConnectionRevision
	if !ext.retrying.CompareAndSwap(false, true) {
		ext.retryEpoch.Store(epoch)
		return
	}
	ext.retryEpoch.Store(epoch)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer ext.retrying.Store(false)
		backoff := 200 * time.Millisecond
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-r.bgCtx.Done():
				return
			case <-timer.C:
			}
			if r.closed.Load() {
				return
			}
			ext.ctrl.Lock()
			snap := ext.rec
			if snap.Epoch != epoch && snap.Epoch != ext.retryEpoch.Load() {
				ext.ctrl.Unlock()
				return
			}
			if snap.Epoch != epoch {
				epoch = snap.Epoch
				generation = snap.Generation
				revision = snap.ConnectionRevision
			}
			need := (!snap.Incompatible || !snap.Enabled) && (snap.pending() || snap.Health == contracts.HealthUnknown || snap.Health == contracts.HealthOffline)
			if !need || snap.ConnectionRevision != revision {
				ext.ctrl.Unlock()
				return
			}
			pending := r.reconcileLocked(ext, snap)
			ext.ctrl.Unlock()
			if !pending {
				return
			}
			retryMax := r.currentTimeouts().RetryMax
			if backoff > retryMax {
				backoff = retryMax
			}
			timer.Reset(backoff)
			if backoff < retryMax {
				backoff *= 2
				if backoff > retryMax {
					backoff = retryMax
				}
			}
			_ = generation
		}
	}()
}

func (r *Registry) reconcileLocked(ext *externalModule, snap externalRecord) bool {
	ctx, cancel := context.WithTimeout(r.bgCtx, r.currentTimeouts().Control)
	defer cancel()
	status, err := r.getStatus(ctx, snap)
	now := r.now().UnixMilli()
	// Incompatibility blocks business traffic and automatic enabling, but must
	// not prevent delivery of a saved disable intent after the service recovers.
	if snap.Incompatible && snap.Enabled {
		if err != nil {
			r.persistObserved(ext, snap, contracts.ExternalStatus{}, err, now)
		} else {
			r.mu.Lock()
			if ext.rec.Epoch == snap.Epoch && ext.rec.Incompatible {
				ext.rec.LastCheckedAt = &now
				ext.rec.Health = contracts.HealthIncompatible
			}
			r.mu.Unlock()
		}
		return ext.rec.pending()
	}
	if err != nil {
		r.persistObserved(ext, snap, contracts.ExternalStatus{}, err, now)
		return true
	}
	if status.InstanceID != "" && status.InstanceID != snap.InstanceID {
		return r.applyRemoteState(ctx, ext, snap)
	}
	if status.Generation != snap.Generation || status.Enabled != snap.Enabled {
		return r.applyRemoteState(ctx, ext, snap)
	}
	r.acceptStatus(ext, snap, status, now)
	return ext.rec.pending()
}

func (r *Registry) probeLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.currentTimeouts().ProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.bgCtx.Done():
			return
		case <-ticker.C:
			r.probeAll()
		}
	}
}

func (r *Registry) probeAll() {
	r.mu.RLock()
	ids := make([]contracts.ModuleID, 0, len(r.external))
	for id := range r.external {
		ids = append(ids, id)
	}
	r.mu.RUnlock()
	for _, id := range ids {
		if r.closed.Load() {
			return
		}
		select {
		case r.probeSem <- struct{}{}:
		case <-r.bgCtx.Done():
			return
		}
		r.wg.Add(1)
		go func(id contracts.ModuleID) {
			defer r.wg.Done()
			defer func() { <-r.probeSem }()
			r.Probe(r.bgCtx, id)
		}(id)
	}
}

func (r *Registry) Probe(ctx context.Context, id contracts.ModuleID) {
	r.mu.RLock()
	ext, ok := r.external[id]
	r.mu.RUnlock()
	if !ok {
		return
	}
	ext.ctrl.Lock()
	defer ext.ctrl.Unlock()
	r.reconcileLocked(ext, ext.rec)
}
