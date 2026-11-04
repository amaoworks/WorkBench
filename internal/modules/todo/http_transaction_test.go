package todo

import (
	"database/sql"
	"net/http/httptest"
	"testing"
)

type writingResponse struct {
	*httptest.ResponseRecorder
	db  *sql.DB
	err error
}

func (w *writingResponse) WriteHeader(code int) {
	_, w.err = w.db.Exec(`INSERT INTO todo_create_requests(user_id, request_key, request_hash, response_json) VALUES ('session', 'write', 'hash', '{}')`)
	w.ResponseRecorder.WriteHeader(code)
}

func TestDeleteClosesTransactionBeforeHTTPError(t *testing.T) {
	for _, scenario := range []string{"foreign task", "event failure"} {
		t.Run(scenario, func(t *testing.T) {
			m, db := openTodoModule(t)
			task, err := m.Create(userContext("alice"), CreateTask{Title: "Private"})
			if err != nil {
				t.Fatal(err)
			}
			user, want := "bob", 404
			if scenario == "event failure" {
				m.events = failingPublisher{}
				user, want = "alice", 500
			}
			r := userRequest(user, "DELETE", "/", "")
			r.SetPathValue("id", task.ID)
			w := &writingResponse{ResponseRecorder: httptest.NewRecorder(), db: db.SQL()}
			m.deleteTask(w, r)
			if w.Code != want || w.err != nil {
				t.Fatalf("status=%d want=%d response-time database write=%v", w.Code, want, w.err)
			}
			var count int
			if err := db.SQL().QueryRow("SELECT COUNT(*) FROM todo_tasks WHERE id=?", task.ID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("failed delete modified task: %d %v", count, err)
			}
		})
	}
}
