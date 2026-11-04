package mobilepush

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"workbench/internal/contracts"
	dbsqlc "workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/httpapi"
	"workbench/internal/foundation/identity"
)

const consumerID = "core.notification.mobile_push"

type DeviceSessionIDFunc func(*http.Request) string

type Config struct {
	Enabled bool
	APNs    *APNsConfig
	FCM     *FCMConfig
	Allowed func(contracts.ModuleID) bool
}

type Service struct {
	db              *sql.DB
	enabled         bool
	activatedAt     int64
	deviceSessionID DeviceSessionIDFunc
	now             func() time.Time
	providers       map[string]provider
	allowed         func(contracts.ModuleID) bool
	users           contracts.UserDirectory
}

func (s *Service) SetUserDirectory(users contracts.UserDirectory) { s.users = users }

type deploymentState struct {
	Enabled     bool  `json:"enabled"`
	ActivatedAt int64 `json:"activatedAt"`
}

type provider interface {
	Send(context.Context, contracts.Notification, string) (invalidToken bool, err error)
}

type deliveryError struct {
	message    string
	retryAfter time.Duration
	permanent  bool
}

func (e *deliveryError) Error() string             { return e.message }
func (e *deliveryError) RetryAfter() time.Duration { return e.retryAfter }
func (e *deliveryError) Permanent() bool           { return e.permanent }

func New(db *sql.DB, cfg Config, deviceSessionID DeviceSessionIDFunc) (*Service, error) {
	if db == nil || deviceSessionID == nil {
		return nil, errors.New("database and device session resolver are required")
	}
	s := &Service{db: db, enabled: cfg.Enabled, deviceSessionID: deviceSessionID, now: time.Now, providers: make(map[string]provider), allowed: cfg.Allowed}
	if !cfg.Enabled {
		if err := s.syncActivation(context.Background()); err != nil {
			return nil, err
		}
		return s, nil
	}
	if cfg.APNs != nil {
		p, err := newAPNs(*cfg.APNs)
		if err != nil {
			return nil, fmt.Errorf("configure APNs: %w", err)
		}
		s.providers["ios"] = p
	}
	if cfg.FCM != nil {
		p, err := newFCM(*cfg.FCM)
		if err != nil {
			return nil, fmt.Errorf("configure FCM: %w", err)
		}
		s.providers["android"] = p
	}
	if len(s.providers) == 0 {
		return nil, errors.New("at least one mobile push provider is required when enabled")
	}
	if err := s.syncActivation(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) syncActivation(ctx context.Context) error {
	queries := dbsqlc.New(s.db)
	raw, err := queries.GetMobilePushState(ctx)
	missing := errors.Is(err, sql.ErrNoRows)
	if err != nil && !missing {
		return fmt.Errorf("read mobile push activation: %w", err)
	}
	var state deploymentState
	if !missing {
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return fmt.Errorf("decode mobile push activation: %w", err)
		}
	}
	if missing || state.Enabled != s.enabled {
		state.Enabled = s.enabled
		if s.enabled {
			state.ActivatedAt = s.now().UTC().UnixMilli()
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if err := queries.SaveMobilePushState(ctx, string(raw)); err != nil {
			return fmt.Errorf("save mobile push activation: %w", err)
		}
	}
	s.activatedAt = state.ActivatedAt
	return nil
}

func (s *Service) Consumer() contracts.EventConsumer {
	return contracts.EventConsumer{ID: consumerID, Module: "core", Topics: []string{"core.notification.created"}, Timeout: 20 * time.Second, MaxAttempts: 12, Handler: s.consume}
}

func (s *Service) consume(ctx context.Context, event contracts.Event) error {
	if !s.enabled {
		return nil
	}
	var payload struct {
		NotificationID string `json:"notificationId"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil || payload.NotificationID == "" {
		return &deliveryError{message: "invalid notification event", permanent: true}
	}
	if event.UserID == "" || contracts.UserID(ctx) != event.UserID {
		return &deliveryError{message: "notification event owner is required", permanent: true}
	}
	row, err := dbsqlc.New(s.db).GetNotificationByID(ctx, dbsqlc.GetNotificationByIDParams{ID: payload.NotificationID, RecipientID: event.UserID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.New("cannot read notification for mobile push")
	}
	note := contracts.Notification{
		ID: row.ID, RecipientID: row.RecipientID, SourceModule: contracts.ModuleID(row.SourceModule), External: row.External != 0,
		Severity: contracts.NotificationSeverity(row.Severity), Title: row.Title, Content: row.Content,
		ActionLabel: row.ActionLabel, ActionRoute: row.ActionRoute, CreatedAt: time.UnixMilli(row.CreatedAt).UTC(),
		ExpiresAt: nullableTime(row.ExpiresAt),
	}
	return s.Deliver(ctx, note)
}

func (s *Service) Deliver(ctx context.Context, note contracts.Notification) error {
	if !s.enabled || !note.External || note.CreatedAt.UnixMilli() < s.activatedAt || (s.allowed != nil && !s.allowed(note.SourceModule)) {
		return nil
	}
	if note.RecipientID == "" || (contracts.UserID(ctx) != "" && contracts.UserID(ctx) != note.RecipientID) {
		return &deliveryError{message: "notification recipient is required", permanent: true}
	}
	if s.users != nil {
		active, err := s.users.IsUserActive(ctx, note.RecipientID)
		if err != nil {
			return errors.New("cannot check notification recipient")
		}
		if !active {
			return nil
		}
	}
	now := s.now().UTC()
	if (note.ExpiresAt == nil && now.Sub(note.CreatedAt) > 15*time.Minute) || (note.ExpiresAt != nil && !now.Before(*note.ExpiresAt)) {
		return &deliveryError{message: "mobile notification delivery window expired", permanent: true}
	}
	targets, err := dbsqlc.New(s.db).ListActiveMobilePushTokens(ctx, dbsqlc.ListActiveMobilePushTokensParams{
		RecipientID: note.RecipientID, Now: now.UnixMilli(), NotificationCreatedAt: note.CreatedAt.UnixMilli(),
	})
	if err != nil {
		return errors.New("cannot list mobile push tokens")
	}

	var transient, permanent []error
	for _, target := range targets {
		p := s.providers[target.Platform]
		if p == nil {
			continue
		}
		invalid, sendErr := p.Send(ctx, note, target.Token)
		if invalid {
			if err := dbsqlc.New(s.db).DeleteInvalidMobilePushToken(ctx, dbsqlc.DeleteInvalidMobilePushTokenParams{ID: target.ID, Token: target.Token}); err != nil {
				transient = append(transient, errors.New("cannot remove invalid mobile push token"))
			}
			continue
		}
		if sendErr == nil {
			continue
		}
		var classified contracts.PermanentDeliveryError
		if errors.As(sendErr, &classified) && classified.Permanent() {
			permanent = append(permanent, sendErr)
		} else {
			transient = append(transient, sendErr)
		}
	}
	if len(transient) > 0 {
		return errors.Join(transient...)
	}
	if len(permanent) > 0 {
		return errors.Join(permanent...)
	}
	return nil
}

type tokenRequest struct {
	Platform  string `json:"platform"`
	PushToken string `json:"pushToken"`
}

func (s *Service) RegisterToken(w http.ResponseWriter, r *http.Request) {
	userID, err := contracts.RequireUserID(r.Context())
	if err != nil {
		httpapi.Error(w, 401, "authentication_required", "authentication required")
		return
	}
	if !s.enabled {
		httpapi.Error(w, http.StatusNotFound, "mobile_push_disabled", "mobile push is disabled")
		return
	}
	request, ok := decodeTokenRequest(w, r)
	if !ok {
		return
	}
	if s.providers[request.Platform] == nil {
		httpapi.Error(w, http.StatusBadRequest, "push_platform_unavailable", "push platform is not configured")
		return
	}
	sessionID := s.deviceSessionID(r)
	if sessionID == "" {
		httpapi.Error(w, http.StatusUnauthorized, "device_authentication_required", "device authentication required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "push_token_registration_failed", "could not register push token")
		return
	}
	defer tx.Rollback()
	queries := dbsqlc.New(tx)
	if err = queries.DeleteMobilePushTokenOwner(r.Context(), dbsqlc.DeleteMobilePushTokenOwnerParams{Platform: request.Platform, Token: request.PushToken, DeviceSessionID: sessionID}); err == nil {
		var id string
		id, err = identity.New()
		if err == nil {
			now := s.now().UTC().UnixMilli()
			var affected int64
			affected, err = queries.UpsertMobilePushToken(r.Context(), dbsqlc.UpsertMobilePushTokenParams{
				ID: id, UserID: userID, DeviceSessionID: sessionID, Platform: request.Platform, Token: request.PushToken, CreatedAt: now, UpdatedAt: now,
			})
			if err == nil {
				if affected == 0 {
					_ = tx.Rollback()
					httpapi.Error(w, http.StatusUnauthorized, "device_session_inactive", "device session is inactive")
					return
				}
			}
		}
	}
	if err != nil {
		_ = tx.Rollback()
		httpapi.Error(w, http.StatusInternalServerError, "push_token_registration_failed", "could not register push token")
		return
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		httpapi.Error(w, http.StatusInternalServerError, "push_token_registration_failed", "could not register push token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) DeleteToken(w http.ResponseWriter, r *http.Request) {
	userID, err := contracts.RequireUserID(r.Context())
	if err != nil {
		httpapi.Error(w, 401, "authentication_required", "authentication required")
		return
	}
	request, ok := decodeTokenRequest(w, r)
	if !ok {
		return
	}
	sessionID := s.deviceSessionID(r)
	if sessionID == "" {
		httpapi.Error(w, http.StatusUnauthorized, "device_authentication_required", "device authentication required")
		return
	}
	if err := dbsqlc.New(s.db).DeleteMobilePushToken(r.Context(), dbsqlc.DeleteMobilePushTokenParams{UserID: userID, DeviceSessionID: sessionID, Platform: request.Platform, Token: request.PushToken}); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "push_token_deletion_failed", "could not delete push token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.UnixMilli(value.Int64).UTC()
	return &result
}

func decodeTokenRequest(w http.ResponseWriter, r *http.Request) (tokenRequest, bool) {
	var request tokenRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid push token request")
		return tokenRequest{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid push token request")
		return tokenRequest{}, false
	}
	request.Platform = strings.TrimSpace(request.Platform)
	request.PushToken = strings.TrimSpace(request.PushToken)
	if (request.Platform != "ios" && request.Platform != "android") || request.PushToken == "" || len(request.PushToken) > 4096 {
		httpapi.Error(w, http.StatusBadRequest, "invalid_push_token", "invalid push platform or token")
		return tokenRequest{}, false
	}
	return request, true
}
