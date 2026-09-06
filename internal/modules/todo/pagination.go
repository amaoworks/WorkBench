package todo

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strconv"

	"workbench/internal/foundation/httpapi"
	todosqlc "workbench/internal/modules/todo/sqlc"
)

type taskCursor struct {
	Version int    `json:"v"`
	Done    int64  `json:"d"`
	Due     int64  `json:"u"`
	Created int64  `json:"c"`
	ID      string `json:"i"`
}

var cursorID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (m *Module) listTasks(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			httpapi.Error(w, 400, "invalid_pagination", "limit 必须为正整数")
			return
		}
		limit = min(n, 100)
	}
	params := todosqlc.ListTasksPageParams{PageLimit: int64(limit + 1)}
	if encoded := r.URL.Query().Get("cursor"); encoded != "" {
		raw, err := base64.RawURLEncoding.DecodeString(encoded)
		var cursor taskCursor
		if len(encoded) > 512 || err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 ||
			(cursor.Done != 0 && cursor.Done != 1) || cursor.Created < 0 || !cursorID.MatchString(cursor.ID) {
			httpapi.Error(w, 400, "invalid_cursor", "无效的分页游标")
			return
		}
		params.HasCursor, params.Done, params.Due, params.Created, params.CursorID = 1, cursor.Done, cursor.Due, cursor.Created, cursor.ID
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
		cursor := taskCursor{Version: 1, Due: math.MaxInt64, Created: last.CreatedAt, ID: last.ID}
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
