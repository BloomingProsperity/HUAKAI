package modulehttp

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// ModulesResponse is the JSON body of GET /admin/v1/modules.
type ModulesResponse struct {
	Modules []ModuleView `json:"modules"`
}

// NewModulesHandler returns the read-only modules handler. It is intentionally
// behind adminGate (the caller's responsibility, mirroring the system-health
// route) — the handler itself performs NO auth, so its tests stay focused on the
// merge/filter behavior while the gating is asserted at the route layer.
//
// Query param:
//
//	?category=<cat>  filter to one category (e.g. money-path); empty = all.
func NewModulesHandler(src Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if src == nil {
			writeJSON(w, http.StatusServiceUnavailable, ModulesResponse{Modules: []ModuleView{}})
			return
		}
		category := r.URL.Query().Get("category")
		views := Merge(r.Context(), src, category)
		if views == nil {
			views = []ModuleView{}
		}
		writeJSON(w, http.StatusOK, ModulesResponse{Modules: views})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("modulehttp json encode failed", slog.String("error", err.Error()))
	}
}
