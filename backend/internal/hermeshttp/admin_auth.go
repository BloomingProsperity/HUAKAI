package hermeshttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
)

// AdminAuthResolver authenticates an operator bearer (hk_admin_) against
// admin_tokens and returns its resolved identity. *admin.AdminResolver
// satisfies it; the interface keeps the middleware unit-testable with injected
// identities (mirrors cmd/gateway.adminIdentityResolver).
type AdminAuthResolver interface {
	Resolve(ctx context.Context, r *http.Request) (admin.AdminIdentity, error)
}

// adminActorContextKey carries the resolved operator identity so the audit path
// can attribute the real admin actor even though actor_user_id still points at
// the tenant user whose ops context the operator is acting within.
type adminActorContextKey struct{}

// adminActor is the operator attribution recorded alongside an admin-mode
// Hermes action. It is distinct from the threaded sessionauth.Identity, which
// continues to carry (tenant_id, user_id) so the existing users FK holds.
type adminActor struct {
	TokenID int64
	Role    string
}

// AdminAuthMiddleware gates Hermes routes to authenticated ADMIN/OPERATOR
// callers under the admin-only repositioning. It mirrors cmd/gateway.adminGate's
// status mapping (401 on credential failure, 503 on backend / nil resolver) and
// then derives the tenant + the tenant user whose Hermes ops context the
// operator acts within, enforcing CanIssueForTenant BEFORE the request reaches
// any tenant-scoped handler.
//
// Tenant derivation:
//   - tenant_operator  => tenant_id is the token's ScopeTenantID. A ?tenant_id
//     query param, if present, must match it (else 403) — an operator can never
//     reach outside its scope.
//   - platform_admin   => tenant_id MUST be supplied via ?tenant_id (a
//     platform admin has no implicit tenant; omitting it is a 400, never a
//     silent cross-tenant default).
//
// Actor user derivation: the operator must name which tenant user's Hermes ops
// context is being acted on via ?as_user_id. That id becomes the threaded
// sessionauth.Identity.UserID, which the existing composite FK
// (tenant_id, owner_user_id/actor_user_id) -> users(tenant_id, id) requires to
// resolve to a real users row. The operator's own token id is recorded
// separately as the admin actor for audit attribution.
func AdminAuthMiddleware(resolver AdminAuthResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if resolver == nil {
				writeError(w, http.StatusServiceUnavailable, "hermes_admin_gate_not_configured", "hermes admin auth resolver unset")
				return
			}
			id, err := resolver.Resolve(r.Context(), r)
			if err != nil {
				if errors.Is(err, admin.ErrAdminBackend) {
					writeError(w, http.StatusServiceUnavailable, "hermes_admin_backend_error", "hermes admin auth backend transient failure")
					return
				}
				writeError(w, http.StatusUnauthorized, "hermes_admin_unauthorized", "missing or invalid admin credential")
				return
			}

			tenantID, ok := deriveAdminTenantID(w, r, id)
			if !ok {
				return
			}
			// A non-positive resolved tenant (e.g. an operator token with no
			// scope and no ?tenant_id) can never name a real tenant; reject it
			// before CanIssueForTenant rather than threading a zero tenant.
			if tenantID <= 0 {
				writeError(w, http.StatusForbidden, "hermes_admin_forbidden_scope", "operator token has no usable tenant scope")
				return
			}
			// Enforce scope BEFORE any tenant-scoped op. This is the SINGLE
			// authority for tenant scoping: tenant_operator may only touch its
			// ScopeTenantID; platform_admin may touch any tenant.
			if err := id.CanIssueForTenant(tenantID); err != nil {
				writeError(w, http.StatusForbidden, "hermes_admin_forbidden_scope", "operator may not access this tenant's hermes resources")
				return
			}

			asUserID, ok := parseAsUserID(w, r)
			if !ok {
				return
			}

			ident := sessionauth.Identity{TenantID: tenantID, UserID: asUserID}
			ctx := context.WithValue(r.Context(), authContextKey{}, ident)
			ctx = context.WithValue(ctx, adminActorContextKey{}, adminActor{TokenID: id.TokenID, Role: id.Role})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// deriveAdminTenantID resolves the REQUESTED target tenant from the operator
// identity and the optional ?tenant_id query param. It does NOT itself enforce
// the operator scope — CanIssueForTenant in the middleware is the single
// authority that authorizes the resolved tenant, so a tenant_operator that
// requests a foreign ?tenant_id is resolved here and then rejected there
// (keeping the scope rule in exactly one place). This function only enforces
// the platform_admin no-implicit-tenant rule (platform admins have no scope to
// default to, so omitting ?tenant_id is a 400, never a silent default).
func deriveAdminTenantID(w http.ResponseWriter, r *http.Request, id admin.AdminIdentity) (int64, bool) {
	paramTenant, hasParam, ok := parseOptionalPositiveQuery(w, r, "tenant_id")
	if !ok {
		return 0, false
	}
	switch id.Role {
	case admin.RolePlatformAdmin:
		if !hasParam {
			writeError(w, http.StatusBadRequest, "hermes_admin_tenant_required", "platform_admin must specify ?tenant_id")
			return 0, false
		}
		return paramTenant, true
	case admin.RoleTenantOperator:
		// An explicit ?tenant_id is honored as the REQUESTED tenant (even a
		// foreign one) so that CanIssueForTenant can reject it; when omitted,
		// default to the operator's own scope.
		if hasParam {
			return paramTenant, true
		}
		return id.ScopeTenantID, true
	default:
		// Unknown role: resolve the requested/zero tenant and let
		// CanIssueForTenant reject it (it maps unknown roles to unauthorized).
		if hasParam {
			return paramTenant, true
		}
		return id.ScopeTenantID, true
	}
}

// parseAsUserID requires the ?as_user_id query param naming the tenant user
// whose Hermes ops context the operator acts within. It must be a positive
// int64 so the (tenant_id, user_id) users FK can resolve.
func parseAsUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value, has, ok := parseOptionalPositiveQuery(w, r, "as_user_id")
	if !ok {
		return 0, false
	}
	if !has {
		writeError(w, http.StatusBadRequest, "hermes_admin_user_required", "admin-mode hermes requires ?as_user_id naming the tenant user context")
		return 0, false
	}
	return value, true
}

// parseOptionalPositiveQuery parses an optional positive-int64 query param.
// Returns (value, present, ok). A present-but-malformed/non-positive value is a
// 400 (ok=false); absent is (0, false, true).
func parseOptionalPositiveQuery(w http.ResponseWriter, r *http.Request, name string) (int64, bool, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, false, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		writeError(w, http.StatusBadRequest, "hermes_admin_invalid_param", name+" must be a positive integer")
		return 0, false, false
	}
	return value, true, true
}

// adminActorFromContext returns the operator attribution injected by
// AdminAuthMiddleware, if the request was authenticated in admin mode.
func adminActorFromContext(ctx context.Context) (adminActor, bool) {
	actor, ok := ctx.Value(adminActorContextKey{}).(adminActor)
	return actor, ok
}
