package investment

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func putPreference(t *testing.T, m *Module, name string, revision, sequence int64, writer, value string, want int) map[string]int64 {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"revision":%d,"writer":%q,"sequence":%d,"value":%s}`, name, revision, writer, sequence, value)
	response := httptest.NewRecorder()
	m.savePreference(response, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))
	if response.Code != want || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("save %s: %d %s", name, response.Code, response.Body.String())
	}
	result := map[string]int64{}
	if want == http.StatusOK && json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("decode save %s: %s", name, response.Body.String())
	}
	return result
}

func readPreferences(t *testing.T, m *Module) preferencesView {
	t.Helper()
	response := httptest.NewRecorder()
	m.getPreferences(response, httptest.NewRequest(http.MethodGet, "/", nil))
	var view preferencesView
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(response.Body.Bytes(), &view) != nil {
		t.Fatalf("read: %d %s", response.Code, response.Body.String())
	}
	return view
}

func TestPreferencesRoundTripAcrossModuleInstances(t *testing.T) {
	first := openInvestmentModule(t)
	second, err := New(testInvestmentDependencies(t, first.deps.DB))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	if got := readPreferences(t, second); len(got.Entries) != 0 {
		t.Fatalf("new preferences: %+v", got)
	}
	values := map[string]string{
		"chart":        `{"symbol":"NASDAQ:AAPL","resolution":"1D","session":"regular"}`,
		"layout":       `{"charts":[{"symbol":"AAPL"}],"tvInternal":{"opaque":true}}`,
		"account":      `"account-hash"`,
		"view":         `{"tab":"terminal","expanded":true}`,
		"tv:favorites": `"indicators"`,
	}
	for name, value := range values {
		putPreference(t, first, name, 0, 1, "tab-a", value, http.StatusOK)
	}
	view := readPreferences(t, second)
	if len(view.Entries) != len(values) {
		t.Fatalf("entries: %+v", view)
	}
	for name, value := range values {
		entry, ok := view.Entries[name]
		if !ok || entry.Revision != 1 || !reflect.DeepEqual(compactJSON(t, entry.Value), compactJSON(t, json.RawMessage(value))) {
			t.Fatalf("round trip %s: %+v", name, entry)
		}
	}
}

func TestPreferencesCASLateReplayAndTombstone(t *testing.T) {
	m := openInvestmentModule(t)
	newer := `{"symbol":"AAPL","resolution":"1D","session":"regular"}`
	older := `{"symbol":"MSFT","resolution":"1D","session":"regular"}`
	putPreference(t, m, "chart", 1, 1, "stale-before-create", newer, http.StatusConflict)
	if got := putPreference(t, m, "chart", 0, 2, "tab-a", newer, http.StatusOK); got["revision"] != 1 || got["sequence"] != 2 {
		t.Fatalf("initial save: %v", got)
	}
	putPreference(t, m, "chart", 0, 1, "tab-a", older, http.StatusOK)
	putPreference(t, m, "chart", 0, 2, "tab-a", newer, http.StatusOK)
	putPreference(t, m, "chart", 0, 2, "tab-a", older, http.StatusConflict)
	putPreference(t, m, "chart", 0, 3, "tab-b", older, http.StatusConflict)
	if got := putPreference(t, m, "chart", 1, 1, "tab-b", older, http.StatusOK); got["revision"] != 2 {
		t.Fatalf("nonzero revision update: %v", got)
	}
	putPreference(t, m, "chart", 1, 2, "tab-b", "null", http.StatusOK)
	putPreference(t, m, "chart", 0, 99, "tab-a", newer, http.StatusConflict)
	putPreference(t, m, "chart", 0, 1, "tab-c", newer, http.StatusConflict)
	entry := readPreferences(t, m).Entries["chart"]
	if entry.Revision != 3 || string(entry.Value) != "null" {
		t.Fatalf("tombstone: %+v", entry)
	}
	putPreference(t, m, "chart", 3, 1, "tab-c", newer, http.StatusOK)
}

func TestPreferencesRejectInvalidInput(t *testing.T) {
	m := openInvestmentModule(t)
	tooLargeTV := `"` + strings.Repeat("x", (256<<10)+1) + `"`
	cases := []string{
		`{}`,
		`{"name":"chart","revision":0,"writer":"w","sequence":1}`,
		`{"name":"unknown","revision":0,"writer":"w","sequence":1,"value":null}`,
		`{"name":"tv:","revision":0,"writer":"w","sequence":1,"value":"x"}`,
		`{"name":"tv:bad\nkey","revision":0,"writer":"w","sequence":1,"value":"x"}`,
		fmt.Sprintf(`{"name":%q,"revision":0,"writer":"w","sequence":1,"value":"x"}`, "tv:"+strings.Repeat("x", 501)),
		`{"name":"chart","revision":-1,"writer":"w","sequence":1,"value":null}`,
		`{"name":"chart","revision":9007199254740992,"writer":"w","sequence":1,"value":null}`,
		`{"name":"chart","revision":0,"writer":"","sequence":1,"value":null}`,
		fmt.Sprintf(`{"name":"chart","revision":0,"writer":%q,"sequence":1,"value":null}`, strings.Repeat("w", 129)),
		`{"name":"chart","revision":0,"writer":"w","sequence":0,"value":null}`,
		`{"name":"chart","revision":0,"writer":"w","sequence":9007199254740992,"value":null}`,
		`{"name":"chart","revision":0,"writer":"w","sequence":1,"value":{"symbol":"AAPL","resolution":"1D","session":"night"}}`,
		`{"name":"chart","revision":0,"writer":"w","sequence":1,"value":{"symbol":"","resolution":"1","session":"regular"}}`,
		fmt.Sprintf(`{"name":"chart","revision":0,"writer":"w","sequence":1,"value":{"symbol":%q,"resolution":"1","session":"regular"}}`, strings.Repeat("x", 201)),
		`{"name":"chart","revision":0,"writer":"w","sequence":1,"value":{"symbol":"AAPL","resolution":"2","session":"regular"}}`,
		`{"name":"chart","revision":0,"writer":"w","sequence":1,"value":{"symbol":"AAPL","resolution":"1","session":"regular","extra":true}}`,
		`{"name":"account","revision":0,"writer":"w","sequence":1,"value":""}`,
		fmt.Sprintf(`{"name":"account","revision":0,"writer":"w","sequence":1,"value":%q}`, strings.Repeat("x", 201)),
		`{"name":"view","revision":0,"writer":"w","sequence":1,"value":{"tab":"terminal"}}`,
		`{"name":"view","revision":0,"writer":"w","sequence":1,"value":{"tab":"other","expanded":false}}`,
		`{"name":"layout","revision":0,"writer":"w","sequence":1,"value":{"charts":[]}}`,
		`{"name":"layout","revision":0,"writer":"w","sequence":1,"value":[]}`,
		fmt.Sprintf(`{"name":"tv:large","revision":0,"writer":"w","sequence":1,"value":%s}`, tooLargeTV),
		`{"name":"tv:key","revision":0,"writer":"w","sequence":1,"value":{"not":"a string"}}`,
		`{"name":"view","revision":0,"writer":"w","sequence":1,"value":null,"unexpected":true}`,
	}
	for i, body := range cases {
		response := httptest.NewRecorder()
		m.savePreference(response, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("case %d: %d %s", i, response.Code, response.Body.String())
		}
	}
	oversized := fmt.Sprintf(`{"name":"layout","revision":0,"writer":"w","sequence":1,"value":{"charts":[{}],"opaque":%q}}`, strings.Repeat("x", maxPreferenceRequest))
	response := httptest.NewRecorder()
	m.savePreference(response, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(oversized)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized request: %d %s", response.Code, response.Body.String())
	}
	if got := readPreferences(t, m); len(got.Entries) != 0 {
		t.Fatalf("invalid writes changed state: %+v", got)
	}
}

func compactJSON(t *testing.T, value json.RawMessage) any {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
