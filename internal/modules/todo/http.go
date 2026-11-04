package todo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
	todosqlc "workbench/internal/modules/todo/sqlc"
)

func (m *Module) createTask(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireHTTPUser(w, r); !ok {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) > 1 || (len(keys) == 1 && !validIdempotencyKey(keys[0])) {
		httpapi.Error(w, http.StatusBadRequest, "invalid_idempotency_key", "invalid Idempotency-Key")
		return
	}
	var input CreateTask
	if err := httpapi.Decode(w, r, &input, 16*1024); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid task")
		return
	}
	if len(keys) == 1 {
		input.IdempotencyKey = keys[0]
	}
	task, err := m.Create(r.Context(), input)
	if errors.Is(err, ErrIdempotencyConflict) {
		httpapi.Error(w, http.StatusConflict, "idempotency_conflict", err.Error())
		return
	}
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "todo_create_failed", err.Error())
		return
	}
	httpapi.Write(w, http.StatusCreated, task)
}

func requireHTTPUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := contracts.RequireUserID(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return "", false
	}
	return id, true
}

func validIdempotencyKey(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for index := range len(value) {
		if value[index] < 0x20 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

func (m *Module) updateTask(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireHTTPUser(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	var input UpdateTask
	if id == "" || httpapi.Decode(w, r, &input, 16*1024) != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid task update")
		return
	}
	task, err := m.update(r.Context(), id, input)
	if errors.Is(err, ErrTaskConflict) {
		httpapi.Error(w, http.StatusConflict, "todo_conflict", "待办已被修改")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		httpapi.Error(w, http.StatusNotFound, "todo_not_found", "task not found")
		return
	}
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "todo_update_failed", err.Error())
		return
	}
	httpapi.Write(w, http.StatusOK, task)
}

func (m *Module) deleteTask(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireHTTPUser(w, r); !ok {
		return
	}
	err := m.delete(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		httpapi.Error(w, http.StatusNotFound, "todo_not_found", "task not found")
		return
	}
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "todo_delete_failed", "could not delete task")
		return
	}
	httpapi.Write(w, http.StatusNoContent, nil)
}

func (m *Module) delete(ctx context.Context, id string) error {
	userID, err := contracts.RequireUserID(ctx)
	if err != nil {
		return err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := m.queries.WithTx(tx).DeleteTask(ctx, todosqlc.DeleteTaskParams{ID: id, UserID: userID})
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	payload, _ := json.Marshal(map[string]string{"taskId": id})
	if _, err := m.events.PublishTx(ctx, tx, contracts.NewEvent{
		UserID: userID, Topic: "todo.task.deleted", SchemaVersion: 1, SourceModule: "todo", AggregateID: id, Payload: payload,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Module) summary(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireHTTPUser(w, r)
	if !ok {
		return
	}
	result, err := m.queries.TodoSummary(r.Context(), todosqlc.TodoSummaryParams{DueAt: sql.NullInt64{Int64: m.now().UTC().UnixMilli(), Valid: true}, UserID: userID})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "todo_summary_failed", "could not load summary")
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]int64{"open": result.Open, "overdue": result.Overdue, "completed": result.Completed})
}
