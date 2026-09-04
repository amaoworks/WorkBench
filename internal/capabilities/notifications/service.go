package notifications

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/identity"
)

type Service struct {
	db      *sql.DB
	queries *dbsqlc.Queries
	events  contracts.EventPublisher
	now     func() time.Time
	newID   func() (string, error)
}

func NewService(db *sql.DB, events contracts.EventPublisher) (*Service, error) {
	if db == nil || events == nil {
		return nil, errors.New("database and event publisher are required")
	}
	return &Service{db: db, queries: dbsqlc.New(db), events: events, now: time.Now, newID: identity.New}, nil
}

func (s *Service) Create(ctx context.Context, input contracts.NewNotification) (contracts.Notification, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.Notification{}, fmt.Errorf("begin notification transaction: %w", err)
	}
	defer tx.Rollback()
	notification, err := s.CreateTx(ctx, tx, input)
	if err != nil {
		return contracts.Notification{}, err
	}
	if err := tx.Commit(); err != nil {
		return contracts.Notification{}, fmt.Errorf("commit notification: %w", err)
	}
	return notification, nil
}

func (s *Service) CreateTx(ctx context.Context, tx *sql.Tx, input contracts.NewNotification) (contracts.Notification, error) {
	if tx == nil {
		return contracts.Notification{}, errors.New("transaction is required")
	}
	if err := validateNew(input); err != nil {
		return contracts.Notification{}, err
	}
	id, err := s.newID()
	if err != nil {
		return contracts.Notification{}, fmt.Errorf("generate notification id: %w", err)
	}
	now := s.now().UTC()
	var expiresAt sql.NullInt64
	if input.ExpiresAt != nil {
		expiresAt = sql.NullInt64{Int64: input.ExpiresAt.UTC().UnixMilli(), Valid: true}
	}
	queries := dbsqlc.New(tx)
	rows, err := queries.InsertNotification(ctx, dbsqlc.InsertNotificationParams{
		ID: id, SourceModule: string(input.SourceModule), Severity: string(input.Severity),
		Title: input.Title, Content: input.Content, ActionLabel: input.ActionLabel,
		ActionRoute: input.ActionRoute, IdempotencyKey: input.IdempotencyKey,
		SourceEventID: string(input.SourceEventID), CreatedAt: now.UnixMilli(), ExpiresAt: expiresAt,
	})
	if err != nil {
		return contracts.Notification{}, fmt.Errorf("insert notification: %w", err)
	}
	if rows == 0 {
		return getByIdempotencyKey(ctx, queries, input.IdempotencyKey)
	}

	payload, _ := json.Marshal(map[string]string{"notificationId": id})
	if _, err := s.events.PublishTx(ctx, tx, contracts.NewEvent{
		Topic: "core.notification.created", SchemaVersion: 1, SourceModule: "core", AggregateID: id, Payload: payload,
	}); err != nil {
		return contracts.Notification{}, fmt.Errorf("publish notification event: %w", err)
	}
	return contracts.Notification{
		ID: id, SourceModule: input.SourceModule, Severity: input.Severity,
		Title: input.Title, Content: input.Content, ActionLabel: input.ActionLabel,
		ActionRoute: input.ActionRoute, IdempotencyKey: input.IdempotencyKey,
		SourceEventID: input.SourceEventID, CreatedAt: now, ExpiresAt: input.ExpiresAt,
	}, nil
}

func validateNew(input contracts.NewNotification) error {
	if input.SourceModule == "" || input.IdempotencyKey == "" {
		return errors.New("source module and idempotency key are required")
	}
	if input.Title == "" || len(input.Title) > 200 {
		return errors.New("notification title must contain 1 to 200 bytes")
	}
	if len(input.Content) > 10_000 {
		return errors.New("notification content exceeds 10000 bytes")
	}
	switch input.Severity {
	case contracts.NotificationInfo, contracts.NotificationSuccess, contracts.NotificationWarning, contracts.NotificationError:
	default:
		return fmt.Errorf("invalid notification severity %q", input.Severity)
	}
	if input.ActionRoute != "" {
		u, err := url.Parse(input.ActionRoute)
		if err != nil || !strings.HasPrefix(input.ActionRoute, "/") || strings.HasPrefix(input.ActionRoute, "//") || u.IsAbs() || u.Host != "" {
			return errors.New("action route must be a site-relative path")
		}
	}
	return nil
}

func getByIdempotencyKey(ctx context.Context, queries *dbsqlc.Queries, key string) (contracts.Notification, error) {
	row, err := queries.GetNotificationByIdempotencyKey(ctx, key)
	if err != nil {
		return contracts.Notification{}, err
	}
	return contracts.Notification{
		ID: row.ID, SourceModule: contracts.ModuleID(row.SourceModule), Severity: contracts.NotificationSeverity(row.Severity),
		Title: row.Title, Content: row.Content, ActionLabel: row.ActionLabel, ActionRoute: row.ActionRoute,
		IdempotencyKey: row.IdempotencyKey, SourceEventID: contracts.EventID(row.SourceEventID),
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), ReadAt: nullableTime(row.ReadAt),
		ArchivedAt: nullableTime(row.ArchivedAt), ExpiresAt: nullableTime(row.ExpiresAt),
	}, nil
}

const notificationSelect = `
	SELECT id, source_module, severity, title, content,
	       COALESCE(action_label, ''), COALESCE(action_route, ''), idempotency_key,
	       COALESCE(source_event_id, ''), created_at, read_at, archived_at, expires_at
	FROM notifications`

type scanner interface{ Scan(...any) error }

func scanNotification(row scanner) (contracts.Notification, error) {
	var notification contracts.Notification
	var createdAt int64
	var readAt, archivedAt, expiresAt sql.NullInt64
	err := row.Scan(
		&notification.ID, &notification.SourceModule, &notification.Severity,
		&notification.Title, &notification.Content, &notification.ActionLabel,
		&notification.ActionRoute, &notification.IdempotencyKey, &notification.SourceEventID,
		&createdAt, &readAt, &archivedAt, &expiresAt,
	)
	if err != nil {
		return contracts.Notification{}, err
	}
	notification.CreatedAt = time.UnixMilli(createdAt).UTC()
	notification.ReadAt = nullableTime(readAt)
	notification.ArchivedAt = nullableTime(archivedAt)
	notification.ExpiresAt = nullableTime(expiresAt)
	return notification, nil
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.UnixMilli(value.Int64).UTC()
	return &result
}

type cursor struct {
	CreatedAt int64  `json:"createdAt"`
	ID        string `json:"id"`
}

func (s *Service) List(ctx context.Context, query contracts.NotificationQuery) (contracts.NotificationPage, error) {
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 100 {
		query.Limit = 100
	}
	position := cursor{CreatedAt: math.MaxInt64, ID: "\uffff"}
	if query.Cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(query.Cursor)
		if err != nil || json.Unmarshal(decoded, &position) != nil || position.ID == "" {
			return contracts.NotificationPage{}, errors.New("invalid notification cursor")
		}
	}

	statement := notificationSelect + `
		WHERE archived_at IS NULL
		  AND (created_at < ? OR (created_at = ? AND id < ?))`
	args := []any{position.CreatedAt, position.CreatedAt, position.ID}
	if query.UnreadOnly {
		statement += " AND read_at IS NULL"
	}
	statement += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, query.Limit+1)
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return contracts.NotificationPage{}, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	page := contracts.NotificationPage{Items: make([]contracts.Notification, 0, query.Limit)}
	for rows.Next() {
		notification, err := scanNotification(rows)
		if err != nil {
			return contracts.NotificationPage{}, fmt.Errorf("scan notification: %w", err)
		}
		page.Items = append(page.Items, notification)
	}
	if err := rows.Err(); err != nil {
		return contracts.NotificationPage{}, err
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		encoded, _ := json.Marshal(cursor{CreatedAt: last.CreatedAt.UnixMilli(), ID: last.ID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return page, nil
}

func (s *Service) MarkRead(ctx context.Context, ids []string) error {
	return s.updateMany(ctx, ids, "read_at", "core.notification.updated")
}

func (s *Service) Archive(ctx context.Context, ids []string) error {
	return s.updateMany(ctx, ids, "archived_at", "core.notification.archived")
}

func (s *Service) updateMany(ctx context.Context, ids []string, column, topic string) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > 100 {
		return errors.New("at most 100 notification ids may be updated")
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	args := []any{s.now().UTC().UnixMilli()}
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE notifications SET "+column+" = ? WHERE id IN ("+placeholders+")", args...); err != nil {
		return fmt.Errorf("update notifications: %w", err)
	}
	payload, _ := json.Marshal(map[string][]string{"notificationIds": ids})
	if _, err := s.events.PublishTx(ctx, tx, contracts.NewEvent{
		Topic: topic, SchemaVersion: 1, SourceModule: "core", Payload: payload,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) MarkAllRead(ctx context.Context, before time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC().UnixMilli()
	if err := dbsqlc.New(tx).MarkAllNotificationsRead(ctx, dbsqlc.MarkAllNotificationsReadParams{
		ReadAt: sql.NullInt64{Int64: now, Valid: true}, CreatedAt: before.UTC().UnixMilli(),
	}); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]int64{"before": before.UTC().UnixMilli()})
	if _, err := s.events.PublishTx(ctx, tx, contracts.NewEvent{
		Topic: "core.notification.updated", SchemaVersion: 1, SourceModule: "core", Payload: payload,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) UnreadCount(ctx context.Context) (int64, error) {
	return s.queries.CountUnreadNotifications(ctx, sql.NullInt64{Int64: s.now().UTC().UnixMilli(), Valid: true})
}
