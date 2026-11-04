package contracts

import (
	"context"
	"errors"
	"testing"
)

func TestPrincipalRequiresExplicitIdentity(t *testing.T) {
	ctx := context.Background()
	if id, err := RequireUserID(ctx); id != "" || !errors.Is(err, ErrUnauthenticated) || IsAdmin(ctx) {
		t.Fatalf("missing identity acquired authority: id=%q err=%v", id, err)
	}
	ctx = WithPrincipal(ctx, Principal{Role: RoleAdmin})
	if _, ok := CurrentPrincipal(ctx); ok || IsAdmin(ctx) {
		t.Fatal("role without a user ID must not authorize a request")
	}
}

func TestPrincipalKeepsUserRoleAndSession(t *testing.T) {
	admin := Principal{UserID: "alice", Role: RoleAdmin, SessionID: "session-a"}
	ctx := WithPrincipal(context.Background(), admin)
	if got, ok := CurrentPrincipal(ctx); !ok || got != admin || !IsAdmin(ctx) {
		t.Fatalf("principal not preserved: %+v %v", got, ok)
	}
	child := WithPrincipal(ctx, Principal{UserID: "bob", Role: RoleUser})
	if id, err := RequireUserID(child); err != nil || id != "bob" || IsAdmin(child) {
		t.Fatalf("child inherited another user's authority: id=%q err=%v", id, err)
	}
	if UserID(ctx) != "alice" {
		t.Fatal("changing child identity mutated its parent")
	}
}
