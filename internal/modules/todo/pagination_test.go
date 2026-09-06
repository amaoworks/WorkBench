package todo

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestTaskCursorTraversesTiesAndRejectsInvalidInput(t *testing.T) {
	module, _ := openTodoModule(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	module.now = func() time.Time { return now }
	due := now.Add(time.Hour)
	for i := 0; i < 7; i++ {
		input := CreateTask{Title: "pagination"}
		if i < 4 {
			input.DueAt = &due
		}
		created, err := module.Create(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if i%3 == 0 {
			done := true
			if _, err := module.update(context.Background(), created.ID, UpdateTask{Completed: &done}); err != nil {
				t.Fatal(err)
			}
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		rec := httptest.NewRecorder()
		module.listTasks(rec, httptest.NewRequest("GET", "/?limit=2&cursor="+url.QueryEscape(cursor), nil))
		if rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
		var body struct {
			Items []Task `json:"items"`
			Next  string `json:"nextCursor"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Items) > 2 {
			t.Fatal("limit ignored")
		}
		for _, task := range body.Items {
			if seen[task.ID] {
				t.Fatal("duplicate task across pages")
			}
			seen[task.ID] = true
		}
		cursor = body.Next
		if cursor == "" {
			break
		}
	}
	if len(seen) != 7 || cursor != "" {
		t.Fatalf("missing rows or endless cursor: %d %q", len(seen), cursor)
	}
	for _, query := range []string{"limit=0", "limit=abc", "cursor=garbage", "cursor=e30"} {
		rec := httptest.NewRecorder()
		module.listTasks(rec, httptest.NewRequest("GET", "/?"+query, nil))
		if rec.Code != 400 {
			t.Fatalf("accepted %s", query)
		}
	}
}
