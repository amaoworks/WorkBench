package investment

import (
	"net/http"
	"strings"

	"workbench/internal/contracts"
)

func (m *Module) SessionPolicy(r *http.Request) contracts.SessionPolicy {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) ||
		!strings.HasPrefix(r.URL.Path, "/charting_library/") || !HashedChartLibraryPath(r.URL.Path) {
		return contracts.SessionPolicy{}
	}
	return contracts.SessionPolicy{ReadOnly: true, WrapResponse: WrapHashedChartResponse}
}
