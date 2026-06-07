// Package userauditloghttp exposes session-scoped user audit events.
package userauditloghttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userauditlog"
)

type AuditEventStore interface {
	List(ctx context.Context, req userauditlog.ListRequest) ([]userauditlog.EventRecord, error)
}

type Deps struct {
	Store AuditEventStore
}

func MountRoutes(r chi.Router, d Deps) {
	r.Get("/audit-events", newListHandler(d))
}

type auditEventsResponse struct {
	AuditEvents []auditEventView `json:"audit_events"`
	Count       int              `json:"count"`
}

type auditEventView struct {
	ID         int64  `json:"id"`
	Action     string `json:"action"`
	Outcome    string `json:"outcome"`
	APIKeyID   *int64 `json:"api_key_id,omitempty"`
	KeyPrefix  string `json:"key_prefix,omitempty"`
	Reason     string `json:"reason,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	OccurredAt string `json:"occurred_at"`
}

func newListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		offset, limit, ok := parsePagination(w, r)
		if !ok {
			return
		}
		rows, err := d.Store.List(r.Context(), userauditlog.ListRequest{
			TenantID: ident.TenantID,
			UserID:   ident.UserID,
			Limit:    limit,
			Offset:   offset,
		})
		if err != nil {
			writeAuditLogError(w, err)
			return
		}
		out := auditEventsResponse{
			AuditEvents: make([]auditEventView, 0, len(rows)),
			Count:       len(rows),
		}
		for _, row := range rows {
			out.AuditEvents = append(out.AuditEvents, eventToView(row))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func resolveSession(w http.ResponseWriter, r *http.Request, d Deps) (sessionauth.SessionIdentity, bool) {
	if d.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "user_audit_log_unavailable", "user audit log dependency unset")
		return sessionauth.SessionIdentity{}, false
	}
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		writeJSONError(w, http.StatusUnauthorized, "session_required", "session bearer token is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func parsePagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	q := r.URL.Query()
	offset := 0
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_offset", "offset must be a non-negative integer")
			return 0, 0, false
		}
		offset = n
	}
	limit := userauditlog.PageLimitDefault
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > userauditlog.PageLimitMax {
			writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be in (0, "+strconv.Itoa(userauditlog.PageLimitMax)+"]")
			return 0, 0, false
		}
		limit = n
	}
	return offset, limit, true
}

func eventToView(row userauditlog.EventRecord) auditEventView {
	return auditEventView{
		ID:         row.ID,
		Action:     row.Action,
		Outcome:    row.Outcome,
		APIKeyID:   row.APIKeyID,
		KeyPrefix:  row.KeyPrefix,
		Reason:     row.Reason,
		RequestID:  row.RequestID,
		OccurredAt: row.OccurredAt.UTC().Format(time.RFC3339Nano),
	}
}

func writeAuditLogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userauditlog.ErrInvalidRequest):
		writeJSONError(w, http.StatusBadRequest, "invalid_audit_log_request", "audit event query is invalid")
	case errors.Is(err, userauditlog.ErrMisconfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "user_audit_log_unavailable", "user audit log service unavailable")
	case errors.Is(err, userauditlog.ErrBackend):
		writeJSONError(w, http.StatusServiceUnavailable, "user_audit_log_backend_error", "user audit log backend transient failure")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "user_audit_log_error", "user audit log request failed")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
