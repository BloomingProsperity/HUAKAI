// Package adminquotahttp exposes tenant-scoped admin CRUD for quota policies
// (/admin/v1/quota-policies). This is anti-abuse OPERATIONAL config: it never
// touches user_balances or the billing ledger. It mirrors adminuserhttp /
// adminhttp channel-catalog: platform_admin/tenant_operator guard, explicit
// tenant scoping, and an admin_audit_events row written atomically with every
// mutation.
package adminquotahttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quotaadmin"
)

const (
	defaultPageLimit = int32(50)
	maxPageLimit     = int32(100)
)

// Deps is the dependency set for the quota-policy admin surface. Auth resolves
// the admin identity; Store runs reads and the audited mutations.
type Deps struct {
	Auth  adminAuth
	Store quotaPolicyStore
}

type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// quotaPolicyStore combines the admin reads with the audited mutation methods.
// The reads accept the sqlc-generated params directly; the mutations take a
// neutral params struct plus the audit row so the adapter can run both inside
// one transaction.
type quotaPolicyStore interface {
	ListQuotaPoliciesForAdmin(context.Context, dbquota.ListQuotaPoliciesForAdminParams) ([]dbquota.QuotaPolicy, error)
	GetQuotaPolicyByID(context.Context, dbquota.GetQuotaPolicyByIDParams) (dbquota.QuotaPolicy, error)
	CreateQuotaPolicyWithAudit(context.Context, quotaPolicyCreateParams, auditInput) (dbquota.QuotaPolicy, error)
	UpdateQuotaPolicyWithAudit(context.Context, quotaPolicyUpdateParams, auditInput) (dbquota.QuotaPolicy, error)
	DeleteQuotaPolicyWithAudit(context.Context, quotaPolicyDeleteParams, auditInput) (int64, error)
}

// MountRoutes registers the id-scoped quota-policy CRUD subtree (GET/PUT/DELETE
// /{id}). The collection GET/POST are mounted at the bare path by the caller
// (see cmd/gateway/routes.go), mirroring how adminuserhttp is wired so chi
// reports the canonical no-trailing-slash collection path.
func MountRoutes(r chi.Router, d Deps) {
	r.Get("/{id}", newGetHandler(d))
	r.Put("/{id}", newUpdateHandler(d))
	r.Delete("/{id}", newDeleteHandler(d))
}

// The exported constructors let the gateway mount each route inline (no
// chi.Route subtree) so the path walker reports the canonical
// no-trailing-slash paths, exactly like the channel-catalog block.
func NewListHandler(d Deps) http.HandlerFunc   { return newListHandler(d) }
func NewCreateHandler(d Deps) http.HandlerFunc { return newCreateHandler(d) }
func NewGetHandler(d Deps) http.HandlerFunc    { return newGetHandler(d) }
func NewUpdateHandler(d Deps) http.HandlerFunc { return newUpdateHandler(d) }
func NewDeleteHandler(d Deps) http.HandlerFunc { return newDeleteHandler(d) }

// NewRouter builds a standalone router for tests: it wires the collection-level
// GET/POST plus the id subtree exactly like the gateway mount.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Get("/", newListHandler(d))
	r.Post("/", newCreateHandler(d))
	MountRoutes(r, d)
	return r
}

// resolveTenantIdentity authenticates the caller and resolves the operating
// tenant. tenant_operator may omit ?tenant_id (uses its scope); platform_admin
// must pass ?tenant_id and is checked via CanIssueForTenant. Mirrors
// adminuserhttp.resolveTenantIdentity so RBAC semantics stay identical.
func resolveTenantIdentity(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "admin_quota_not_configured",
			"admin quota policy dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeError(w, http.StatusForbidden, "admin_tenant_scope_required",
				"tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, 0, false
		}
		if tenantID, ok := tenantFromQueryOrScope(w, r, ident); ok {
			return ident, tenantID, true
		}
		return admin.AdminIdentity{}, 0, false
	case admin.RolePlatformAdmin:
		if tenantID, ok := tenantFromQueryOrScope(w, r, ident); ok {
			return ident, tenantID, true
		}
		return admin.AdminIdentity{}, 0, false
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
}

// tenantFromQueryOrScope resolves the target tenant: a present ?tenant_id is
// validated through CanIssueForTenant (cross-tenant guard); absent it falls
// back to a tenant_operator's own scope.
func tenantFromQueryOrScope(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	tenantParam := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	var tenantID int64
	if tenantParam == "" {
		if ident.Role != admin.RoleTenantOperator {
			writeError(w, http.StatusBadRequest, "tenant_id_required",
				"tenant_id query param required for platform_admin")
			return 0, false
		}
		tenantID = ident.ScopeTenantID
	} else {
		v, err := strconv.ParseInt(tenantParam, 10, 64)
		if err != nil || v <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_tenant_id",
				"tenant_id must be a positive int64")
			return 0, false
		}
		tenantID = v
	}
	if tenantID <= 0 {
		writeAdminAuthError(w, admin.ErrAdminForbidden)
		return 0, false
	}
	if err := ident.CanIssueForTenant(tenantID); err != nil {
		writeAdminAuthError(w, err)
		return 0, false
	}
	return tenantID, true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, "id"))
	if raw == "" {
		writeError(w, http.StatusBadRequest, "quota_policy_id_required", "quota policy id is required")
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_quota_policy_id",
			"quota policy id must be a positive int64")
		return 0, false
	}
	return id, true
}

func pagination(w http.ResponseWriter, r *http.Request) (int32, int32, bool) {
	limit := defaultPageLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
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
			writeError(w, http.StatusBadRequest, "invalid_offset", "offset must be a non-negative integer")
			return 0, 0, false
		}
		offset = int32(parsed)
	}
	return limit, offset, true
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
		"error": {"code": code, "message": message},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func timestamp(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339)
}
