package todo

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAgendaListsOverdueAndTodayOnly(t *testing.T) {
	module, database := openTodoModule(t)
	ctx := context.Background()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("no tzdata")
	}
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, shanghai)
	module.now = func() time.Time { return now }
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	for _, input := range []CreateTask{
		{Title: "overdue", DueAt: at(-26 * time.Hour)},
		{Title: "later today", DueAt: at(8 * time.Hour)},
		{Title: "tomorrow", DueAt: at(15 * time.Hour)},
		{Title: "no due"},
	} {
		if _, err := module.Create(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	done, err := module.Create(ctx, CreateTask{Title: "done", DueAt: at(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	completed := true
	if _, err := module.update(ctx, done.ID, UpdateTask{Completed: &completed}); err != nil {
		t.Fatal(err)
	}
	agenda, err := module.agenda(ctx, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if agenda.Total != 2 || agenda.Overdue != 1 || len(agenda.Items) != 2 || agenda.Items[0].Title != "overdue" || !agenda.Items[0].Overdue || agenda.Items[1].Overdue {
		t.Fatalf("agenda %+v", agenda)
	}
	if agenda.Wallos || agenda.Renewals != nil {
		t.Fatalf("renewals must be hidden without Wallos: %+v", agenda)
	}
	// Enable Wallos and record one upcoming renewal occurrence.
	if _, err := database.SQL().Exec(`INSERT INTO todo_wallos_settings(id, config) VALUES (1, '{"enabled":true,"baseUrl":"https://wallos.example","timeZone":"Asia/Shanghai"}')`); err != nil {
		t.Fatal(err)
	}
	renewal, err := module.Create(ctx, CreateTask{Title: wallosTitlePrefix + "Netflix", Description: "分类: 娱乐\n金额: ¥68.00\n请及时付款！", DueAt: at(48 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().Exec(`INSERT INTO todo_wallos_occurrences(source, subscription_id, payment_date, task_id) VALUES ('wallos', '7', '2026-10-29', ?)`, renewal.ID); err != nil {
		t.Fatal(err)
	}
	agenda, err = module.agenda(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !agenda.Wallos || len(agenda.Renewals) != 1 || agenda.Renewals[0].Name != "Netflix" || agenda.Renewals[0].Amount != "¥68.00" || agenda.Renewals[0].PaymentDate != "2026-10-29" {
		t.Fatalf("renewals %+v", agenda.Renewals)
	}
}

func TestAgendaLimitsItemsAndKeepsTotal(t *testing.T) {
	module, _ := openTodoModule(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	module.now = func() time.Time { return now }

	for i := range 10 {
		due := now.Add(time.Duration(i+1) * time.Hour)
		if _, err := module.Create(ctx, CreateTask{Title: fmt.Sprintf("task-%02d", i), DueAt: &due}); err != nil {
			t.Fatal(err)
		}
	}

	agenda, err := module.agenda(ctx, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if agenda.Total != 10 {
		t.Fatalf("total = %d, want 10", agenda.Total)
	}
	if len(agenda.Items) != agendaTaskLimit {
		t.Fatalf("items = %d, want %d", len(agenda.Items), agendaTaskLimit)
	}
	if agenda.Items[0].Title != "task-00" || agenda.Items[7].Title != "task-07" {
		t.Fatalf("items are not the earliest eight tasks: %+v", agenda.Items)
	}
}
