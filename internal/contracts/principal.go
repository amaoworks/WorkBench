package contracts

import (
	"context"
	"errors"
)

// LegacyUserID owns data migrated from the single-user workspace.
const LegacyUserID = "owner"

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

type Principal struct {
	UserID    string `json:"id"`
	Role      Role   `json:"role"`
	SessionID string `json:"-"`
}

type principalKey struct{}

var ErrUnauthenticated = errors.New("authenticated user is required")

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

func CurrentPrincipal(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok && principal.UserID != ""
}

func UserID(ctx context.Context) string {
	principal, _ := CurrentPrincipal(ctx)
	return principal.UserID
}

func RequireUserID(ctx context.Context) (string, error) {
	id := UserID(ctx)
	if id == "" {
		return "", ErrUnauthenticated
	}
	return id, nil
}

func IsAdmin(ctx context.Context) bool {
	principal, ok := CurrentPrincipal(ctx)
	return ok && principal.Role == RoleAdmin
}

// UserDirectory lets background work enumerate and recheck active users without
// importing authentication implementation or reading platform tables.
type UserDirectory interface {
	ActiveUserIDs(context.Context) ([]string, error)
	IsUserActive(context.Context, string) (bool, error)
}
