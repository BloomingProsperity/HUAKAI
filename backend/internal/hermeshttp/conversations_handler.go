package hermeshttp

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (h handler) listConversations(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	if !h.requireRunner(w) {
		return
	}
	resp, err := h.runner.Conversations(r.Context(), ident.TenantID, ident.UserID, r.URL.RawQuery)
	if err != nil {
		writeHermesError(w, err)
		return
	}
	copyProxyResponse(w, resp)
}

func (h handler) listConversationMessages(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	if !h.requireRunner(w) {
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id is required")
		return
	}
	resp, err := h.runner.ConversationMessages(r.Context(), ident.TenantID, ident.UserID, id, r.URL.RawQuery)
	if err != nil {
		writeHermesError(w, err)
		return
	}
	copyProxyResponse(w, resp)
}
