// Package adminuserhttp exposes read-only admin user visibility endpoints.
package adminuserhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

const (
	defaultPageLimit = int32(50)
	maxPageLimit     = int32(100)
)

type Deps struct {
	Auth  adminAuth
	Store userReadStore
}

type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type userReadStore interface {
	AdminListUsersForTenant(context.Context, admindb.AdminListUsersForTenantParams) ([]admindb.AdminListUsersForTenantRow, error)
	AdminGetUserForTenant(context.Context, admindb.AdminGetUserForTenantParams) (admindb.AdminGetUserForTenantRow, error)
	AdminListUserBalanceHistoryForTenant(context.Context, admindb.AdminListUserBalanceHistoryForTenantParams) ([]admindb.AdminListUserBalanceHistoryForTenantRow, error)
}

func MountRoutes(r chi.Router, d Deps) {
	r.Get("/", newListHandler(d))
	r.Get("/{id}", newGetHandler(d))
	r.Get("/{id}/balance-history", newBalanceHistoryHandler(d))
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	MountRoutes(r, d)
	return r
}

func NewListHandler(d Deps) http.HandlerFunc {
	return newListHandler(d)
}

type userBody struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	Balance   string `json:"balance"`
	CreatedAt string `json:"created_at"`
}

type balanceHistoryBody struct {
	ID          int64  `json:"id"`
	EventType   string `json:"event_type"`
	Amount      string `json:"amount"`
	Fingerprint string `json:"fingerprint"`
	SourceType  string `json:"source_type"`
	SourceID    int64  `json:"source_id"`
	OccurredAt  string `json:"occurred_at"`
}

func newListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		limit, offset, ok := pagination(w, r)
		if !ok {
			return
		}
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		rows, err := d.Store.AdminListUsersForTenant(r.Context(), admindb.AdminListUsersForTenantParams{
			TenantID:   tenantID,
			Query:      query,
			PageLimit:  limit,
			PageOffset: offset,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("list users failed: %v", err))
			return
		}
		items := make([]userBody, 0, len(rows))
		for _, row := range rows {
			items = append(items, userBody{
				ID:        row.ID,
				Email:     row.Email,
				Role:      row.Role,
				Status:    row.Status,
				Balance:   row.Balance,
				CreatedAt: timestamp(row.CreatedAt.Time),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":  items,
			"limit":  limit,
			"offset": offset,
		})
	}
}

func newGetHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		row, err := d.Store.AdminGetUserForTenant(r.Context(), admindb.AdminGetUserForTenantParams{
			TenantID: tenantID,
			UserID:   userID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("get user failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, userBody{
			ID:        row.ID,
			Email:     row.Email,
			Role:      row.Role,
			Status:    row.Status,
			Balance:   row.Balance,
			CreatedAt: timestamp(row.CreatedAt.Time),
		})
	}
}

func newBalanceHistoryHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		limit, offset, ok := pagination(w, r)
		if !ok {
			return
		}
		if _, err := d.Store.AdminGetUserForTenant(r.Context(), admindb.AdminGetUserForTenantParams{
			TenantID: tenantID,
			UserID:   userID,
		}); errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		} else if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("get user failed: %v", err))
			return
		}
		rows, err := d.Store.AdminListUserBalanceHistoryForTenant(r.Context(), admindb.AdminListUserBalanceHistoryForTenantParams{
			TenantID:   tenantID,
			UserID:     userID,
			PageLimit:  limit,
			PageOffset: offset,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("list balance history failed: %v", err))
			return
		}
		items := make([]balanceHistoryBody, 0, len(rows))
		for _, row := range rows {
			items = append(items, balanceHistoryBody{
				ID:          row.ID,
				EventType:   row.EventType,
				Amount:      row.Amount,
				Fingerprint: row.Fingerprint,
				SourceType:  row.SourceType,
				SourceID:    row.SourceID,
				OccurredAt:  timestamp(row.OccurredAt.Time),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":  items,
			"limit":  limit,
			"offset": offset,
		})
	}
}

func resolveTenant(w http.ResponseWriter, r *http.Request, d Deps) (int64, bool) {
	if d.Auth == nil || d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured",
			"admin users dependency unset")
		return 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeError(w, http.StatusForbidden, "admin_tenant_scope_required",
				"tenant_operator scope_tenant_id required")
			return 0, false
		}
		return ident.ScopeTenantID, true
	case admin.RolePlatformAdmin:
		writeError(w, http.StatusForbidden, "admin_tenant_scope_required",
			"tenant-scoped admin user reads require a tenant_operator identity")
		return 0, false
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden_scope",
			"admin role required")
		return 0, false
	}
}

func pagination(w http.ResponseWriter, r *http.Request) (int32, int32, bool) {
	limit := defaultPageLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_limit",
				"limit must be a positive integer")
			return 0, 0, false
		}
		limit = int32(parsed)
		if limit > maxPageLimit {
			limit = maxPageLimit
		}
	}
	offset := int32(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid_offset",
				"offset must be a non-negative integer")
			return 0, 0, false
		}
		offset = int32(parsed)
	}
	return limit, offset, true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_user_id",
			"user id must be a positive int64")
		return 0, false
	}
	return id, true
}

func writeAdminAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, admin.ErrAdminBackend) {
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error",
			"admin auth backend transient failure")
		return
	}
	if errors.Is(err, admin.ErrAdminForbidden) {
		writeError(w, http.StatusForbidden, "admin_forbidden_scope",
			"admin credential is not allowed for this tenant")
		return
	}
	writeError(w, http.StatusUnauthorized, "admin_unauthorized",
		"missing or invalid admin credential")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {
			"code":    code,
			"message": message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
