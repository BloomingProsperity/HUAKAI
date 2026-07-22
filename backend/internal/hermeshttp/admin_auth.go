package hermeshttp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesprincipal"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

// AdminAuthResolver 组合程序化管理员令牌与管理员会话。
type AdminAuthResolver interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type capabilityChecker interface {
	Allowed(context.Context, int64, string) (bool, error)
}

type principalEnsurer interface {
	Ensure(context.Context, int64) (hermesprincipal.Principal, error)
}

// AdminAuthDeps 是 Hermes 管理入口的完整身份依赖。
type AdminAuthDeps struct {
	Resolver         AdminAuthResolver
	PlatformTenantID int64
	Capabilities     capabilityChecker
	Principals       principalEnsurer
}

type adminActorContextKey struct{}

// adminActor 记录真正发起操作的管理员，而 sessionauth.Identity 只携带内部服务主体外键。
type adminActor struct {
	Source string
	ID     int64
	Role   string
}

// AdminAuthMiddleware 只接受部署者和已授权的下级租户管理员。
// 目标租户完全由认证身份推导，请求不能指定租户或模拟某个普通用户。
func AdminAuthMiddleware(d AdminAuthDeps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if d.Resolver == nil || d.Principals == nil || d.PlatformTenantID <= 0 {
				writeError(w, http.StatusServiceUnavailable, "hermes_admin_gate_not_configured", "hermes admin auth dependencies unset")
				return
			}
			if hasLegacyIdentityOverride(r) {
				writeError(w, http.StatusBadRequest, "hermes_identity_override_forbidden", "hermes tenant and actor come from the authenticated administrator")
				return
			}

			identity, err := d.Resolver.Resolve(r.Context(), r)
			if err != nil {
				if errors.Is(err, admin.ErrAdminBackend) {
					writeError(w, http.StatusServiceUnavailable, "hermes_admin_backend_error", "hermes admin auth backend transient failure")
					return
				}
				writeError(w, http.StatusUnauthorized, "hermes_admin_unauthorized", "missing or invalid admin credential")
				return
			}

			tenantID, ok := authorizedHermesTenant(w, r, d, identity)
			if !ok {
				return
			}
			actor, ok := actorFromAdminIdentity(identity)
			if !ok {
				writeError(w, http.StatusUnauthorized, "hermes_admin_unauthorized", "administrator actor identity is incomplete")
				return
			}
			principal, err := d.Principals.Ensure(r.Context(), tenantID)
			if err != nil {
				if errors.Is(err, hermesprincipal.ErrTenantMissing) {
					writeError(w, http.StatusForbidden, "hermes_admin_forbidden_scope", "administrator tenant is unavailable")
					return
				}
				writeError(w, http.StatusServiceUnavailable, "hermes_principal_unavailable", "hermes internal service principal is unavailable")
				return
			}

			serviceIdentity := sessionauth.Identity{
				TenantID: principal.TenantID,
				UserID:   principal.UserID,
				APIKeyID: principal.APIKeyID,
			}
			ctx := context.WithValue(r.Context(), authContextKey{}, serviceIdentity)
			ctx = context.WithValue(ctx, adminActorContextKey{}, actor)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func authorizedHermesTenant(w http.ResponseWriter, r *http.Request, d AdminAuthDeps, identity admin.AdminIdentity) (int64, bool) {
	switch identity.Role {
	case admin.RolePlatformAdmin:
		return d.PlatformTenantID, true
	case admin.RoleTenantOperator:
		if identity.ScopeTenantID <= 0 || d.Capabilities == nil {
			writeError(w, http.StatusServiceUnavailable, "hermes_capability_unavailable", "hermes tenant capability dependency is unavailable")
			return 0, false
		}
		allowed, err := d.Capabilities.Allowed(r.Context(), identity.ScopeTenantID, tenantcapability.HermesOperations)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "hermes_capability_backend_error", "hermes tenant capability backend transient failure")
			return 0, false
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "hermes_capability_required", "tenant administrator has not been granted hermes operations")
			return 0, false
		}
		return identity.ScopeTenantID, true
	default:
		writeError(w, http.StatusForbidden, "hermes_admin_forbidden_role", "administrator role may not use hermes")
		return 0, false
	}
}

func actorFromAdminIdentity(identity admin.AdminIdentity) (adminActor, bool) {
	actor := adminActor{Source: identity.Source, Role: identity.Role}
	if actor.Source == "" {
		actor.Source = admin.AdminSourceToken
	}
	switch actor.Source {
	case admin.AdminSourceToken:
		actor.ID = identity.TokenID
	case admin.AdminSourceSession:
		actor.ID = identity.UserID
	default:
		return adminActor{}, false
	}
	if actor.ID <= 0 || strings.TrimSpace(actor.Role) == "" {
		return adminActor{}, false
	}
	return actor, true
}

func hasLegacyIdentityOverride(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	query := r.URL.Query()
	return strings.TrimSpace(query.Get("tenant_id")) != "" || strings.TrimSpace(query.Get("as_user_id")) != ""
}

func adminActorFromContext(ctx context.Context) (adminActor, bool) {
	actor, ok := ctx.Value(adminActorContextKey{}).(adminActor)
	return actor, ok
}
