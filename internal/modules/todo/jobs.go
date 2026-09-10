package todo

import (
	"context"
	"database/sql"
	"fmt"

	"workbench/internal/contracts"
)

func (m *Module) scanDue(ctx context.Context, _ contracts.JobRun) error {
	rows, err := m.queries.ListDueTasks(ctx, sql.NullInt64{Int64: m.now().UTC().UnixMilli(), Valid: true})
	if err != nil {
		return err
	}
	for _, task := range rows {
		_, err := m.notifications.Create(ctx, contracts.NewNotification{
			SourceModule: "todo", Severity: contracts.NotificationWarning,
			Title: "待办已到期", Content: task.Title, ActionLabel: "查看待办", ActionRoute: "/todo",
			IdempotencyKey: fmt.Sprintf("todo:due:%s:%d", task.ID, task.DueAt.Int64),
		})
		if err != nil {
			return err
		}
	}
	return nil
}
