package hermeshttp

import (
	"log"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/modulehttp"
)

// getModuleContext serves GET /v1/hermes/context. It returns the merged
// module-knowledge view (module identity + capabilities + live probe state +
// static catalog overlay) across all categories — the H2 module spine's first
// consumer. Admin-gated by the H1 middleware mount; here it just requires a
// resolved identity. Privacy: module identity + enum statuses + short detail
// strings only, never secrets or user data.
func (h handler) getModuleContext(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireIdentity(w, r); !ok {
		return
	}
	if h.contextSource == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_context_unavailable", "hermes module context source unset")
		return
	}
	views := modulehttp.ContextSummary(r.Context(), h.contextSource)
	writeJSON(w, http.StatusOK, map[string]any{"modules": views})
}

func logToolCallWriteFailure(toolName string, err error) {
	log.Printf("hermes tool-call audit insert failed (tool=%s): %v", toolName, err)
}

func logToolAuditMirrorFailure(toolName string, err error) {
	log.Printf("hermes tool audit mirror failed (tool=%s): %v", toolName, err)
}
