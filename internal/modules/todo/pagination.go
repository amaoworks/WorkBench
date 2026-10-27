package todo

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"workbench/internal/foundation/httpapi"
	todosqlc "workbench/internal/modules/todo/sqlc"
)

type taskCursor struct {
	Version  int    `json:"v"`
	Done     int64  `json:"d"`
	Priority int64  `json:"p"`
	Due      int64  `json:"u"`
	Created  int64  `json:"c"`
	ID       string `json:"i"`
}

var cursorID = regexp.MustCompile(`^[a-f0-9]{32}$`)

var taskViews = map[string]int64{"": 0, "all": 0, "today": 1, "week": 2, "later": 3, "done": 4, "overdue": 5}

type TaskCounts struct {
	Overdue int64 `json:"overdue"`
	Today   int64 `json:"today"`
	Week    int64 `json:"week"`
	Later   int64 `json:"later"`
	None    int64 `json:"none"`
	Done    int64 `json:"done"`
}

func taskTimeBoundaries(now time.Time, zone string) (time.Time, time.Time, error) {
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "Local" {
		return time.Time{}, time.Time{}, errors.New("invalid timezone")
	}
	localNow := now.In(loc)
	startToday := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	endToday := startToday.AddDate(0, 0, 1)
	daysUntilMonday := (8 - int(startToday.Weekday())) % 7
	if daysUntilMonday == 0 {
		daysUntilMonday = 7
	}
	return endToday, startToday.AddDate(0, 0, daysUntilMonday), nil
}

func taskListFilters(r *http.Request) (int64, string, string, time.Time, time.Time, error) {
	view, ok := taskViews[r.URL.Query().Get("view")]
	if !ok {
		return 0, "", "", time.Time{}, time.Time{}, errors.New("invalid view")
	}
	priority := strings.TrimSpace(r.URL.Query().Get("priority"))
	if priority != "" {
		if _, _, err := normalizeTaskMetadata(priority, ""); err != nil {
			return 0, "", "", time.Time{}, time.Time{}, err
		}
	}
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	_, category, err := normalizeTaskMetadata("", category)
	if err != nil {
		return 0, "", "", time.Time{}, time.Time{}, err
	}
	endToday, endWeek, err := taskTimeBoundaries(time.Now(), r.URL.Query().Get("tz"))
	return view, priority, category, endToday, endWeek, err
}

func (m *Module) listTasks(w http.ResponseWriter, r *http.Request) {
	view, priority, category, endToday, endWeek, err := taskListFilters(r)
	if err != nil {
		httpapi.Error(w, 400, "invalid_filter", err.Error())
		return
	}
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			httpapi.Error(w, 400, "invalid_pagination", "limit 必须为正整数")
			return
		}
		limit = min(n, 100)
	}
	now := m.now().UTC()
	// Recompute boundaries from the module clock so tests and callers share one instant.
	endToday, endWeek, err = taskTimeBoundaries(now, r.URL.Query().Get("tz"))
	if err != nil {
		httpapi.Error(w, 400, "invalid_filter", err.Error())
		return
	}
	params := todosqlc.ListTasksPageParams{
		View: view, NowMs: now.UnixMilli(), EndToday: endToday.UnixMilli(), EndWeek: endWeek.UnixMilli(),
		PriorityFilter: priority, CategoryFilter: category, PageLimit: int64(limit + 1),
	}
	if encoded := r.URL.Query().Get("cursor"); encoded != "" {
		raw, err := base64.RawURLEncoding.DecodeString(encoded)
		var cursor taskCursor
		if len(encoded) > 512 || err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Version != 2 ||
			(cursor.Done != 0 && cursor.Done != 1) || cursor.Priority < 0 || cursor.Priority > 3 || cursor.Created < 0 || !cursorID.MatchString(cursor.ID) {
			httpapi.Error(w, 400, "invalid_cursor", "无效的分页游标")
			return
		}
		params.HasCursor, params.Done, params.PriorityRank, params.Due, params.Created, params.CursorID =
			1, cursor.Done, cursor.Priority, cursor.Due, cursor.Created, cursor.ID
	}
	rows, err := m.queries.ListTasksPage(r.Context(), params)
	if err != nil {
		httpapi.Error(w, 500, "todo_list_failed", "could not list tasks")
		return
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		cursor := taskCursor{Version: 2, Priority: taskPriorityRank(last.Priority), Due: math.MaxInt64, Created: last.CreatedAt, ID: last.ID}
		if last.CompletedAt.Valid {
			cursor.Done = 1
		}
		if last.DueAt.Valid {
			cursor.Due = last.DueAt.Int64
		}
		raw, _ := json.Marshal(cursor)
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	tasks := make([]Task, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, taskFromRow(row))
	}
	httpapi.Write(w, 200, struct {
		Items      []Task `json:"items"`
		NextCursor string `json:"nextCursor,omitempty"`
	}{tasks, next})
}

func taskPriorityRank(priority string) int64 {
	switch priority {
	case "high":
		return 0
	case "medium":
		return 1
	case "low":
		return 2
	default:
		return 3
	}
}

func (m *Module) taskCounts(w http.ResponseWriter, r *http.Request) {
	_, priority, category, _, _, err := taskListFilters(r)
	if err != nil {
		httpapi.Error(w, 400, "invalid_filter", err.Error())
		return
	}
	now := m.now().UTC()
	endToday, endWeek, err := taskTimeBoundaries(now, r.URL.Query().Get("tz"))
	if err != nil {
		httpapi.Error(w, 400, "invalid_filter", err.Error())
		return
	}
	row, err := m.queries.CountTaskGroups(r.Context(), todosqlc.CountTaskGroupsParams{
		NowMs: now.UnixMilli(), EndToday: endToday.UnixMilli(), EndWeek: endWeek.UnixMilli(),
		PriorityFilter: priority, CategoryFilter: category,
	})
	if err != nil {
		httpapi.Error(w, 500, "todo_counts_failed", "could not count tasks")
		return
	}
	httpapi.Write(w, 200, TaskCounts(row))
}
