package news

import (
	"context"
	"errors"
	"fmt"

	"workbench/internal/contracts"
)

// Scheduled jobs operate on explicit active identities, independently of any
// principal attached to the scheduler's own context.
func (m *Module) forActiveUsers(ctx context.Context, run func(context.Context) error) error {
	ctx, cancel := m.operation(ctx)
	defer cancel()
	ids, err := m.users.ActiveUserIDs(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if id == "" {
			return contracts.ErrUnauthenticated
		}
		active, err := m.users.IsUserActive(ctx, id)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !active {
			continue
		}
		userCtx := contracts.WithPrincipal(ctx, contracts.Principal{UserID: id, Role: contracts.RoleUser})
		if err := run(userCtx); err != nil {
			failures = append(failures, fmt.Errorf("news user %s: %w", id, err))
		}
	}
	return errors.Join(failures...)
}
