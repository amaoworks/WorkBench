package investment

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestWatchlistsPreserveStateAndRejectStaleWriters(t *testing.T) {
	m := openInvestmentModule(t)
	read := func() watchlistsView {
		got := httptest.NewRecorder()
		m.getWatchlists(got, httptest.NewRequest("GET", "/", nil))
		var view watchlistsView
		if got.Code != 200 || got.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(got.Body.Bytes(), &view) != nil {
			t.Fatalf("read: %d %s", got.Code, got.Body)
		}
		return view
	}
	if initial := read(); initial.Revision != 0 || initial.State != nil {
		t.Fatalf("new workspace must distinguish no saved state: %+v", initial)
	}
	state := &watchlistsState{Lists: []chartWatchlist{{ID: "one", Title: "长期关注", Symbols: []string{"###科技", "MSFT", "AAPL"}}, {ID: "two", Title: "空表", Symbols: []string{}}}, ActiveID: "two"}
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for i := range 2 {
		wg.Go(func() {
			body, _ := json.Marshal(map[string]any{"revision": 0, "writer": fmt.Sprint(i), "sequence": 1, "state": state})
			got := httptest.NewRecorder()
			m.saveWatchlists(got, httptest.NewRequest("PUT", "/", strings.NewReader(string(body))))
			results <- got.Code
		})
	}
	wg.Wait()
	close(results)
	statuses := map[int]int{}
	for status := range results {
		statuses[status]++
	}
	if statuses[http.StatusOK] != 1 || statuses[http.StatusConflict] != 1 {
		t.Fatalf("concurrent writers: %v", statuses)
	}
	if got := read(); got.Revision != 1 || !reflect.DeepEqual(got.State, state) {
		t.Fatalf("round trip: %+v", got)
	}
	got := httptest.NewRecorder()
	m.saveWatchlists(got, httptest.NewRequest("PUT", "/", strings.NewReader(`{"revision":1,"writer":"new","sequence":1,"state":{"lists":[],"activeId":""}}`)))
	if got.Code != 200 || len(read().State.Lists) != 0 || read().State.Lists == nil {
		t.Fatalf("empty state: %d %s", got.Code, got.Body)
	}
}

func TestWatchlistsRejectInvalidInputWithoutChangingSavedState(t *testing.T) {
	m := openInvestmentModule(t)
	for _, body := range []string{
		`{}`, `{"revision":0,"state":null}`, `{"state":{"lists":[],"activeId":""}}`,
		`{"revision":-1,"state":{"lists":[],"activeId":""}}`,
		`{"revision":0,"state":{"lists":null,"activeId":""}}`,
		`{"revision":0,"state":{"lists":[],"activeId":"missing"}}`,
		`{"revision":0,"state":{"lists":[{"id":"a","title":"A","symbols":null}],"activeId":"a"}}`,
		`{"revision":0,"state":{"lists":[{"id":"a","title":"A","symbols":[""]}],"activeId":"a"}}`,
		`{"revision":0,"state":{"lists":[{"id":"a","title":"A","symbols":[]},{"id":"a","title":"B","symbols":[]}],"activeId":"a"}}`,
		`{"revision":0,"state":{"lists":[],"activeId":"","unexpected":true}}`,
		`{"revision":0,"state":{"lists":[{"id":"a","title":"` + strings.Repeat("x", 49<<10) + `","symbols":[]}],"activeId":"a"}}`,
	} {
		body = strings.Replace(body, "{", `{"writer":"test","sequence":1,`, 1)
		got := httptest.NewRecorder()
		m.saveWatchlists(got, httptest.NewRequest("PUT", "/", strings.NewReader(body)))
		if got.Code != http.StatusBadRequest {
			t.Fatalf("invalid input status: %d", got.Code)
		}
	}
	rec, err := m.queries.GetWatchlists(t.Context())
	if err != nil || rec.Revision != 0 || rec.Content != "null" {
		t.Fatalf("invalid input changed state: %+v %v", rec, err)
	}
}

func TestWatchlistsUnloadOvertakesEarlierSaveAndRetriesAreIdempotent(t *testing.T) {
	m := openInvestmentModule(t)
	write := func(base, sequence int, writer, symbol string, want int) {
		t.Helper()
		body := fmt.Sprintf(`{"revision":%d,"writer":%q,"sequence":%d,"state":{"lists":[{"id":"a","title":"自选","symbols":[%q]}],"activeId":"a"}}`, base, writer, sequence, symbol)
		got := httptest.NewRecorder()
		m.saveWatchlists(got, httptest.NewRequest("PUT", "/", strings.NewReader(body)))
		if got.Code != want {
			t.Fatalf("write: %d %s", got.Code, got.Body)
		}
	}
	write(0, 2, "tab-a", "NVDA", 200)
	write(0, 1, "tab-a", "GOOG", 200)
	write(0, 2, "tab-a", "NVDA", 200)
	write(0, 2, "tab-a", "WRONG", 409)
	stored, _ := m.queries.GetWatchlists(t.Context())
	if stored.Revision != 1 || !strings.Contains(stored.Content, "NVDA") {
		t.Fatalf("late/duplicate save replaced newer content: %+v", stored)
	}
	write(1, 1, "tab-b", "MSFT", 200)
	write(0, 3, "tab-a", "AAPL", 409)
}
