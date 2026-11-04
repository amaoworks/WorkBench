package auth

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"workbench/internal/contracts"
	dbsqlc "workbench/internal/foundation/database/sqlc"
)

func (s *Service) verifyLogin(ctx context.Context, username, password string) (dbsqlc.AuthUser, bool, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	user, err := s.queries.GetAuthUserByUsername(ctx, username)
	missing := errors.Is(err, sql.ErrNoRows)
	if err != nil && !missing {
		return user, false, err
	}
	hash := user.PasswordHash
	if missing {
		// Unknown names still perform the same password calculation.
		owner, err := s.queries.GetAuthUser(ctx, contracts.LegacyUserID)
		if err != nil {
			return user, false, err
		}
		hash = owner.PasswordHash
	}
	valid, err := verifyPassword(password, hash)
	if err != nil {
		return user, false, err
	}
	return user, !missing && valid && user.Status == "active", nil
}

func (s *Service) acquireLogin(r *http.Request, username string) (int, error) {
	source, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		source = r.RemoteAddr
	}
	keys := []string{"source:" + source, "user:" + strings.ToLower(strings.TrimSpace(username))}
	l := s.passwordLimit
	l.mu.Lock()
	now := l.now()
	if l.loginKeys == nil {
		l.loginKeys = make(map[string]loginBucket)
	}
	if len(l.loginKeys) >= 4096 {
		for key, b := range l.loginKeys {
			if now.Sub(b.at) > 15*time.Minute {
				delete(l.loginKeys, key)
			}
		}
		if len(l.loginKeys) >= 4096 {
			l.mu.Unlock()
			return 1, errLoginRateLimited
		}
	}
	for _, key := range keys {
		b, exists := l.loginKeys[key]
		if !exists {
			b = loginBucket{tokens: loginBurst, at: now}
		}
		if intervals := now.Sub(b.at) / loginRefillInterval; intervals > 0 {
			b.tokens = min(loginBurst, b.tokens+int(intervals))
			b.at = b.at.Add(intervals * loginRefillInterval)
		}
		l.loginKeys[key] = b
		if b.tokens == 0 {
			l.mu.Unlock()
			return max(1, int((loginRefillInterval-now.Sub(b.at)+time.Second-1)/time.Second)), errLoginRateLimited
		}
	}
	if l.active >= maxPasswordConcurrency {
		l.mu.Unlock()
		return 1, errPasswordBusy
	}
	for _, key := range keys {
		b := l.loginKeys[key]
		b.tokens--
		l.loginKeys[key] = b
	}
	l.active++
	l.mu.Unlock()
	return 0, nil
}

type loginBucket struct {
	tokens int
	at     time.Time
}
