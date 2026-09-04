package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"workbench/internal/foundation/database/sqlc"
)

type SessionStore struct {
	queries *dbsqlc.Queries
	now     func() time.Time
}

func NewSessionStore(db *sql.DB) *SessionStore {
	return &SessionStore{queries: dbsqlc.New(db), now: time.Now}
}

func (s *SessionStore) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}

func (s *SessionStore) DeleteCtx(ctx context.Context, token string) error {
	return s.queries.DeleteSession(ctx, token)
}

func (s *SessionStore) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

func (s *SessionStore) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	data, err := s.queries.FindSession(ctx, dbsqlc.FindSessionParams{Token: token, Expiry: s.now().UTC().UnixMilli()})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (s *SessionStore) Commit(token string, data []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, data, expiry)
}

func (s *SessionStore) CommitCtx(ctx context.Context, token string, data []byte, expiry time.Time) error {
	return s.queries.UpsertSession(ctx, dbsqlc.UpsertSessionParams{Token: token, Data: data, Expiry: expiry.UTC().UnixMilli()})
}

func (s *SessionStore) DeleteExpired(ctx context.Context) error {
	return s.queries.DeleteExpiredSessions(ctx, s.now().UTC().UnixMilli())
}
