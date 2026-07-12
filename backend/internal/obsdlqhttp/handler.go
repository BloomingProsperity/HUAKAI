package obsdlqhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
)

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type Store interface {
	ListDead(context.Context, obsdlq.AdminListFilter) ([]obsdlq.AdminDeadEvent, error)
	ReplayDead(context.Context, string) (obsdlq.AdminReplayResult, error)
}

type Deps struct {
	Auth  AdminAuth
	Store Store
}

func NewListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store, ok := resolvePlatformAdmin(w, r, d)
		if !ok {
			return
		}
		filter, ok := parseListFilter(w, r)
		if !ok {
			return
		}
		rows, err := store.ListDead(r.Context(), filter)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "obs_dlq_query_failed", err.Error())
			return
		}
		items := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			items = append(items, mapDeadEvent(row))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func NewReplayHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store, ok := resolvePlatformAdmin(w, r, d)
		if !ok {
			return
		}
		id := strings.TrimSpace(chi.URLParam(r, "id"))
		if id == "" {
			writeJSONError(w, http.StatusBadRequest, "invalid_obs_dlq_id", "id is required")
			return
		}
		result, err := store.ReplayDead(r.Context(), id)
		if err != nil {
			switch {
			case errors.Is(err, obsdlq.ErrReplayConflict):
				writeJSONError(w, http.StatusConflict, "obs_dlq_replay_conflict", "dead event is not replayable")
			default:
				writeJSONError(w, http.StatusServiceUnavailable, "obs_dlq_replay_failed", err.Error())
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"replayed":        true,
			"id":              result.DLQEventID,
			"outbox_event_id": result.OutboxEventID,
		})
	}
}

func resolvePlatformAdmin(w http.ResponseWriter, r *http.Request, d Deps) (Store, bool) {
	if d.Auth == nil || d.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "obs dlq dependency unset")
		return nil, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return nil, false
	}
	if ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return nil, false
	}
	return d.Store, true
}

func parseListFilter(w http.ResponseWriter, r *http.Request) (obsdlq.AdminListFilter, bool) {
	values := r.URL.Query()
	limit := 100
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
			return obsdlq.AdminListFilter{}, false
		}
		limit = n
	}
	tenantID, ok := parseOptionalInt64(w, values.Get("tenant"), "tenant")
	if !ok {
		return obsdlq.AdminListFilter{}, false
	}
	from, ok := parseOptionalTime(w, values.Get("from"), "from")
	if !ok {
		return obsdlq.AdminListFilter{}, false
	}
	to, ok := parseOptionalTime(w, values.Get("to"), "to")
	if !ok {
		return obsdlq.AdminListFilter{}, false
	}
	return obsdlq.AdminListFilter{
		TenantID:  tenantID,
		EventType: strPtr(strings.TrimSpace(values.Get("event_type"))),
		From:      from,
		To:        to,
		Limit:     limit,
	}, true
}

func parseOptionalInt64(w http.ResponseWriter, raw, name string) (*int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_"+name, name+" must be a positive int64")
		return nil, false
	}
	return &n, true
}

func parseOptionalTime(w http.ResponseWriter, raw, name string) (*time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_"+name, name+" must be RFC3339")
		return nil, false
	}
	ts = ts.UTC()
	return &ts, true
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func mapDeadEvent(row obsdlq.AdminDeadEvent) map[string]any {
	payload := json.RawMessage(row.Payload)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	return map[string]any{
		"id":              row.ID,
		"outbox_event_id": row.OutboxEventID,
		"tenant_id":       row.TenantID,
		"event_type":      row.EventType,
		"priority":        row.Priority,
		"payload":         payload,
		"dead_at":         row.DeadAt.UTC().Format(time.RFC3339Nano),
		"dead_reason":     row.DeadReason,
		"attempt_count":   row.AttemptCount,
		"outbox_status":   row.OutboxStatus,
		"failure_reason":  row.FailureReason,
		"created_at":      row.CreatedAt.UTC().Format(time.RFC3339Nano),
		"next_retry_at":   row.NextRetryAt.UTC().Format(time.RFC3339Nano),
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
