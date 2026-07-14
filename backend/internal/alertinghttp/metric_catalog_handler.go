package alertinghttp

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/alertmetrics"
)

func newMetricCatalogHandler(deps AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(deps, w, r); !ok {
			return
		}
		writeJSON(w, http.StatusOK, alertmetrics.CatalogEntries())
	}
}
