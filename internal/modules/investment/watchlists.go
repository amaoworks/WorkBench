package investment

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"workbench/internal/foundation/httpapi"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

type chartWatchlist struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Symbols []string `json:"symbols"`
}

type watchlistsState struct {
	Lists    []chartWatchlist `json:"lists"`
	ActiveID string           `json:"activeId"`
}

type watchlistsView struct {
	Revision int64            `json:"revision"`
	State    *watchlistsState `json:"state"`
}

func (m *Module) getWatchlists(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rec, err := m.queries.GetWatchlists(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "watchlists_read_failed", "无法读取自选表")
		return
	}
	view := watchlistsView{Revision: rec.Revision}
	if err := json.Unmarshal([]byte(rec.Content), &view.State); err != nil {
		httpapi.Error(w, 500, "watchlists_read_failed", "无法读取自选表")
		return
	}
	httpapi.Write(w, http.StatusOK, view)
}

func (m *Module) saveWatchlists(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Revision *int64           `json:"revision"`
		Writer   string           `json:"writer"`
		Sequence int64            `json:"sequence"`
		State    *watchlistsState `json:"state"`
	}
	if httpapi.Decode(w, r, &input, 24<<10) != nil || input.Revision == nil || *input.Revision < 0 || *input.Revision > 1<<53-1 || input.Writer == "" || len(input.Writer) > 128 || input.Sequence < 1 || input.Sequence > 1<<53-1 || !validWatchlists(input.State) {
		httpapi.Error(w, 400, "invalid_watchlists", "自选表格式无效或超过保存限制")
		return
	}
	content, err := json.Marshal(input.State)
	if err != nil {
		httpapi.Error(w, 400, "invalid_watchlists", "自选表格式无效")
		return
	}
	saved, err := m.queries.SaveWatchlists(r.Context(), investmentsqlc.SaveWatchlistsParams{
		Content: string(content), BaseRevision: *input.Revision, Writer: input.Writer, Sequence: input.Sequence,
	})
	if errors.Is(err, sql.ErrNoRows) {
		current, readErr := m.queries.GetWatchlists(r.Context())
		if readErr != nil {
			httpapi.Error(w, 500, "watchlists_save_failed", "无法保存自选表")
			return
		}
		// An unload request may overtake an older request from the same page.
		// A repeated submission is idempotent; a newer sequence already won.
		if current.Writer == input.Writer && current.BaseRevision == *input.Revision &&
			(current.Sequence > input.Sequence || (current.Sequence == input.Sequence && current.Content == string(content))) {
			httpapi.Write(w, http.StatusOK, map[string]int64{"revision": current.Revision, "sequence": current.Sequence})
			return
		}
		httpapi.Error(w, http.StatusConflict, "watchlists_conflict", "自选表已在其他窗口更新，本页改动尚未保存")
		return
	}
	if err != nil {
		httpapi.Error(w, 500, "watchlists_save_failed", "无法保存自选表")
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]int64{"revision": saved.Revision, "sequence": saved.Sequence})
}

func validWatchlists(state *watchlistsState) bool {
	if state == nil || state.Lists == nil || len(state.Lists) > 50 {
		return false
	}
	ids := make(map[string]bool, len(state.Lists))
	total := 0
	for _, list := range state.Lists {
		if strings.TrimSpace(list.ID) == "" || len(list.ID) > 128 || ids[list.ID] || strings.TrimSpace(list.Title) == "" || len(list.Title) > 256 || list.Symbols == nil {
			return false
		}
		ids[list.ID] = true
		total += len(list.Symbols)
		if total > 2000 {
			return false
		}
		for _, symbol := range list.Symbols {
			// Preserve provider symbols and TradingView's ### section dividers.
			if strings.TrimSpace(symbol) == "" || len(symbol) > 256 {
				return false
			}
		}
	}
	return (len(state.Lists) == 0 && state.ActiveID == "") || ids[state.ActiveID]
}
