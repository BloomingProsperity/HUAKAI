// Package adminhttp wires the operator-facing admin endpoints under
// /admin/v1/. This slice ships the api_keys issuance + list +
// revoke surface; later slices add /admin/v1/users, /admin/v1/pools, etc.
//
// Per CLAUDE.md + docs/specs/_invariants/cross-module-boundaries.md:
// this package never imports internal/router or internal/auth.
//     The customer hot path is unaffected.
// plaintext bearer is surfaced ONLY in the POST response body
//     for the operator. Never logged, never echoed in error responses.
// writes go through internal/admin (admin_tokens / api_keys /
//     admin_audit_events). No billing/pool/registry mutation.

package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// AdminAPIKeysDeps is the subset of the deps tree the api_keys handlers
// need. Concrete deps in cmd/gateway/main.go satisfy this implicitly via
// duck typing.
type AdminAPIKeysDeps struct {
	Auth    adminAPIKeysAuth
	Issuer  adminAPIKeysIssuer
	Revoker adminAPIKeysRevoker
	Queries adminAPIKeysQueries // for LIST (read-only)
}

type adminAPIKeysAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type adminAPIKeysIssuer interface {
	Issue(context.Context, admin.IssueRequest) (admin.IssueResult, error)
}

type adminAPIKeysRevoker interface {
	Revoke(context.Context, admin.RevokeRequest) (admin.RevokeResult, error)
}

type adminAPIKeysQueries interface {
	AdminCheckTenantExists(context.Context, int64) (bool, error)
	AdminListAPIKeysForTenant(context.Context, admindb.AdminListAPIKeysForTenantParams) ([]admindb.AdminListAPIKeysForTenantRow, error)
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

// MountAPIKeyRoutes attaches POST/GET/POST-revoke handlers under the
// caller's chi router (typically scoped to /admin/v1/api-keys).
func MountAPIKeyRoutes(r chi.Router, d AdminAPIKeysDeps) {
	r.Post("/", newIssueHandler(d))
	r.Get("/", newListHandler(d))
	r.Post("/{id}/revoke", newRevokeHandler(d))
}

// -----------------------------------------------------------------------------
// POST /admin/v1/api-keys — issue
// -----------------------------------------------------------------------------

type issueRequestBody struct {
	TenantID    int64   `json:"tenant_id"`
	UserID      int64   `json:"user_id"`
	Name        string  `json:"name"`
	Environment string  `json:"environment,omitempty"` // "live" or "test"; default "live"
	ExpiresAt   *string `json:"expires_at,omitempty"`  // RFC3339; null = no expiry
	Reason      string  `json:"reason,omitempty"`
}

type issueResponseBody struct {
	ID              int64   `json:"id"`
	TenantID        int64   `json:"tenant_id"`
	UserID          int64   `json:"user_id"`
	Name            string  `json:"name"`
	KeyPrefix       string  `json:"key_prefix"`
	Status          string  `json:"status"`
	ExpiresAt       *string `json:"expires_at"`
	CreatedAt       string  `json:"created_at"`
	PlaintextBearer string  `json:"plaintext_bearer"` // SHOWN ONCE
}

func newIssueHandler(d AdminAPIKeysDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Issuer == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin issuance dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16) // 64 KiB upper bound
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
			return
		}
		var req issueRequestBody
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if req.TenantID == 0 || req.UserID == 0 || req.Name == "" {
			writeError(w, http.StatusBadRequest, "missing_fields",
				"tenant_id, user_id, and name are required")
			return
		}
		env := admin.EnvLive
		switch req.Environment {
		case "", "live":
			env = admin.EnvLive
		case "test":
			env = admin.EnvTest
		default:
			writeError(w, http.StatusBadRequest, "invalid_environment",
				"environment must be 'live' or 'test'")
			return
		}
		var expiresAt *time.Time
		if req.ExpiresAt != nil && *req.ExpiresAt != "" {
			t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_expires_at",
					"expires_at must be RFC3339")
				return
			}
			// Reject already-expired requests.
			// Otherwise the issuer mints a key + plaintext bearer that
			// the customer resolver immediately refuses.
			if !t.After(time.Now()) {
				writeError(w, http.StatusBadRequest, "expires_at_in_past",
					"expires_at must be strictly in the future")
				return
			}
			expiresAt = &t
		}

		result, err := d.Issuer.Issue(r.Context(), admin.IssueRequest{
			Caller:      ident,
			TenantID:    req.TenantID,
			UserID:      req.UserID,
			Name:        req.Name,
			Environment: env,
			ExpiresAt:   expiresAt,
			Reason:      req.Reason,
			RequestID:   middleware.GetReqID(r.Context()),
		})
		if err != nil {
			writeAdminError(w, err)
			return
		}

		// Header reminds operator key is shown once (D3).
		w.Header().Set("X-Huakai-Key-Display", "once-only")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		var expStr *string
		if result.ExpiresAt != nil {
			s := result.ExpiresAt.Format(time.RFC3339)
			expStr = &s
		}
		_ = json.NewEncoder(w).Encode(issueResponseBody{
			ID:              result.APIKeyID,
			TenantID:        req.TenantID,
			UserID:          req.UserID,
			Name:            req.Name,
			KeyPrefix:       result.KeyPrefix,
			Status:          result.Status,
			ExpiresAt:       expStr,
			CreatedAt:       result.CreatedAt.UTC().Format(time.RFC3339),
			PlaintextBearer: result.Plaintext,
		})
	}
}

// -----------------------------------------------------------------------------
// GET /admin/v1/api-keys — list (tenant-scoped or platform-wide)
// -----------------------------------------------------------------------------

type listItemBody struct {
	ID            int64   `json:"id"`
	TenantID      int64   `json:"tenant_id"`
	UserID        int64   `json:"user_id"`
	Name          string  `json:"name"`
	KeyPrefix     string  `json:"key_prefix"`
	Status        string  `json:"status"`
	ExpiresAt     *string `json:"expires_at"`
	LastUsedAt    *string `json:"last_used_at"`
	RevokedAt     *string `json:"revoked_at"`
	RevokedReason *string `json:"revoked_reason"`
	CreatedAt     string  `json:"created_at"`
}

func newListHandler(d AdminAPIKeysDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Queries == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin list dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		// Tenant scope: tenant_operator must list its own scope; platform_admin
		// MUST pass ?tenant_id=N (no full-fleet list at L0).
		tenantParam := r.URL.Query().Get("tenant_id")
		if tenantParam == "" {
			writeError(w, http.StatusBadRequest, "tenant_id_required",
				"tenant_id query param required")
			return
		}
		tenantID, err := strconv.ParseInt(tenantParam, 10, 64)
		if err != nil || tenantID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_tenant_id",
				"tenant_id must be a positive int64")
			return
		}
		if err := ident.CanIssueForTenant(tenantID); err != nil {
			writeAdminError(w, err)
			return
		}

		// Validate tenant exists BEFORE the list
		// query + audit insert. Otherwise an unknown tenant_id slides
		// through the list (empty result) and then trips the
		// admin_audit_events.tenant_id FK, surfacing as 503 instead of
		// the deterministic 404 the contract documents.
		exists, err := d.Queries.AdminCheckTenantExists(r.Context(), tenantID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "tenant_lookup_failed",
				fmt.Sprintf("tenant existence check failed: %v", err))
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "tenant_not_found",
				fmt.Sprintf("tenant %d not found", tenantID))
			return
		}

		// Malformed pagination must be rejected, not
		// silently coerced to defaults. Otherwise client paging bugs look
		// like successful first-page reads.
		limit := int32(50)
		if s := r.URL.Query().Get("limit"); s != "" {
			v, err := strconv.ParseInt(s, 10, 32)
			if err != nil || v < 1 || v > 500 {
				writeError(w, http.StatusBadRequest, "invalid_limit",
					"limit must be a positive integer between 1 and 500")
				return
			}
			limit = int32(v)
		}
		offset := int32(0)
		if s := r.URL.Query().Get("offset"); s != "" {
			v, err := strconv.ParseInt(s, 10, 32)
			if err != nil || v < 0 {
				writeError(w, http.StatusBadRequest, "invalid_offset",
					"offset must be a non-negative integer")
				return
			}
			offset = int32(v)
		}

		rows, err := d.Queries.AdminListAPIKeysForTenant(r.Context(), admindb.AdminListAPIKeysForTenantParams{
			TenantID:   tenantID,
			PageLimit:  limit,
			PageOffset: offset,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "registry_backend_error",
				fmt.Sprintf("admin list failed: %v", err))
			return
		}

		// Audit successful admin reads. Payload is
		// scoped (tenant + counts + page) — never per-row plaintext or
		// prefix data.
		actorRole := ident.Role
		if actorRole == "" {
			actorRole = admin.RoleTenantOperator
		}
		listPayload, _ := json.Marshal(map[string]any{
			"tenant_id":   tenantID,
			"row_count":   len(rows),
			"page_limit":  limit,
			"page_offset": offset,
		})
		tenantPtr := tenantID
		reqIDPtr := middleware.GetReqID(r.Context())
		var reqIDArg *string
		if reqIDPtr != "" {
			reqIDArg = &reqIDPtr
		}
		// Audit row is part of the contract for admin
		// reads. If we can't write it, fail closed (503) so the operator
		// re-tries against a healthy audit pipe rather than silently
		// dropping the trail.
		if _, err := d.Queries.InsertAdminAuditEvent(r.Context(), admindb.InsertAdminAuditEventParams{
			TenantID:   &tenantPtr,
			ActorID:    fmt.Sprintf("%d", ident.TokenID),
			ActorRole:  actorRole,
			Action:     "list_api_keys",
			TargetType: "api_key",
			RequestID:  reqIDArg,
			Payload:    listPayload,
		}); err != nil {
			writeError(w, http.StatusServiceUnavailable, "audit_write_failed",
				fmt.Sprintf("admin list audit insert failed: %v", err))
			return
		}

		items := make([]listItemBody, 0, len(rows))
		for _, row := range rows {
			item := listItemBody{
				ID:        row.ID,
				TenantID:  row.TenantID,
				UserID:    row.UserID,
				Name:      row.Name,
				KeyPrefix: row.KeyPrefix,
				Status:    row.Status,
				CreatedAt: row.CreatedAt.Time.UTC().Format(time.RFC3339),
			}
			if row.ExpiresAt.Valid {
				s := row.ExpiresAt.Time.UTC().Format(time.RFC3339)
				item.ExpiresAt = &s
			}
			if row.LastUsedAt.Valid {
				s := row.LastUsedAt.Time.UTC().Format(time.RFC3339)
				item.LastUsedAt = &s
			}
			if row.RevokedAt.Valid {
				s := row.RevokedAt.Time.UTC().Format(time.RFC3339)
				item.RevokedAt = &s
			}
			item.RevokedReason = row.RevokedReason
			items = append(items, item)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"items":  items,
			"limit":  limit,
			"offset": offset,
		})
	}
}

// -----------------------------------------------------------------------------
// POST /admin/v1/api-keys/{id}/revoke
// -----------------------------------------------------------------------------

type revokeRequestBody struct {
	TenantID int64  `json:"tenant_id"` // tenant the key belongs to
	Reason   string `json:"reason"`
}

type revokeResponseBody struct {
	ID             int64 `json:"id"`
	AlreadyRevoked bool  `json:"already_revoked"`
}

func newRevokeHandler(d AdminAPIKeysDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Revoker == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin revoke dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_api_key_id",
				"id must be a positive int64")
			return
		}

		// Surface MaxBytesReader truncation so an
		// oversized body whose first bytes happen to parse as valid JSON
		// can't slip through. Mirrors the issue handler.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
			return
		}
		var req revokeRequestBody
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
		}
		if req.TenantID == 0 {
			writeError(w, http.StatusBadRequest, "tenant_id_required",
				"body.tenant_id required (the tenant this api_key belongs to)")
			return
		}

		result, err := d.Revoker.Revoke(r.Context(), admin.RevokeRequest{
			Caller:    ident,
			APIKeyID:  id,
			TenantID:  req.TenantID,
			Reason:    req.Reason,
			RequestID: middleware.GetReqID(r.Context()),
		})
		if err != nil {
			writeAdminError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(revokeResponseBody{
			ID:             result.APIKeyID,
			AlreadyRevoked: result.AlreadyRevoked,
		})
	}
}

// -----------------------------------------------------------------------------
// Error mapping helpers
// -----------------------------------------------------------------------------

// writeAdminAuthError handles ErrAdminUnauthorized + ErrAdminBackend
// from AdminResolver.Resolve. Other admin errors flow through
// writeAdminError.
func writeAdminAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminBackend):
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error",
			"admin auth backend transient failure")
	default:
		writeError(w, http.StatusUnauthorized, "admin_unauthorized",
			"missing or invalid admin credential")
	}
}

func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminUnauthorized):
		writeError(w, http.StatusUnauthorized, "admin_unauthorized", "")
	case errors.Is(err, admin.ErrAdminForbidden):
		writeError(w, http.StatusForbidden, "admin_forbidden",
			"caller cannot act on this tenant scope")
	case errors.Is(err, admin.ErrAdminRateLimited):
		// Shared RateLimited response in
		// docs/openapi/openapi.yaml requires Retry-After. The 30/hour
		// window slides per actor; advise the conservative 60s so a
		// well-behaved client backs off without thrashing.
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "admin_rate_limited",
			"issuance rate limit exceeded")
	case errors.Is(err, admin.ErrAdminBadRequest):
		writeError(w, http.StatusBadRequest, "admin_bad_request", err.Error())
	case errors.Is(err, admin.ErrAdminNotFound):
		writeError(w, http.StatusNotFound, "admin_not_found",
			"target resource not found")
	case errors.Is(err, admin.ErrAdminBackend):
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error",
			"admin backend transient failure")
	default:
		writeError(w, http.StatusInternalServerError, "admin_unknown_error",
			err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// 用 encoding/json 编码而非 fmt %q 手拼:%q 对部分控制字节会产出非法 JSON。本入口的
	// default 分支会回显 err.Error(),可能携带控制字符,故必须用 JSON 编码器保证响应可被严格解析。
	body, err := json.Marshal(map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
	if err != nil {
		body = []byte(`{"error":{"code":"internal_error","message":"internal error"}}`)
	}
	_, _ = w.Write(body)
}
