package todo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"workbench/internal/contracts"
	"workbench/internal/modules/todo/sqlc"
)

func (m *Module) forActiveUsers(ctx context.Context, run func(context.Context) error) error {
	if m.users == nil {
		return errors.New("todo background jobs require a user directory")
	}
	ids, err := m.users.ActiveUserIDs(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if id == "" {
			failures = append(failures, contracts.ErrUnauthenticated)
			continue
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
			failures = append(failures, fmt.Errorf("todo user %s: %w", id, err))
		}
	}
	return errors.Join(failures...)
}

func (m *Module) scanDue(ctx context.Context, _ contracts.JobRun) error {
	return m.forActiveUsers(ctx, m.scanDueUser)
}

func (m *Module) scanDueUser(ctx context.Context) error {
	userID, err := contracts.RequireUserID(ctx)
	if err != nil {
		return err
	}
	page := todosqlc.ListDueTasksParams{
		UserID:     userID,
		ScanBefore: sql.NullInt64{Int64: m.now().UTC().UnixMilli(), Valid: true},
	}
	for {
		rows, err := m.queries.ListDueTasks(ctx, page)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, task := range rows {
			active, err := m.users.IsUserActive(ctx, userID)
			if err != nil {
				return err
			}
			if !active {
				return nil
			}
			_, err = m.notifications.Create(ctx, contracts.NewNotification{
				RecipientID: userID, SourceModule: "todo", Severity: contracts.NotificationWarning,
				Title: "待办已到期", Content: task.Title, ActionLabel: "查看待办", ActionRoute: "/todo",
				External:       true,
				IdempotencyKey: fmt.Sprintf("todo:due:%s:%d", task.ID, task.DueAt.Int64),
			})
			if err != nil {
				return err
			}
		}
		last := rows[len(rows)-1]
		page.HasCursor, page.CursorDue, page.CursorID = 1, last.DueAt.Int64, last.ID
	}
}
