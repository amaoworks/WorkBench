package modules

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"workbench/internal/contracts"
	dbsqlc "workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/identity"
)

type ModuleKind string

const (
	KindBuiltin  ModuleKind = "builtin"
	KindExternal ModuleKind = "external"
)

type ListedPage struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Entry string `json:"entry"`
	Order int    `json:"order"`
}

type ListedModule struct {
	ID                 contracts.ModuleID         `json:"id"`
	Kind               ModuleKind                 `json:"kind"`
	Name               string                     `json:"name"`
	Version            string                     `json:"version"`
	ContractVersion    int                        `json:"contractVersion,omitempty"`
	ProtocolVersion    int                        `json:"protocolVersion,omitempty"`
	Icon               string                     `json:"icon"`
	Navigation         []contracts.NavigationItem `json:"navigation"`
	Pages              []ListedPage               `json:"pages,omitempty"`
	HasSettings        bool                       `json:"hasSettings"`
	SettingsEntry      string                     `json:"settingsEntry,omitempty"`
	Enabled            bool                       `json:"enabled"`
	ObservedEnabled    *bool                      `json:"observedEnabled"`
	Health             string                     `json:"health,omitempty"`
	Pending            bool                       `json:"pending,omitempty"`
	LastError          string                     `json:"lastError,omitempty"`
	LastCheckedAt      *int64                     `json:"lastCheckedAt,omitempty"`
	BaseURL            string                     `json:"baseUrl,omitempty"`
	AllowNonLocal      bool                       `json:"allowNonLocal,omitempty"`
	HasServiceToken    bool                       `json:"hasServiceToken,omitempty"`
	ConnectionNote     string                     `json:"connectionNote,omitempty"`
	Generation         int64                      `json:"generation,omitempty"`
	ObservedGeneration int64                      `json:"observedGeneration,omitempty"`
}

type ConnectionInput struct {
	BaseURL       string `json:"baseUrl"`
	ServiceToken  string `json:"serviceToken"`
	AllowNonLocal bool   `json:"allowNonLocal"`
}

type EnableResult struct {
	Module  ListedModule
	Pending bool
}

type externalRecord struct {
	ID                 contracts.ModuleID
	Name               string
	Version            string
	RegistrationID     string
	ConnectionRevision int64
	BaseURL            string
	AllowNonLocal      bool
	ServiceToken       string
	ProtocolVersion    int
	Manifest           contracts.ExternalManifest
	Enabled            bool
	Generation         int64
	ObservedGeneration int64
	ObservedEnabled    *bool
	Health             string
	LastError          string
	LastCheckedAt      *int64
	LastSuccessAt      *int64
	InstanceID         string
	ConnectionNote     string
	Epoch              uint64
	Incompatible       bool
}

type externalModule struct {
	ctrl       sync.Mutex
	rec        externalRecord
	retrying   atomic.Bool
	retryEpoch atomic.Uint64
}

func (e *externalModule) listed() ListedModule {
	rec := e.rec
	nav := navigationFromManifest(rec.Manifest)
	pages := listedPages(rec.Manifest)
	pending := rec.pending()
	item := ListedModule{
		ID:                 rec.ID,
		Kind:               KindExternal,
		Name:               rec.Name,
		Version:            rec.Version,
		ProtocolVersion:    rec.ProtocolVersion,
		Icon:               rec.Manifest.Icon,
		Navigation:         nav,
		Pages:              pages,
		HasSettings:        rec.Manifest.Settings != nil,
		Enabled:            rec.Enabled,
		ObservedEnabled:    copyBool(rec.ObservedEnabled),
		Health:             rec.Health,
		Pending:            pending,
		LastError:          rec.LastError,
		LastCheckedAt:      copyInt(rec.LastCheckedAt),
		BaseURL:            rec.BaseURL,
		AllowNonLocal:      rec.AllowNonLocal,
		HasServiceToken:    rec.ServiceToken != "",
		ConnectionNote:     rec.ConnectionNote,
		Generation:         rec.Generation,
		ObservedGeneration: rec.ObservedGeneration,
	}
	if rec.Manifest.Settings != nil {
		item.SettingsEntry = rec.Manifest.Settings.Entry
	}
	return item
}

func (rec externalRecord) pending() bool {
	if rec.ObservedEnabled == nil || rec.ObservedGeneration != rec.Generation {
		return true
	}
	return *rec.ObservedEnabled != rec.Enabled
}

func (rec externalRecord) businessAllowed() bool {
	if rec.Incompatible {
		return false
	}
	if !rec.Enabled || rec.ObservedEnabled == nil || !*rec.ObservedEnabled {
		return false
	}
	if rec.ObservedGeneration != rec.Generation {
		return false
	}
	switch rec.Health {
	case contracts.HealthReady, contracts.HealthDegraded:
		return true
	default:
		return false
	}
}

func copyBool(v *bool) *bool {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

func copyInt(v *int64) *int64 {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

func (r *Registry) snapshotExternal(id contracts.ModuleID) (externalRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ext, ok := r.external[id]
	if !ok {
		return externalRecord{}, false
	}
	return ext.rec, true
}

func (r *Registry) loadExternals(ctx context.Context) error {
	rows, err := r.queries.ListExternalModules(ctx)
	if err != nil {
		return err
	}
	loaded := make(map[contracts.ModuleID]*externalModule, len(rows))
	for _, row := range rows {
		ext, err := moduleFromListRow(row)
		if err != nil {
			return err
		}
		incompatible := ext.rec.Health == contracts.HealthIncompatible
		ext.rec.Incompatible = incompatible
		ext.rec.ObservedEnabled = nil
		ext.rec.ObservedGeneration = 0
		ext.rec.InstanceID = ""
		if incompatible {
			ext.rec.Health = contracts.HealthIncompatible
		} else {
			ext.rec.Health = contracts.HealthUnknown
		}
		loaded[ext.rec.ID] = ext
	}
	r.mu.Lock()
	r.external = loaded
	r.mu.Unlock()
	// Reconfirm in the background so a restarted host does not wait for the
	// periodic probe before applying the persisted intent.
	for _, ext := range loaded {
		r.scheduleReconcile(ext)
	}
	return nil
}

func moduleFromListRow(row dbsqlc.ListExternalModulesRow) (*externalModule, error) {
	return moduleFromFields(externalFields{
		ID: row.ID, Name: row.Name, Version: row.Version, Enabled: row.Enabled,
		RegistrationID: row.RegistrationID, ConnectionRevision: row.ConnectionRevision,
		BaseURL: row.BaseUrl, AllowNonLocal: row.AllowNonLocal, ServiceToken: row.ServiceToken,
		ProtocolVersion: row.ProtocolVersion, ManifestJSON: row.ManifestJson, Generation: row.Generation,
		ObservedGeneration: row.ObservedGeneration, ObservedEnabled: row.ObservedEnabled,
		Health: row.Health, LastError: row.LastError, LastCheckedAt: row.LastCheckedAt,
		LastSuccessAt: row.LastSuccessAt, InstanceID: row.InstanceID, ConnectionNote: row.ConnectionNote,
	})
}

func moduleFromGetRow(row dbsqlc.GetExternalModuleRow) (*externalModule, error) {
	return moduleFromFields(externalFields{
		ID: row.ID, Name: row.Name, Version: row.Version, Enabled: row.Enabled,
		RegistrationID: row.RegistrationID, ConnectionRevision: row.ConnectionRevision,
		BaseURL: row.BaseUrl, AllowNonLocal: row.AllowNonLocal, ServiceToken: row.ServiceToken,
		ProtocolVersion: row.ProtocolVersion, ManifestJSON: row.ManifestJson, Generation: row.Generation,
		ObservedGeneration: row.ObservedGeneration, ObservedEnabled: row.ObservedEnabled,
		Health: row.Health, LastError: row.LastError, LastCheckedAt: row.LastCheckedAt,
		LastSuccessAt: row.LastSuccessAt, InstanceID: row.InstanceID, ConnectionNote: row.ConnectionNote,
	})
}

type externalFields struct {
	ID, Name, Version, RegistrationID, BaseURL, ServiceToken, ManifestJSON, Health string
	Enabled, ConnectionRevision, ProtocolVersion, Generation                       int64
	AllowNonLocal                                                                  int64
	ObservedGeneration, ObservedEnabled, LastCheckedAt, LastSuccessAt              sql.NullInt64
	LastError, InstanceID, ConnectionNote                                          sql.NullString
}

func moduleFromFields(row externalFields) (*externalModule, error) {
	manifest, err := ValidateExternalManifest([]byte(row.ManifestJSON), contracts.ModuleID(row.ID))
	if err != nil {
		var raw contracts.ExternalManifest
		_ = json.Unmarshal([]byte(row.ManifestJSON), &raw)
		raw.ID = contracts.ModuleID(row.ID)
		manifest = raw
	}
	rec := externalRecord{
		ID:                 contracts.ModuleID(row.ID),
		Name:               row.Name,
		Version:            row.Version,
		RegistrationID:     row.RegistrationID,
		ConnectionRevision: row.ConnectionRevision,
		BaseURL:            row.BaseURL,
		AllowNonLocal:      row.AllowNonLocal != 0,
		ServiceToken:       row.ServiceToken,
		ProtocolVersion:    int(row.ProtocolVersion),
		Manifest:           manifest,
		Enabled:            row.Enabled != 0,
		Generation:         row.Generation,
		Health:             row.Health,
		LastError:          row.LastError.String,
		InstanceID:         row.InstanceID.String,
		ConnectionNote:     row.ConnectionNote.String,
		Epoch:              1,
	}
	if row.ObservedGeneration.Valid {
		rec.ObservedGeneration = row.ObservedGeneration.Int64
	}
	if row.ObservedEnabled.Valid {
		v := row.ObservedEnabled.Int64 != 0
		rec.ObservedEnabled = &v
	}
	if row.LastCheckedAt.Valid {
		v := row.LastCheckedAt.Int64
		rec.LastCheckedAt = &v
	}
	if row.LastSuccessAt.Valid {
		v := row.LastSuccessAt.Int64
		rec.LastSuccessAt = &v
	}
	return &externalModule{rec: rec}, nil
}

func (r *Registry) Attach(ctx context.Context, input ConnectionInput) (ListedModule, error) {
	origin, token, err := r.validateConnection(input, true)
	if err != nil {
		return ListedModule{}, err
	}
	raw, err := r.fetchManifest(ctx, origin, token, input.AllowNonLocal)
	if err != nil {
		return ListedModule{}, err
	}
	manifest, err := ValidateExternalManifest(raw, "")
	if err != nil {
		return ListedModule{}, err
	}
	if manifest.ID == reservedModuleID {
		return ListedModule{}, apiError(409, "module_id_conflict", "不能覆盖核心模块")
	}
	r.mu.RLock()
	_, builtin := r.enabled[manifest.ID]
	_, exists := r.external[manifest.ID]
	r.mu.RUnlock()
	if builtin || exists {
		return ListedModule{}, apiError(409, "module_id_conflict", "模块 ID 已存在")
	}
	if existing, getErr := r.queries.GetModule(ctx, string(manifest.ID)); getErr == nil {
		if existing.Kind == "builtin" || existing.ID == reservedModuleID {
			return ListedModule{}, apiError(409, "module_id_conflict", "模块 ID 已存在")
		}
		return ListedModule{}, apiError(409, "module_exists", "模块已接入")
	} else if !errors.Is(getErr, sql.ErrNoRows) {
		return ListedModule{}, apiError(500, "persist_failed", "无法保存模块注册")
	}

	registrationID, err := identity.New()
	if err != nil {
		return ListedModule{}, apiError(500, "persist_failed", "无法保存模块注册")
	}
	now := r.now().UnixMilli()
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return ListedModule{}, apiError(500, "persist_failed", "无法保存模块注册")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ListedModule{}, apiError(500, "persist_failed", "无法保存模块注册")
	}
	defer tx.Rollback()
	q := dbsqlc.New(tx)
	if err := q.InsertModule(ctx, dbsqlc.InsertModuleParams{
		ID: string(manifest.ID), Name: manifest.Name, Version: manifest.Version,
		ContractVersion: 0, Enabled: 0, InstalledAt: now, UpdatedAt: now, Kind: "external",
	}); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ListedModule{}, apiError(409, "module_exists", "模块已接入")
		}
		return ListedModule{}, apiError(500, "persist_failed", "无法保存模块注册")
	}
	if err := q.InsertExternalModule(ctx, dbsqlc.InsertExternalModuleParams{
		ModuleID: string(manifest.ID), RegistrationID: registrationID, ConnectionRevision: 1,
		BaseUrl: origin, AllowNonLocal: boolToInt(input.AllowNonLocal), ServiceToken: token,
		ProtocolVersion: int64(manifest.ProtocolVersion), ManifestJson: string(manifestJSON),
		Generation: 0, Health: contracts.HealthUnknown, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return ListedModule{}, apiError(500, "persist_failed", "无法保存模块注册")
	}
	if err := tx.Commit(); err != nil {
		return ListedModule{}, apiError(500, "persist_failed", "无法保存模块注册")
	}

	ext := &externalModule{rec: externalRecord{
		ID: manifest.ID, Name: manifest.Name, Version: manifest.Version,
		RegistrationID: registrationID, ConnectionRevision: 1, BaseURL: origin,
		AllowNonLocal: input.AllowNonLocal, ServiceToken: token,
		ProtocolVersion: manifest.ProtocolVersion, Manifest: manifest,
		Health: contracts.HealthUnknown, Epoch: 1,
	}}
	r.mu.Lock()
	if r.closed.Load() {
		r.mu.Unlock()
		return ListedModule{}, apiError(503, "shutting_down", "工作台正在关闭")
	}
	if _, exists := r.external[manifest.ID]; exists {
		r.mu.Unlock()
		return ListedModule{}, apiError(409, "module_exists", "模块已接入")
	}
	if _, builtin := r.enabled[manifest.ID]; builtin {
		r.mu.Unlock()
		return ListedModule{}, apiError(409, "module_id_conflict", "模块 ID 已存在")
	}
	r.external[manifest.ID] = ext
	listed := ext.listed()
	r.mu.Unlock()
	r.logger.Info("external module attached", "component", "modules", "module", manifest.ID, "baseUrl", origin)
	r.scheduleReconcile(ext)
	return listed, nil
}

func (r *Registry) validateConnection(input ConnectionInput, tokenRequired bool) (string, string, error) {
	origin, err := ParseBaseURL(input.BaseURL)
	if err != nil {
		return "", "", err
	}
	token := strings.TrimSpace(input.ServiceToken)
	if tokenRequired && token == "" {
		return "", "", apiError(400, "invalid_request", "请填写服务凭据")
	}
	if len(token) > 8192 {
		return "", "", apiError(400, "invalid_request", "服务凭据过长")
	}
	if !input.AllowNonLocal {
		if err := assertLoopbackHost(context.Background(), hostnameOfOrigin(origin)); err != nil {
			return "", "", err
		}
	}
	return origin, token, nil
}

func (r *Registry) UpdateConnection(ctx context.Context, id contracts.ModuleID, input ConnectionInput) (ListedModule, error) {
	ext, err := r.externalLocked(id)
	if err != nil {
		return ListedModule{}, err
	}
	ext.ctrl.Lock()
	defer ext.ctrl.Unlock()
	if ext.rec.Enabled {
		return ListedModule{}, apiError(409, "module_enabled", "更新连接前请先停用业务")
	}
	origin, token, err := r.validateConnection(input, false)
	if err != nil {
		return ListedModule{}, err
	}
	originChanged := origin != ext.rec.BaseURL
	if originChanged && token == "" {
		return ListedModule{}, apiError(400, "origin_changed_token_required", "更换服务地址时请重新填写服务凭据")
	}
	if token == "" {
		token = ext.rec.ServiceToken
	}
	raw, err := r.fetchManifest(ctx, origin, token, input.AllowNonLocal)
	if err != nil {
		return ListedModule{}, err
	}
	manifest, err := ValidateExternalManifest(raw, id)
	if err != nil {
		return ListedModule{}, err
	}
	note := ""
	if ext.rec.pending() || (ext.rec.ObservedEnabled != nil && *ext.rec.ObservedEnabled) {
		note = "旧端点未确认停用"
	}
	now := r.now().UnixMilli()
	manifestJSON, _ := json.Marshal(manifest)
	revision := ext.rec.ConnectionRevision + 1
	if err := r.queries.UpdateExternalConnection(ctx, dbsqlc.UpdateExternalConnectionParams{
		ConnectionRevision: revision, BaseUrl: origin, AllowNonLocal: boolToInt(input.AllowNonLocal),
		ServiceToken: token, ProtocolVersion: int64(manifest.ProtocolVersion), ManifestJson: string(manifestJSON),
		ConnectionNote: nullString(note), UpdatedAt: now, ModuleID: string(id),
	}); err != nil {
		return ListedModule{}, apiError(500, "persist_failed", "无法保存连接")
	}
	if err := r.queries.UpdateModuleIdentity(ctx, dbsqlc.UpdateModuleIdentityParams{
		Name: manifest.Name, Version: manifest.Version, UpdatedAt: now, ID: string(id), Kind: "external",
	}); err != nil {
		return ListedModule{}, apiError(500, "persist_failed", "无法保存连接")
	}
	r.sockets.closeModule(id)
	r.transports.drop(ext.rec.BaseURL)
	r.mu.Lock()
	ext.rec.ConnectionRevision = revision
	ext.rec.BaseURL = origin
	ext.rec.AllowNonLocal = input.AllowNonLocal
	ext.rec.ServiceToken = token
	ext.rec.ProtocolVersion = manifest.ProtocolVersion
	ext.rec.Manifest = manifest
	ext.rec.Name = manifest.Name
	ext.rec.Version = manifest.Version
	ext.rec.ObservedEnabled = nil
	ext.rec.ObservedGeneration = 0
	ext.rec.Health = contracts.HealthUnknown
	ext.rec.Incompatible = false
	ext.rec.LastError = ""
	ext.rec.InstanceID = ""
	ext.rec.ConnectionNote = note
	ext.rec.Epoch++
	listed := ext.listed()
	r.mu.Unlock()
	r.logger.Info("external module connection updated", "component", "modules", "module", id, "baseUrl", origin)
	r.scheduleReconcile(ext)
	return listed, nil
}

func (r *Registry) Refresh(ctx context.Context, id contracts.ModuleID) (ListedModule, error) {
	ext, err := r.externalLocked(id)
	if err != nil {
		return ListedModule{}, err
	}
	ext.ctrl.Lock()
	defer ext.ctrl.Unlock()
	snap := ext.rec
	raw, err := r.fetchManifest(ctx, snap.BaseURL, snap.ServiceToken, snap.AllowNonLocal)
	now := r.now().UnixMilli()
	if err != nil {
		r.markProbeFailure(ext, snap, err, now)
		return r.listedLocked(ext), err
	}
	manifest, err := ValidateExternalManifest(raw, id)
	if err != nil {
		r.markIncompatible(ctx, ext, snap, err, now)
		return r.listedLocked(ext), nil
	}
	manifestJSON, _ := json.Marshal(manifest)
	health := ext.rec.Health
	if ext.rec.Incompatible || health == contracts.HealthIncompatible {
		health = contracts.HealthUnknown
	}
	if dbErr := r.queries.UpdateExternalManifest(ctx, dbsqlc.UpdateExternalManifestParams{
		ProtocolVersion: int64(manifest.ProtocolVersion), ManifestJson: string(manifestJSON),
		Health: health, LastError: nullString(""), LastCheckedAt: nullInt(now),
		LastSuccessAt: nullInt(now), UpdatedAt: now, ModuleID: string(id),
	}); dbErr != nil {
		return ListedModule{}, apiError(500, "persist_failed", "无法保存模块描述")
	}
	_ = r.queries.UpdateModuleIdentity(ctx, dbsqlc.UpdateModuleIdentityParams{
		Name: manifest.Name, Version: manifest.Version, UpdatedAt: now, ID: string(id), Kind: "external",
	})
	r.mu.Lock()
	if ext.rec.ConnectionRevision != snap.ConnectionRevision || ext.rec.Epoch != snap.Epoch {
		listed := ext.listed()
		r.mu.Unlock()
		return listed, nil
	}
	ext.rec.Manifest = manifest
	ext.rec.Name = manifest.Name
	ext.rec.Version = manifest.Version
	ext.rec.ProtocolVersion = manifest.ProtocolVersion
	ext.rec.Incompatible = false
	ext.rec.Health = health
	ext.rec.LastError = ""
	ext.rec.LastCheckedAt = &now
	ext.rec.LastSuccessAt = &now
	listed := ext.listed()
	r.mu.Unlock()
	r.scheduleReconcile(ext)
	return listed, nil
}

func (r *Registry) Unregister(ctx context.Context, id contracts.ModuleID) error {
	ext, err := r.externalLocked(id)
	if err != nil {
		return err
	}
	ext.ctrl.Lock()
	defer ext.ctrl.Unlock()
	if ext.rec.Enabled {
		return apiError(409, "module_enabled", "解除接入前请先停用业务")
	}
	confirmedDisabled := ext.rec.ObservedEnabled != nil && !*ext.rec.ObservedEnabled && ext.rec.ObservedGeneration == ext.rec.Generation
	neverEnabled := ext.rec.Generation == 0 && (ext.rec.ObservedEnabled == nil || !*ext.rec.ObservedEnabled)
	if !confirmedDisabled && !neverEnabled {
		return apiError(409, "unregister_not_allowed", "请等待远端确认停用后再解除接入")
	}
	rows, err := r.queries.DeleteExternalModuleRow(ctx, string(id))
	if err != nil {
		return apiError(500, "persist_failed", "无法删除模块注册")
	}
	if rows != 1 {
		return apiError(404, "module_not_found", "模块不存在")
	}
	r.sockets.closeModule(id)
	r.transports.drop(ext.rec.BaseURL)
	r.mu.Lock()
	ext.rec.Epoch++
	delete(r.external, id)
	r.mu.Unlock()
	r.logger.Info("external module unregistered", "component", "modules", "module", id)
	return nil
}

func (r *Registry) GetConfig(ctx context.Context, id contracts.ModuleID) (json.RawMessage, error) {
	ext, err := r.externalLocked(id)
	if err != nil {
		return nil, err
	}
	ext.ctrl.Lock()
	defer ext.ctrl.Unlock()
	return r.controlJSON(ctx, ext.rec, "GET", "/_workbench/config", nil)
}

func (r *Registry) PutConfig(ctx context.Context, id contracts.ModuleID, body json.RawMessage) (json.RawMessage, error) {
	ext, err := r.externalLocked(id)
	if err != nil {
		return nil, err
	}
	ext.ctrl.Lock()
	defer ext.ctrl.Unlock()
	if len(body) == 0 {
		body = json.RawMessage(`{}`)
	}
	return r.controlJSON(ctx, ext.rec, "PUT", "/_workbench/config", body)
}

func (r *Registry) externalLocked(id contracts.ModuleID) (*externalModule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed.Load() {
		return nil, apiError(503, "shutting_down", "工作台正在关闭")
	}
	if _, builtin := r.enabled[id]; builtin {
		return nil, apiError(400, "not_external", "该操作仅适用于外部模块")
	}
	ext, ok := r.external[id]
	if !ok {
		return nil, apiError(404, "module_not_found", "模块不存在")
	}
	return ext, nil
}

func (r *Registry) listedLocked(ext *externalModule) ListedModule {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return ext.listed()
}

func (r *Registry) markIncompatible(ctx context.Context, ext *externalModule, snap externalRecord, cause error, now int64) {
	message := cause.Error()
	_ = r.queries.UpdateExternalObserved(ctx, dbsqlc.UpdateExternalObservedParams{
		ObservedGeneration: nullInt(ext.rec.ObservedGeneration),
		ObservedEnabled:    nullBool(ext.rec.ObservedEnabled),
		Health:             contracts.HealthIncompatible,
		LastError:          nullString(message),
		LastCheckedAt:      nullInt(now),
		LastSuccessAt:      nullIntPtr(ext.rec.LastSuccessAt),
		InstanceID:         nullString(ext.rec.InstanceID),
		UpdatedAt:          now,
		ModuleID:           string(ext.rec.ID),
	})
	r.sockets.closeModule(ext.rec.ID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if ext.rec.ConnectionRevision != snap.ConnectionRevision || ext.rec.Epoch != snap.Epoch {
		return
	}
	ext.rec.Health = contracts.HealthIncompatible
	ext.rec.Incompatible = true
	ext.rec.LastError = message
	ext.rec.LastCheckedAt = &now
}

func (r *Registry) markProbeFailure(ext *externalModule, snap externalRecord, cause error, now int64) {
	message := "服务不可达"
	if e, ok := cause.(*Error); ok && e.Message != "" {
		message = e.Message
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ext.rec.ConnectionRevision != snap.ConnectionRevision || ext.rec.Epoch != snap.Epoch {
		return
	}
	if !ext.rec.Incompatible && ext.rec.Health != contracts.HealthIncompatible {
		ext.rec.Health = contracts.HealthOffline
	}
	ext.rec.LastError = message
	ext.rec.LastCheckedAt = &now
}

func boolToInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func nullString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

func nullInt(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: true}
}

func nullIntPtr(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func nullBool(v *bool) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: boolToInt(*v), Valid: true}
}
