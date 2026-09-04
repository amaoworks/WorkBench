package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/identity"
)

var topicPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

type Store struct {
	db    *sql.DB
	wake  chan struct{}
	now   func() time.Time
	newID func() (string, error)
}

func NewStore(db *sql.DB) *Store {
	return &Store{
		db:    db,
		wake:  make(chan struct{}, 1),
		now:   time.Now,
		newID: identity.New,
	}
}

func (s *Store) Wake() <-chan struct{} { return s.wake }

func (s *Store) Publish(ctx context.Context, event contracts.NewEvent) (contracts.EventID, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin event transaction: %w", err)
	}
	defer tx.Rollback()
	id, err := s.PublishTx(ctx, tx, event)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit event: %w", err)
	}
	s.notify()
	return id, nil
}

func (s *Store) PublishTx(ctx context.Context, tx *sql.Tx, event contracts.NewEvent) (contracts.EventID, error) {
	if tx == nil {
		return "", errors.New("transaction is required")
	}
	if !topicPattern.MatchString(event.Topic) {
		return "", fmt.Errorf("invalid event topic %q", event.Topic)
	}
	if event.SchemaVersion < 1 {
		return "", errors.New("event schema version must be positive")
	}
	if event.SourceModule == "" {
		return "", errors.New("event source module is required")
	}
	if !strings.HasPrefix(event.Topic, string(event.SourceModule)+".") {
		return "", fmt.Errorf("event topic %q does not belong to source module %q", event.Topic, event.SourceModule)
	}
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(event.Payload) {
		return "", errors.New("event payload is not valid JSON")
	}
	id, err := s.newID()
	if err != nil {
		return "", fmt.Errorf("generate event id: %w", err)
	}
	now := s.now().UTC().UnixMilli()
	err = dbsqlc.New(tx).InsertEvent(ctx, dbsqlc.InsertEventParams{
		ID: id, Topic: event.Topic, SchemaVersion: int64(event.SchemaVersion),
		SourceModule: string(event.SourceModule), AggregateID: event.AggregateID,
		PayloadJson: string(event.Payload), OccurredAt: now, AvailableAt: now, CreatedAt: now,
	})
	if err != nil {
		return "", fmt.Errorf("insert event: %w", err)
	}
	// This is only a low-latency hint. If the outer transaction has not committed
	// yet, the dispatcher's periodic database poll still guarantees discovery.
	s.notify()
	return contracts.EventID(id), nil
}

func (s *Store) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
