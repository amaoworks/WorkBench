package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/database/sqlc"
)

type EnabledFunc func(contracts.ModuleID) bool

type Dispatcher struct {
	db        *sql.DB
	store     *Store
	consumers []contracts.EventConsumer
	enabled   EnabledFunc
	logger    *slog.Logger
	pollEvery time.Duration
	now       func() time.Time
}

func NewDispatcher(
	db *sql.DB,
	store *Store,
	consumers []contracts.EventConsumer,
	enabled EnabledFunc,
	logger *slog.Logger,
) (*Dispatcher, error) {
	if db == nil || store == nil || enabled == nil {
		return nil, errors.New("database, store, and enabled function are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	seen := make(map[contracts.ConsumerID]struct{}, len(consumers))
	for index := range consumers {
		consumer := &consumers[index]
		if consumer.ID == "" || consumer.Module == "" || len(consumer.Topics) == 0 || consumer.Handler == nil {
			return nil, fmt.Errorf("consumer at index %d is invalid", index)
		}
		if _, exists := seen[consumer.ID]; exists {
			return nil, fmt.Errorf("duplicate consumer id %q", consumer.ID)
		}
		seen[consumer.ID] = struct{}{}
		if consumer.Timeout <= 0 {
			consumer.Timeout = 30 * time.Second
		}
		if consumer.MaxAttempts <= 0 {
			consumer.MaxAttempts = 5
		}
	}
	return &Dispatcher{
		db:        db,
		store:     store,
		consumers: consumers,
		enabled:   enabled,
		logger:    logger,
		pollEvery: time.Second,
		now:       time.Now,
	}, nil
}

func (d *Dispatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.pollEvery)
	defer ticker.Stop()
	for {
		if err := d.DispatchOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			d.logger.Error("event dispatch cycle failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-d.store.Wake():
		}
	}
}

func (d *Dispatcher) DispatchOnce(ctx context.Context) error {
	if err := d.materializeDeliveries(ctx); err != nil {
		return err
	}
	var errs []error
	for _, consumer := range d.consumers {
		if !d.enabled(consumer.Module) {
			continue
		}
		for processed := 0; processed < 100; processed++ {
			delivery, found, err := d.claim(ctx, consumer)
			if err != nil {
				errs = append(errs, fmt.Errorf("claim consumer %q: %w", consumer.ID, err))
				break
			}
			if !found {
				break
			}
			d.handle(ctx, consumer, delivery)
		}
	}
	return errors.Join(errs...)
}

func (d *Dispatcher) materializeDeliveries(ctx context.Context) error {
	now := d.now().UTC().UnixMilli()
	for _, consumer := range d.consumers {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(consumer.Topics)), ",")
		query := `
			INSERT OR IGNORE INTO event_deliveries(
				event_id, consumer_id, status, attempts, next_attempt_at, updated_at
			)
			SELECT id, ?, 'pending', 0, available_at, ?
			FROM events_log
			WHERE topic IN (` + placeholders + `)`
		args := make([]any, 0, 2+len(consumer.Topics))
		args = append(args, consumer.ID, now)
		for _, topic := range consumer.Topics {
			args = append(args, topic)
		}
		if _, err := d.db.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("materialize consumer %q deliveries: %w", consumer.ID, err)
		}
	}
	return nil
}

type claimedDelivery struct {
	event    contracts.Event
	attempts int
}

func (d *Dispatcher) claim(ctx context.Context, consumer contracts.EventConsumer) (claimedDelivery, bool, error) {
	now := d.now().UTC()
	leaseUntil := now.Add(consumer.Timeout + 5*time.Second).UnixMilli()
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return claimedDelivery{}, false, err
	}
	defer tx.Rollback()

	queries := dbsqlc.New(tx)
	row, err := queries.GetClaimableDelivery(ctx, dbsqlc.GetClaimableDeliveryParams{
		ConsumerID:    string(consumer.ID),
		NextAttemptAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true},
		LockedUntil:   sql.NullInt64{Int64: now.UnixMilli(), Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return claimedDelivery{}, false, nil
	}
	if err != nil {
		return claimedDelivery{}, false, err
	}
	delivery := claimedDelivery{
		event: contracts.Event{
			ID: contracts.EventID(row.EventID), Topic: row.Topic, SchemaVersion: int(row.SchemaVersion),
			SourceModule: contracts.ModuleID(row.SourceModule), AggregateID: row.AggregateID,
			OccurredAt: time.UnixMilli(row.OccurredAt).UTC(), Payload: json.RawMessage(row.PayloadJson),
		},
		attempts: int(row.Attempts) + 1,
	}
	rows, err := queries.MarkDeliveryRunning(ctx, dbsqlc.MarkDeliveryRunningParams{
		Attempts: int64(delivery.attempts), LockedUntil: sql.NullInt64{Int64: leaseUntil, Valid: true},
		StartedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, UpdatedAt: now.UnixMilli(),
		EventID: row.EventID, ConsumerID: string(consumer.ID),
	})
	if err != nil || rows != 1 {
		return claimedDelivery{}, false, fmt.Errorf("claim update affected %d rows: %w", rows, err)
	}
	if err := tx.Commit(); err != nil {
		return claimedDelivery{}, false, err
	}
	return delivery, true, nil
}

func (d *Dispatcher) handle(parent context.Context, consumer contracts.EventConsumer, delivery claimedDelivery) {
	d.logger.Debug("event delivery started", "eventId", delivery.event.ID, "consumerId", consumer.ID, "attempt", delivery.attempts)
	ctx, cancel := context.WithTimeout(parent, consumer.Timeout)
	err := consumer.Handler(ctx, delivery.event)
	cancel()

	updateCtx, updateCancel := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
	defer updateCancel()
	now := d.now().UTC()
	queries := dbsqlc.New(d.db)
	if err == nil {
		updateErr := queries.MarkDeliverySucceeded(updateCtx, dbsqlc.MarkDeliverySucceededParams{
			CompletedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, UpdatedAt: now.UnixMilli(),
			EventID: string(delivery.event.ID), ConsumerID: string(consumer.ID),
		})
		if updateErr != nil {
			d.logger.Error("mark event delivery succeeded", "eventId", delivery.event.ID, "consumerId", consumer.ID, "error", updateErr)
		} else {
			d.logger.Debug("event delivered", "eventId", delivery.event.ID, "consumerId", consumer.ID)
		}
		return
	}

	status := "retry"
	delay := backoff(delivery.attempts)
	var retryAfter contracts.RetryAfterError
	if errors.As(err, &retryAfter) {
		delay = max(delay, retryAfter.RetryAfter())
	}
	nextAttempt := now.Add(delay).UnixMilli()
	var permanent contracts.PermanentDeliveryError
	if delivery.attempts >= consumer.MaxAttempts || (errors.As(err, &permanent) && permanent.Permanent()) {
		status = "dead"
		nextAttempt = 0
	}
	level := slog.LevelWarn
	if status == "dead" {
		level = slog.LevelError
	}
	d.logger.Log(parent, level, "event delivery failed", "eventId", delivery.event.ID, "consumerId", consumer.ID,
		"attempt", delivery.attempts, "status", status, "errorType", fmt.Sprintf("%T", err))
	updateErr := queries.MarkDeliveryFailed(updateCtx, dbsqlc.MarkDeliveryFailedParams{
		Status: status, NextAttemptAt: nextAttempt,
		LastError: sql.NullString{String: truncateError(err), Valid: true}, UpdatedAt: now.UnixMilli(),
		EventID: string(delivery.event.ID), ConsumerID: string(consumer.ID),
	})
	if updateErr != nil {
		d.logger.Error("mark event delivery failed", "eventId", delivery.event.ID, "consumerId", consumer.ID, "error", updateErr)
	}
}

func backoff(attempt int) time.Duration {
	seconds := math.Pow(2, float64(max(attempt-1, 0)))
	return min(time.Duration(seconds)*time.Second, 5*time.Minute)
}

func truncateError(err error) string {
	const maxLength = 2000
	message := err.Error()
	if len(message) > maxLength {
		return message[:maxLength]
	}
	return message
}
