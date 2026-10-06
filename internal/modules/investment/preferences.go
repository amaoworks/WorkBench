package investment

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"workbench/internal/foundation/httpapi"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

const (
	maxPreferenceRequest = 8 << 20
	maxPreferenceInteger = 1<<53 - 1
)

type preferenceView struct {
	Revision int64           `json:"revision"`
	Value    json.RawMessage `json:"value"`
}

type preferencesView struct {
	Entries map[string]preferenceView `json:"entries"`
}

type preferenceInput struct {
	Name     string          `json:"name"`
	Revision *int64          `json:"revision"`
	Writer   string          `json:"writer"`
	Sequence int64           `json:"sequence"`
	Value    json.RawMessage `json:"value"`
}

func (m *Module) getPreferences(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rows, err := m.queries.ListPreferences(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "preferences_read_failed", "无法读取投资偏好")
		return
	}
	entries := make(map[string]preferenceView, len(rows))
	for _, row := range rows {
		value := json.RawMessage(row.Content)
		if !json.Valid(value) {
			httpapi.Error(w, http.StatusInternalServerError, "preferences_read_failed", "无法读取投资偏好")
			return
		}
		entries[row.Name] = preferenceView{Revision: row.Revision, Value: value}
	}
	httpapi.Write(w, http.StatusOK, preferencesView{Entries: entries})
}

func (m *Module) savePreference(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input preferenceInput
	if httpapi.Decode(w, r, &input, maxPreferenceRequest) != nil || !validPreferenceEnvelope(input) {
		httpapi.Error(w, http.StatusBadRequest, "invalid_preference", "投资偏好格式无效或超过保存限制")
		return
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, input.Value); err != nil || !validPreferenceValue(input.Name, compact.Bytes()) {
		httpapi.Error(w, http.StatusBadRequest, "invalid_preference", "投资偏好格式无效或超过保存限制")
		return
	}
	content := compact.String()
	saved, err := m.queries.SavePreference(r.Context(), investmentsqlc.SavePreferenceParams{
		Name: input.Name, Content: content, Writer: input.Writer, BaseRevision: *input.Revision, Sequence: input.Sequence,
	})
	if errors.Is(err, sql.ErrNoRows) {
		current, readErr := m.queries.GetPreference(r.Context(), input.Name)
		if readErr == nil && current.Writer == input.Writer && current.BaseRevision == *input.Revision &&
			(current.Sequence > input.Sequence || (current.Sequence == input.Sequence && current.Content == content)) {
			httpapi.Write(w, http.StatusOK, map[string]int64{"revision": current.Revision, "sequence": current.Sequence})
			return
		}
		if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
			httpapi.Error(w, http.StatusInternalServerError, "preferences_save_failed", "无法保存投资偏好")
			return
		}
		httpapi.Error(w, http.StatusConflict, "preferences_conflict", "投资偏好已在其他窗口更新，本页改动尚未保存")
		return
	}
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "preferences_save_failed", "无法保存投资偏好")
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]int64{"revision": saved.Revision, "sequence": saved.Sequence})
}

func validPreferenceEnvelope(input preferenceInput) bool {
	return validPreferenceName(input.Name) && input.Revision != nil && *input.Revision >= 0 && *input.Revision <= maxPreferenceInteger &&
		input.Writer != "" && len(input.Writer) <= 128 && input.Sequence >= 1 && input.Sequence <= maxPreferenceInteger && input.Value != nil
}

func validPreferenceName(name string) bool {
	if name == "chart" || name == "layout" || name == "account" || name == "view" {
		return true
	}
	if !strings.HasPrefix(name, "tv:") {
		return false
	}
	key := strings.TrimPrefix(name, "tv:")
	if key == "" || !utf8.ValidString(key) || utf8.RuneCountInString(key) > 500 {
		return false
	}
	return strings.IndexFunc(key, unicode.IsControl) < 0
}

func validPreferenceValue(name string, value json.RawMessage) bool {
	if bytes.Equal(value, []byte("null")) {
		return true
	}
	switch name {
	case "chart":
		var state struct {
			Symbol     string `json:"symbol"`
			Resolution string `json:"resolution"`
			Session    string `json:"session"`
		}
		if decodePreferenceValue(value, &state, true) != nil || strings.TrimSpace(state.Symbol) == "" || utf8.RuneCountInString(state.Symbol) > 200 {
			return false
		}
		minute := state.Resolution == "1" || state.Resolution == "5" || state.Resolution == "15" || state.Resolution == "30"
		resolution := minute || state.Resolution == "1D" || state.Resolution == "1W" || state.Resolution == "1M"
		session := state.Session == "regular" || state.Session == "extended" || state.Session == "night" || state.Session == "equity24h"
		return resolution && session && (state.Session == "regular" || minute)
	case "account":
		var account string
		return json.Unmarshal(value, &account) == nil && strings.TrimSpace(account) != "" && utf8.RuneCountInString(account) <= 200
	case "view":
		var state struct {
			Tab      string `json:"tab"`
			Expanded *bool  `json:"expanded"`
		}
		return decodePreferenceValue(value, &state, true) == nil && (state.Tab == "terminal" || state.Tab == "monitor") && state.Expanded != nil
	case "layout":
		var object map[string]json.RawMessage
		if json.Unmarshal(value, &object) != nil || object == nil {
			return false
		}
		var charts []json.RawMessage
		return json.Unmarshal(object["charts"], &charts) == nil && len(charts) > 0
	default:
		var text string
		return json.Unmarshal(value, &text) == nil && len(text) <= 256<<10
	}
}

func decodePreferenceValue(value json.RawMessage, destination any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}
