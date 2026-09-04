package contracts

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type EventID string
type ConsumerID string

type Event struct {
	ID            EventID         `json:"id"`
	Topic         string          `json:"topic"`
	SchemaVersion int             `json:"schemaVersion"`
	SourceModule  ModuleID        `json:"sourceModule"`
	AggregateID   string          `json:"aggregateId,omitempty"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Payload       json.RawMessage `json:"payload"`
}

type NewEvent struct {
	Topic         string
	SchemaVersion int
	SourceModule  ModuleID
	AggregateID   string
	Payload       json.RawMessage
}

type EventPublisher interface {
	Publish(ctx context.Context, event NewEvent) (EventID, error)
	PublishTx(ctx context.Context, tx *sql.Tx, event NewEvent) (EventID, error)
}

type EventConsumer struct {
	ID          ConsumerID
	Module      ModuleID
	Topics      []string
	Timeout     time.Duration
	MaxAttempts int
	Handler     func(context.Context, Event) error
}
