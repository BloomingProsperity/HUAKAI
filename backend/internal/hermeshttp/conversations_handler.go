package hermeshttp

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

func (h handler) listConversations(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}
	conversations, err := h.svc.ListConversationsByOwner(r.Context(), ident.TenantID, ident.UserID, limit, offset)
	if err != nil {
		writeHermesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversations": conversations,
		"limit":         limit,
		"offset":        offset,
	})
}

func (h handler) listConversationMessages(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	id, ok := conversationIDParam(w, r)
	if !ok {
		return
	}
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}
	messages, err := h.svc.ListMessagesByConversation(r.Context(), ident.TenantID, id, ident.UserID, limit, offset)
	if err != nil {
		writeHermesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"messages": messages,
		"limit":    limit,
		"offset":   offset,
	})
}

func (h handler) getConversation(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	id, ok := conversationIDParam(w, r)
	if !ok {
		return
	}
	conversation, err := h.svc.GetConversation(r.Context(), ident.TenantID, id, ident.UserID)
	if err != nil {
		writeHermesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conversation)
}

func (h handler) deleteConversation(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	id, ok := conversationIDParam(w, r)
	if !ok {
		return
	}
	args := map[string]any{
		"conversation_id": id,
	}
	err := h.svc.SoftDeleteConversationWithAudit(
		r.Context(), ident.TenantID, ident.UserID, id,
		auditFields(r, ident, hermes.ActionConversationDelete, args, hermes.AuditResultSuccess),
	)
	if err != nil {
		h.auditFailureThenError(w, r, ident, hermes.ActionConversationDelete, args, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func conversationIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, "id"))
	if raw == "" {
		writeError(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id is required")
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id must be a positive integer")
		return 0, false
	}
	return id, true
}

func parsePagination(w http.ResponseWriter, r *http.Request) (int32, int32, bool) {
	limit, ok := parseInt32Query(w, r, "limit", 50)
	if !ok {
		return 0, 0, false
	}
	if limit > 200 {
		limit = 200
	}
	if limit <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_pagination", "limit must be positive")
		return 0, 0, false
	}
	offset, ok := parseInt32Query(w, r, "offset", 0)
	if !ok {
		return 0, 0, false
	}
	if offset < 0 {
		writeError(w, http.StatusBadRequest, "invalid_pagination", "offset must be non-negative")
		return 0, 0, false
	}
	return limit, offset, true
}

func parseInt32Query(w http.ResponseWriter, r *http.Request, name string, defaultValue int32) (int32, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return defaultValue, true
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_pagination", name+" must be an integer")
		return 0, false
	}
	return int32(value), true
}
