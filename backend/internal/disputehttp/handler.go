// Package disputehttp exposes F-AUDIT-001 cost dispute HTTP endpoints.
package disputehttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/audit"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
)

const maxBodyBytes = 64 << 10

type ReceiptReader interface {
	GetReceiptForUser(ctx context.Context, requestID string, tenantID, userID int64) (*audit.CostReceipt, error)
}

type Store interface {
	CreateDispute(context.Context, audit.CreateCostDisputeInput) (audit.CostDispute, error)
	ListUserDisputes(context.Context, int64, int64, int32) ([]audit.CostDispute, error)
	ResolveDispute(context.Context, audit.ResolveCostDisputeInput) (audit.CostDispute, error)
}

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type UserDeps struct {
	Receipts ReceiptReader
	Store    Store
}

type AdminDeps struct {
	Auth  AdminAuth
	Store Store
}

type createRequest struct {
	Reason string `json:"reason"`
}

type resolveRequest struct {
	TenantID     int64  `json:"tenant_id"`
	Status       string `json:"status"`
	OperatorNote string `json:"operator_note"`
}

type disputeView struct {
	ID           int64   `json:"id"`
	DisputeID    string  `json:"dispute_id"`
	TenantID     int64   `json:"tenant_id"`
	UserID       int64   `json:"user_id"`
	RequestID    string  `json:"request_id"`
	Reason       string  `json:"reason"`
	Status       string  `json:"status"`
	OperatorNote string  `json:"operator_note,omitempty"`
	CreatedAt    string  `json:"created_at"`
	ResolvedAt   *string `json:"resolved_at,omitempty"`
}

func NewCreateDisputeHandler(d UserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d.Store)
		if !ok {
			return
		}
		if d.Receipts == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "receipt dependency unset")
			return
		}
		requestID := requestIDFromPath(r)
		if strings.TrimSpace(requestID) == "" {
			writeJSONError(w, http.StatusBadRequest, "invalid_request_id", "request_id is required")
			return
		}
		var req createRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if _, err := d.Receipts.GetReceiptForUser(r.Context(), requestID, ident.TenantID, ident.UserID); err != nil {
			if errors.Is(err, audit.ErrReceiptNotFound) {
				writeJSONError(w, http.StatusNotFound, "receipt_not_found", "receipt not found")
				return
			}
			writeJSONError(w, http.StatusServiceUnavailable, "receipt_lookup_failed", "receipt lookup unavailable")
			return
		}
		dispute, err := d.Store.CreateDispute(r.Context(), audit.CreateCostDisputeInput{
			TenantID:  ident.TenantID,
			UserID:    ident.UserID,
			RequestID: requestID,
			Reason:    req.Reason,
		})
		if err != nil {
			writeDisputeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"dispute": toView(dispute)})
	}
}

func NewListUserDisputesHandler(d UserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d.Store)
		if !ok {
			return
		}
		limit, ok := parseLimit(w, r)
		if !ok {
			return
		}
		rows, err := d.Store.ListUserDisputes(r.Context(), ident.TenantID, ident.UserID, limit)
		if err != nil {
			writeDisputeError(w, err)
			return
		}
		out := make([]disputeView, 0, len(rows))
		for _, row := range rows {
			out = append(out, toView(row))
		}
		writeJSON(w, http.StatusOK, map[string]any{"disputes": out})
	}
}

func NewAdminResolveDisputeHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Store == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "dispute admin dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminError(w, err)
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req resolveRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if err := ident.CanIssueForTenant(req.TenantID); err != nil {
			writeAdminError(w, err)
			return
		}
		dispute, err := d.Store.ResolveDispute(r.Context(), audit.ResolveCostDisputeInput{
			TenantID:     req.TenantID,
			ID:           id,
			Status:       strings.TrimSpace(req.Status),
			OperatorNote: req.OperatorNote,
		})
		if err != nil {
			writeDisputeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"dispute": toView(dispute)})
	}
}

func requestIDFromPath(r *http.Request) string {
	if v := strings.TrimSpace(chi.URLParam(r, "request_id")); v != "" {
		return v
	}
	host := strings.TrimSpace(chi.URLParam(r, "request_id_host"))
	tail := strings.TrimSpace(chi.URLParam(r, "request_id_tail"))
	if host == "" || tail == "" {
		return ""
	}
	return host + "/" + tail
}

func resolveSession(w http.ResponseWriter, r *http.Request, store Store) (sessionauth.SessionIdentity, bool) {
	if store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "dispute dependency unset")
		return sessionauth.SessionIdentity{}, false
	}
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_dispute_request", "request body is not valid JSON")
		return false
	}
	return true
}

func parseLimit(w http.ResponseWriter, r *http.Request) (int32, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 100, true
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n <= 0 || n > 500 {
		writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 500")
		return 0, false
	}
	return int32(n), true
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func toView(row audit.CostDispute) disputeView {
	createdAt := formatTime(row.CreatedAt)
	var resolvedAt *string
	if row.ResolvedAt != nil {
		s := formatTime(*row.ResolvedAt)
		resolvedAt = &s
	}
	return disputeView{
		ID:           row.ID,
		DisputeID:    row.DisputeID,
		TenantID:     row.TenantID,
		UserID:       row.UserID,
		RequestID:    row.RequestID,
		Reason:       row.Reason,
		Status:       row.Status,
		OperatorNote: row.OperatorNote,
		CreatedAt:    createdAt,
		ResolvedAt:   resolvedAt,
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func writeDisputeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, audit.ErrDisputeInvalid):
		writeJSONError(w, http.StatusBadRequest, "invalid_dispute_request", "dispute request is invalid")
	case errors.Is(err, audit.ErrDisputeDuplicate):
		writeJSONError(w, http.StatusConflict, "dispute_duplicate", "dispute already exists for this receipt")
	case errors.Is(err, audit.ErrDisputeNotFound):
		writeJSONError(w, http.StatusNotFound, "dispute_not_found", "dispute not found")
	case errors.Is(err, audit.ErrDisputeStoreRequired):
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "dispute dependency unset")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "dispute_backend_error", "dispute backend unavailable")
	}
}

func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminForbidden):
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "admin role cannot access tenant")
	case errors.Is(err, admin.ErrAdminBackend):
		writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
	default:
		writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
