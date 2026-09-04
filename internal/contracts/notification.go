package contracts

import (
	"context"
	"database/sql"
	"time"
)

type NotificationSeverity string

const (
	NotificationInfo    NotificationSeverity = "info"
	NotificationSuccess NotificationSeverity = "success"
	NotificationWarning NotificationSeverity = "warning"
	NotificationError   NotificationSeverity = "error"
)

type NewNotification struct {
	SourceModule   ModuleID
	Severity       NotificationSeverity
	Title          string
	Content        string
	ActionLabel    string
	ActionRoute    string
	IdempotencyKey string
	SourceEventID  EventID
	ExpiresAt      *time.Time
}

type Notification struct {
	ID             string               `json:"id"`
	SourceModule   ModuleID             `json:"sourceModule"`
	Severity       NotificationSeverity `json:"severity"`
	Title          string               `json:"title"`
	Content        string               `json:"content"`
	ActionLabel    string               `json:"actionLabel,omitempty"`
	ActionRoute    string               `json:"actionRoute,omitempty"`
	IdempotencyKey string               `json:"-"`
	SourceEventID  EventID              `json:"sourceEventId,omitempty"`
	CreatedAt      time.Time            `json:"createdAt"`
	ReadAt         *time.Time           `json:"readAt,omitempty"`
	ArchivedAt     *time.Time           `json:"archivedAt,omitempty"`
	ExpiresAt      *time.Time           `json:"expiresAt,omitempty"`
}

type NotificationQuery struct {
	UnreadOnly bool
	Limit      int
	Cursor     string
}

type NotificationPage struct {
	Items      []Notification `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type NotificationService interface {
	Create(context.Context, NewNotification) (Notification, error)
	CreateTx(context.Context, *sql.Tx, NewNotification) (Notification, error)
	List(context.Context, NotificationQuery) (NotificationPage, error)
	MarkRead(context.Context, []string) error
	MarkAllRead(context.Context, time.Time) error
	Archive(context.Context, []string) error
	UnreadCount(context.Context) (int64, error)
}

type NotificationChannel interface {
	ID() string
	Deliver(context.Context, Notification) error
}
