package todo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
)

func (m *Module) createTask(w http.ResponseWriter, r *http.Request) {
	var input CreateTask
	if err := httpapi.Decode(w, r, &input, 16*1024); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid task")
		return
	}
	task, err := m.Create(r.Context(), input)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "todo_create_failed", err.Error())
		return
	}
	httpapi.Write(w, http.StatusCreated, task)
}

func (m *Module) updateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input UpdateTask
	if id == "" || httpapi.Decode(w, r, &input, 16*1024) != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid task update")
		return
	}
	task, err := m.update(r.Context(), id, input)
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
	id := r.PathValue("id")
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "todo_delete_failed", "could not delete task")
		return
	}
	defer tx.Rollback()
	rows, err := m.queries.WithTx(tx).DeleteTask(r.Context(), id)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "todo_delete_failed", "could not delete task")
		return
	}
	if rows == 0 {
		httpapi.Error(w, http.StatusNotFound, "todo_not_found", "task not found")
		return
	}
	payload, _ := json.Marshal(map[string]string{"taskId": id})
	if _, err := m.events.PublishTx(r.Context(), tx, contracts.NewEvent{
		Topic: "todo.task.deleted", SchemaVersion: 1, SourceModule: "todo", AggregateID: id, Payload: payload,
	}); err != nil || tx.Commit() != nil {
		httpapi.Error(w, http.StatusInternalServerError, "todo_delete_failed", "could not delete task")
		return
	}
	httpapi.Write(w, http.StatusNoContent, nil)
}

func (m *Module) summary(w http.ResponseWriter, r *http.Request) {
	result, err := m.queries.TodoSummary(r.Context(), sql.NullInt64{Int64: m.now().UTC().UnixMilli(), Valid: true})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "todo_summary_failed", "could not load summary")
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]int64{"open": result.Open, "overdue": result.Overdue, "completed": result.Completed})
}
