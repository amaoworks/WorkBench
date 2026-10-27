package todo

import (
	"context"
	"net/http"
	"strings"
	"time"

	"workbench/internal/foundation/httpapi"
	todosqlc "workbench/internal/modules/todo/sqlc"
)

const (
	agendaTaskLimit    = 8
	agendaRenewalLimit = 5
	wallosTitlePrefix  = "Wallos续费提醒 - "
)

type AgendaTask struct {
	Task
	Overdue bool `json:"overdue"`
}

type AgendaRenewal struct {
	TaskID      string     `json:"taskId"`
	Name        string     `json:"name"`
	PaymentDate string     `json:"paymentDate"`
	Amount      string     `json:"amount,omitempty"`
	DueAt       *time.Time `json:"dueAt,omitempty"`
}

// Agenda is the overview card for today's work: open tasks due before the end
// of today (overdue included) and, when Wallos is enabled, upcoming renewals.
type Agenda struct {
	TimeZone string       `json:"timeZone"`
	Total    int64        `json:"total"`
	Overdue  int64        `json:"overdue"`
	Items    []AgendaTask `json:"items"`
	// Renewals is omitted when Wallos is not enabled so the card can hide the section.
	Renewals       []AgendaRenewal `json:"renewals,omitempty"`
	Wallos         bool            `json:"wallos"`
	CompletedToday int64           `json:"completedToday"`
}

func (m *Module) agenda(ctx context.Context, zone string) (Agenda, error) {
	settings, err := m.loadWallos(ctx)
	if err != nil {
		return Agenda{}, err
	}
	loc, err := time.LoadLocation(zone)
	if zone == "" || err != nil {
		zone = settings.TimeZone
		if loc, err = time.LoadLocation(zone); err != nil || zone == "" {
			zone, loc = "UTC", time.UTC
		}
	}
	now := m.now().In(loc)
	endOfDay := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	completedToday, err := m.queries.CountCompletedBetween(ctx, todosqlc.CountCompletedBetweenParams{
		CompletedAt: toNullTime(&startOfDay), CompletedAt_2: toNullTime(&endOfDay),
	})
	if err != nil {
		return Agenda{}, err
	}
	counts, err := m.queries.CountAgendaTasks(ctx, todosqlc.CountAgendaTasksParams{NowMs: now.UnixMilli(), DueBefore: endOfDay.UnixMilli()})
	if err != nil {
		return Agenda{}, err
	}
	rows, err := m.queries.ListAgendaTasks(ctx, todosqlc.ListAgendaTasksParams{DueBefore: endOfDay.UnixMilli(), RowLimit: agendaTaskLimit})
	if err != nil {
		return Agenda{}, err
	}
	out := Agenda{TimeZone: zone, Total: counts.Total, Overdue: counts.Overdue, CompletedToday: completedToday, Items: make([]AgendaTask, 0, len(rows)), Wallos: settings.Enabled}
	listed := map[string]bool{}
	for _, row := range rows {
		task := taskFromRow(row)
		listed[task.ID] = true
		out.Items = append(out.Items, AgendaTask{Task: task, Overdue: task.DueAt != nil && task.DueAt.Before(now)})
	}
	if !settings.Enabled {
		return out, nil
	}
	renewals, err := m.queries.ListUpcomingWallos(ctx, todosqlc.ListUpcomingWallosParams{FromDate: now.Format("2006-01-02"), RowLimit: agendaRenewalLimit + agendaTaskLimit})
	if err != nil {
		return Agenda{}, err
	}
	out.Renewals = []AgendaRenewal{}
	for _, row := range renewals {
		if listed[row.ID] || len(out.Renewals) >= agendaRenewalLimit {
			continue
		}
		out.Renewals = append(out.Renewals, AgendaRenewal{TaskID: row.ID, Name: strings.TrimSpace(strings.TrimPrefix(row.Title, wallosTitlePrefix)),
			PaymentDate: row.PaymentDate, Amount: descriptionField(row.Description, "金额"), DueAt: nullableTime(row.DueAt)})
	}
	return out, nil
}

// descriptionField reads one "label: value" line from a Wallos task description.
func descriptionField(description, label string) string {
	for _, line := range strings.Split(description, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), label+":"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (m *Module) agendaHTTP(w http.ResponseWriter, r *http.Request) {
	out, err := m.agenda(r.Context(), r.URL.Query().Get("tz"))
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "todo_agenda_failed", "无法读取今日待办")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, out)
}
