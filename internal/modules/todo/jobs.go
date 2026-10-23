package todo

import (
	"context"
	"database/sql"
	"fmt"

	"workbench/internal/contracts"
	"workbench/internal/modules/todo/sqlc"
)

func (m *Module) scanDue(ctx context.Context, _ contracts.JobRun) error {
	page := todosqlc.ListDueTasksParams{
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
			_, err := m.notifications.Create(ctx, contracts.NewNotification{
				SourceModule: "todo", Severity: contracts.NotificationWarning,
				Title: "待办已到期", Content: task.Title, ActionLabel: "查看待办", ActionRoute: "/todo",
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
