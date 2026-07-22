package dlqhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

type AdminDLQAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminDLQStore interface {
	List(context.Context, dlq.ListFilter) ([]dlq.Record, error)
	Replay(context.Context, int64, string) (*dlq.Record, error)
}

type AdminDLQDeps interface {
	AdminDLQAuth() AdminDLQAuth
	AdminDLQStore() AdminDLQStore
}

func NewAdminDLQListHandler(d AdminDLQDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store, ident, ok := resolvePlatformDLQ(w, r, d)
		if !ok {
			return
		}
		_ = ident
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 200 {
				writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
				return
			}
			limit = n
		}
		filter := dlq.ListFilter{
			EventKind: dlq.EventKind(chi.URLParam(r, "handler")),
			Status:    dlq.Status(r.URL.Query().Get("status")),
			Limit:     limit,
		}
		rows, err := store.List(r.Context(), filter)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "dlq_query_failed", err.Error())
			return
		}
		items := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			items = append(items, mapDLQRecord(row))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func NewAdminDLQReplayHandler(d AdminDLQDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store, ident, ok := resolvePlatformDLQ(w, r, d)
		if !ok {
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_dlq_id", "id must be a positive int64")
			return
		}
		row, err := store.Replay(r.Context(), id, ident.AuditActor())
		if err != nil {
			switch {
			case errors.Is(err, dlq.ErrNotFound):
				writeJSONError(w, http.StatusNotFound, "dlq_not_found", "DLQ record not found")
			case errors.Is(err, dlq.ErrNoHandler):
				writeJSONError(w, http.StatusConflict, "dlq_handler_missing", err.Error())
			default:
				writeJSONError(w, http.StatusServiceUnavailable, "dlq_replay_failed", err.Error())
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"item": mapDLQRecord(*row), "replayed": true})
	}
}

func resolvePlatformDLQ(w http.ResponseWriter, r *http.Request, d AdminDLQDeps) (AdminDLQStore, admin.AdminIdentity, bool) {
	if d == nil || d.AdminDLQAuth() == nil || d.AdminDLQStore() == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin DLQ dependency unset")
		return nil, admin.AdminIdentity{}, false
	}
	ident, err := d.AdminDLQAuth().Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return nil, admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return nil, admin.AdminIdentity{}, false
	}
	return d.AdminDLQStore(), ident, true
}

func mapDLQRecord(row dlq.Record) map[string]any {
	payload := json.RawMessage(row.Payload)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	return map[string]any{
		"id":                    row.ID,
		"tenant_id":             row.TenantID,
		"claim_id":              row.ClaimID,
		"event_kind":            row.EventKind,
		"lane":                  row.Lane,
		"status":                row.Status,
		"payload":               payload,
		"failure_reason":        row.FailureReason,
		"failure_at":            row.FailureAt.UTC().Format(time.RFC3339Nano),
		"replay_attempts":       row.ReplayAttempts,
		"last_replay_at":        formatDLQTS(row.LastReplayAt),
		"replayed_at":           formatDLQTS(row.ReplayedAt),
		"replay_failure_reason": row.ReplayFailureReason,
		"next_retry_at":         row.NextRetryAt.UTC().Format(time.RFC3339Nano),
		"lease_owner":           row.LeaseOwner,
		"lease_until":           formatDLQTS(row.LeaseUntil),
		"replica_status":        row.ReplicaStatus,
		"replica_target":        row.ReplicaTarget,
		"replica_committed_at":  formatDLQTS(row.ReplicaCommittedAt),
		"idempotency_key":       row.IdempotencyKey,
		"source_table":          row.SourceTable,
		"source_id":             row.SourceID,
		"operator_review_at":    formatDLQTS(row.OperatorReviewAt),
	}
}

func formatDLQTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339Nano)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
