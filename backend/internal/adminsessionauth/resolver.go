// Package adminsessionauth 组合 admin 鉴权的两条通道:既有令牌通道(admin_tokens,hk_admin_)
// + session 通道(admin-role 用户 session 直接鉴权 admin 端点)。
// role 制单登录,详见 docs/process/plans/2026-07-01-role-based-auth-migration-claude.md。
package adminsessionauth

import (
	"context"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/panelauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// TokenResolver 是既有令牌通道(admin.AdminResolver 实现),按 hk_admin_ 令牌鉴权。
type TokenResolver interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// SessionValidator 校验用户 session bearer(usersession 服务实现)。
type SessionValidator interface {
	Validate(context.Context, string, string, string) (usersession.ValidatedSession, error)
}

// RoleStore 读 users.role(panelauth.PostgresRoleStore 实现)。ActiveUserRole 同时校验
// 用户和租户有效状态,任一方停用或软删除后 admin 即刻失去 session 权力。
type RoleStore interface {
	ActiveUserRole(context.Context, int64, int64) (string, error)
}

// Resolver 组合令牌通道 + session 通道。
type Resolver struct {
	token    TokenResolver
	session  SessionValidator
	roles    RoleStore
	clientIP *clientip.Resolver
	// deploymentTenantID 是部署者自营租户。只有该租户内明确授权的 admin
	// 会话可映射为平台管理员；其他租户的 admin 会话只能管理本租户。
	deploymentTenantID int64
}

// New 组装组合解析器。
func New(token TokenResolver, session SessionValidator, roles RoleStore, clientIP *clientip.Resolver, deploymentTenantID int64) *Resolver {
	return &Resolver{
		token: token, session: session, roles: roles, clientIP: clientIP,
		deploymentTenantID: deploymentTenantID,
	}
}

// Resolve 先令牌通道(hk_admin_ 前缀恒走),再 session 通道。
// session 任何失败(无效/查角色错/非 admin/未标注写)一律 ErrAdminUnauthorized,与令牌通道反枚举语义一致。
func (r *Resolver) Resolve(ctx context.Context, req *http.Request) (admin.AdminIdentity, error) {
	// nil 接收者 / 未配令牌通道:fail-closed 返 ErrAdminBackend(503),与既有 AdminResolver
	// 的 nil 契约一致(未接线的 admin 面统一 503,不误报 401、也绝不 panic)。
	if r == nil || r.token == nil {
		return admin.AdminIdentity{}, admin.ErrAdminBackend
	}
	bearer, hasBearer := parseBearer(req.Header.Get("Authorization"))
	// hk_admin_ 令牌恒走既有令牌通道。
	if hasBearer && strings.HasPrefix(bearer, "hk_admin_") {
		return r.token.Resolve(ctx, req)
	}
	// 依赖缺失/无 bearer:回退令牌通道(它对非 hk_admin bearer 统一返 ErrAdminUnauthorized)。
	if r.session == nil || r.roles == nil || !hasBearer {
		return r.token.Resolve(ctx, req)
	}
	var ip string
	if r.clientIP != nil {
		ip = r.clientIP.ClientIP(req)
	}
	validated, err := r.session.Validate(ctx, bearer, ip, req.UserAgent())
	if err != nil {
		return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
	}
	role, err := r.roles.ActiveUserRole(ctx, validated.TenantID, validated.UserID)
	if err != nil {
		return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
	}
	// deny-by-default:复用 panelauth 的精确 'admin' 匹配;污染/空/未知/大小写不符一律拒。
	if panelauth.PanelForRole(role) != panelauth.PanelAdmin {
		return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
	}
	// 只读方法(GET/HEAD)照放。写方法按路由在注册处标注的写分级 fail-closed 判定:
	// 未标注 → 拒(默认 token-only,P1 的写端点隐患物理上无法触发);SessionSafe → 放。
	// 危险操作仍由路由分级决定是否允许 session 通道。
	if !isReadOnlyMethod(req.Method) && writeClassFromContext(req.Context()) != SessionSafe {
		return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
	}
	if r.deploymentTenantID <= 0 {
		return admin.AdminIdentity{}, admin.ErrAdminBackend
	}
	identity := admin.AdminIdentity{
		Source:        admin.AdminSourceSession,
		UserID:        validated.UserID,
		Role:          admin.RoleTenantOperator,
		ScopeTenantID: validated.TenantID,
	}
	if validated.TenantID == r.deploymentTenantID {
		identity.Role = admin.RolePlatformAdmin
		identity.ScopeTenantID = 0
	}
	return identity, nil
}

// isReadOnlyMethod 判定请求是否为只读方法。session 通道灰度期只放行这些。
func isReadOnlyMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead:
		return true
	default:
		return false
	}
}

func parseBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return tok, tok != ""
}
