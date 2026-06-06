// Package controlhttp exposes F-AUDIT-001 cost dispute HTTP endpoints.
package controlhttp

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

const (
	disputeMaxBodyBytes      = 64 << 10
	disputeDefaultListLimit  = int32(100)
	disputeMaxListLimit      = int32(500)
	disputeDefaultListOffset = int32(0)
)

type DisputeReceiptReader interface {
	GetReceiptForUser(ctx context.Context, requestID string, tenantID, userID int64) (*audit.CostReceipt, error)
}

type DisputeStore interface {
	CreateDispute(context.Context, audit.CreateCostDisputeInput) (audit.CostDispute, error)
	ListForAdmin(context.Context, int64, string, int32, int32) ([]audit.CostDispute, error)
	ListUserDisputes(context.Context, int64, int64, int32) ([]audit.CostDispute, error)
	ResolveDispute(context.Context, audit.ResolveCostDisputeInput) (audit.CostDispute, error)
}

type DisputeAdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type DisputeUserDeps struct {
	Receipts DisputeReceiptReader
	Store    DisputeStore
}

type DisputeAdminDeps struct {
	Auth  DisputeAdminAuth
	Store DisputeStore
}

type disputeCreateRequest struct {
	Reason string `json:"reason"`
}

type disputeResolveRequest struct {
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

func NewCreateDisputeHandler(d DisputeUserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := disputeResolveSession(w, r, d.Store)
		if !ok {
			return
		}
		if d.Receipts == nil {
			controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "receipt dependency unset")
			return
		}
		requestID := requestIDFromPath(r)
		if strings.TrimSpace(requestID) == "" {
			controlWriteJSONError(w, http.StatusBadRequest, "invalid_request_id", "request_id is required")
			return
		}
		var req disputeCreateRequest
		if !disputeDecodeJSON(w, r, &req) {
			return
		}
		if _, err := d.Receipts.GetReceiptForUser(r.Context(), requestID, ident.TenantID, ident.UserID); err != nil {
			if errors.Is(err, audit.ErrReceiptNotFound) {
				controlWriteJSONError(w, http.StatusNotFound, "receipt_not_found", "receipt not found")
				return
			}
			controlWriteJSONError(w, http.StatusServiceUnavailable, "receipt_lookup_failed", "receipt lookup unavailable")
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
		controlWriteJSON(w, http.StatusCreated, map[string]any{"dispute": disputeToView(dispute)})
	}
}

func NewListUserDisputesHandler(d DisputeUserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := disputeResolveSession(w, r, d.Store)
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
			out = append(out, disputeToView(row))
		}
		controlWriteJSON(w, http.StatusOK, map[string]any{"disputes": out})
	}
}

func NewAdminListDisputesHandler(d DisputeAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Store == nil {
			controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "dispute admin dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			disputeWriteAdminError(w, err)
			return
		}
		tenantID, ok := disputeAdminTenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		limit, offset, ok := parseAdminDisputePagination(w, r)
		if !ok {
			return
		}
		statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
		rows, err := d.Store.ListForAdmin(r.Context(), tenantID, statusFilter, limit, offset)
		if err != nil {
			writeDisputeError(w, err)
			return
		}
		out := make([]disputeView, 0, len(rows))
		for _, row := range rows {
			out = append(out, disputeToView(row))
		}
		controlWriteJSON(w, http.StatusOK, map[string]any{"disputes": out})
	}
}

func NewAdminResolveDisputeHandler(d DisputeAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Store == nil {
			controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "dispute admin dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			disputeWriteAdminError(w, err)
			return
		}
		id, ok := disputeParsePathID(w, r)
		if !ok {
			return
		}
		var req disputeResolveRequest
		if !disputeDecodeJSON(w, r, &req) {
			return
		}
		if err := ident.CanIssueForTenant(req.TenantID); err != nil {
			disputeWriteAdminError(w, err)
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
		controlWriteJSON(w, http.StatusOK, map[string]any{"dispute": disputeToView(dispute)})
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

func disputeResolveSession(w http.ResponseWriter, r *http.Request, store DisputeStore) (sessionauth.SessionIdentity, bool) {
	if store == nil {
		controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "dispute dependency unset")
		return sessionauth.SessionIdentity{}, false
	}
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok {
		controlWriteJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func disputeDecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, disputeMaxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		controlWriteJSONError(w, http.StatusBadRequest, "invalid_dispute_request", "request body is not valid JSON")
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
		controlWriteJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 500")
		return 0, false
	}
	return int32(n), true
}

func disputeAdminTenantFromQuery(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			controlWriteJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope required")
			return 0, false
		}
		if raw == "" {
			return ident.ScopeTenantID, true
		}
		tenantID, ok := parsePositiveInt64QueryValue(w, raw, "tenant_id")
		if !ok {
			return 0, false
		}
		if tenantID != ident.ScopeTenantID {
			controlWriteJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
			return 0, false
		}
		return tenantID, true
	case admin.RolePlatformAdmin:
		if raw == "" && ident.ScopeTenantID > 0 {
			return ident.ScopeTenantID, true
		}
		if raw == "" {
			controlWriteJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query parameter must be positive")
			return 0, false
		}
		tenantID, ok := parsePositiveInt64QueryValue(w, raw, "tenant_id")
		if !ok {
			return 0, false
		}
		if err := ident.CanIssueForTenant(tenantID); err != nil {
			disputeWriteAdminError(w, err)
			return 0, false
		}
		return tenantID, true
	default:
		controlWriteJSONError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return 0, false
	}
}

func parsePositiveInt64QueryValue(w http.ResponseWriter, raw, name string) (int64, bool) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		controlWriteJSONError(w, http.StatusBadRequest, "invalid_"+name, name+" query parameter must be positive")
		return 0, false
	}
	return n, true
}

func parseAdminDisputePagination(w http.ResponseWriter, r *http.Request) (int32, int32, bool) {
	limit := disputeDefaultListLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n <= 0 {
			controlWriteJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be positive")
			return 0, 0, false
		}
		limit = int32(n)
		if limit > disputeMaxListLimit {
			limit = disputeMaxListLimit
		}
	}
	offset := disputeDefaultListOffset
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < 0 {
			controlWriteJSONError(w, http.StatusBadRequest, "invalid_offset", "offset must be non-negative")
			return 0, 0, false
		}
		offset = int32(n)
	}
	return limit, offset, true
}

func disputeParsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		controlWriteJSONError(w, http.StatusBadRequest, "invalid_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func disputeToView(row audit.CostDispute) disputeView {
	createdAt := disputeFormatTime(row.CreatedAt)
	var resolvedAt *string
	if row.ResolvedAt != nil {
		s := disputeFormatTime(*row.ResolvedAt)
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

func disputeFormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func writeDisputeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, audit.ErrDisputeInvalid):
		controlWriteJSONError(w, http.StatusBadRequest, "invalid_dispute_request", "dispute request is invalid")
	case errors.Is(err, audit.ErrDisputeDuplicate):
		controlWriteJSONError(w, http.StatusConflict, "dispute_duplicate", "dispute already exists for this receipt")
	case errors.Is(err, audit.ErrDisputeNotFound):
		controlWriteJSONError(w, http.StatusNotFound, "dispute_not_found", "dispute not found")
	case errors.Is(err, audit.ErrDisputeStoreRequired):
		controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "dispute dependency unset")
	default:
		controlWriteJSONError(w, http.StatusServiceUnavailable, "dispute_backend_error", "dispute backend unavailable")
	}
}

func disputeWriteAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminForbidden):
		controlWriteJSONError(w, http.StatusForbidden, "admin_forbidden", "admin role cannot access tenant")
	case errors.Is(err, admin.ErrAdminBackend):
		controlWriteJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
	default:
		controlWriteJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
	}
}
