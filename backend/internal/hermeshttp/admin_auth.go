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

// AdminAuthResolver 用 admin_tokens 校验 operator bearer(hk_admin_),
// 并返回其解析出的身份。*admin.AdminResolver 实现了该接口;此接口使中间件
// 可以注入身份进行单元测试(对应 cmd/gateway.adminIdentityResolver)。
type AdminAuthResolver interface {
	Resolve(ctx context.Context, r *http.Request) (admin.AdminIdentity, error)
}

// adminActorContextKey 携带解析出的 operator 身份,使审计路径即便 actor_user_id
// 仍指向 operator 当前所操作 ops context 所属的 tenant user,也能把动作归因到
// 真正的 admin actor。
type adminActorContextKey struct{}

// adminActor 是与 admin 模式 Hermes 动作一并记录的 operator 归因信息。它区别于
// 贯穿传递的 sessionauth.Identity——后者仍携带 (tenant_id, user_id),以保证既有的
// users FK 成立。
type adminActor struct {
	TokenID int64
	Role    string
}

// AdminAuthMiddleware 在 admin-only 重定位下,把 Hermes 路由限制给已认证的
// ADMIN/OPERATOR 调用方。它对齐 cmd/gateway.adminGate 的状态码映射(凭证失败 401、
// backend 故障 / resolver 为 nil 时 503),随后推导出 tenant 以及 operator 当前所操作
// Hermes ops context 所属的 tenant user,并在请求到达任何 tenant 范围的 handler
// 之前先执行 CanIssueForTenant。
//
// Tenant 推导:
//   - tenant_operator  => tenant_id 取 token 的 ScopeTenantID。若带了 ?tenant_id
//     query 参数,必须与之相等(否则 403)——operator 永远无法越出自身 scope。
//   - platform_admin   => tenant_id 必须通过 ?tenant_id 提供(platform admin 没有
//     隐含 tenant;缺省即 400,绝不静默地默认成某个跨 tenant 的值)。
//
// Actor user 推导:operator 必须通过 ?as_user_id 指明所操作的是哪个 tenant user 的
// Hermes ops context。该 id 成为贯穿传递的 sessionauth.Identity.UserID,而既有的
// 复合 FK (tenant_id, owner_user_id/actor_user_id) -> users(tenant_id, id) 要求它能
// 解析到一条真实的 users 行。operator 自己的 token id 则单独记录为 admin actor,
// 用于审计归因。
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
			// 解析出的 tenant 非正(例如既无 scope 又无 ?tenant_id 的 operator
			// token)永远无法指向一个真实 tenant;在 CanIssueForTenant 之前就拒绝,
			// 而不是把一个 0 值 tenant 继续传下去。
			if tenantID <= 0 {
				writeError(w, http.StatusForbidden, "hermes_admin_forbidden_scope", "operator token has no usable tenant scope")
				return
			}
			// 在任何 tenant 范围的操作之前先执行 scope 校验。这里是 tenant
			// scoping 的唯一权威:tenant_operator 只能触及自身 ScopeTenantID;
			// platform_admin 可触及任意 tenant。
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

// deriveAdminTenantID 根据 operator 身份与可选的 ?tenant_id query 参数,解析出
// 请求的目标 tenant。它本身不执行 operator scope 校验——中间件里的
// CanIssueForTenant 才是授权该 tenant 的唯一权威,因此 tenant_operator 请求一个外部
// ?tenant_id 时,这里会先解析出来、再由那里拒绝(把 scope 规则集中在唯一一处)。
// 本函数只执行 platform_admin 的「无隐含 tenant」规则(platform admin 没有可默认的
// scope,故缺省 ?tenant_id 即 400,绝不静默默认)。
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
		// 显式的 ?tenant_id 会被当作请求的 tenant(哪怕是外部 tenant),以便
		// CanIssueForTenant 能拒绝它;缺省时则默认为 operator 自身的 scope。
		if hasParam {
			return paramTenant, true
		}
		return id.ScopeTenantID, true
	default:
		// 未知 role:解析出请求的 tenant(或 0),交由 CanIssueForTenant 拒绝
		// (它把未知 role 映射为 unauthorized)。
		if hasParam {
			return paramTenant, true
		}
		return id.ScopeTenantID, true
	}
}

// parseAsUserID 要求 ?as_user_id query 参数,用以指明 operator 当前所操作 Hermes
// ops context 所属的 tenant user。它必须是正 int64,这样 (tenant_id, user_id) 的
// users FK 才能解析。
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

// parseOptionalPositiveQuery 解析一个可选的正 int64 query 参数。返回
// (value, present, ok)。存在但格式错误/非正的值即 400(ok=false);缺省则为
// (0, false, true)。
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

// adminActorFromContext 返回 AdminAuthMiddleware 注入的 operator 归因信息
// (前提是该请求以 admin 模式完成认证)。
func adminActorFromContext(ctx context.Context) (adminActor, bool) {
	actor, ok := ctx.Value(adminActorContextKey{}).(adminActor)
	return actor, ok
}
