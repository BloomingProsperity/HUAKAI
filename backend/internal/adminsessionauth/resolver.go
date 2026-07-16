// Package adminsessionauth 组合 admin 鉴权的两条通道:既有令牌通道(admin_tokens,hk_admin_)
// + session 通道(admin-role 用户 session 直接鉴权 admin 端点)。登录即管理员是产品形态,无开关。
// role 制单登录,详见 docs/process/plans/2026-07-01-role-based-auth-migration-claude.md。
package adminsessionauth

import (
	"context"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
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

// IdentityStore 从可信数据库状态解析 session admin 的角色与私有租户作用域。
type IdentityStore interface {
	ResolveActiveAdminIdentity(ctx context.Context, tenantID, userID int64) (admin.AdminIdentity, error)
}

// IdentityStoreFunc 把受控函数适配为 IdentityStore，供接线与测试脚手架共用。
type IdentityStoreFunc func(context.Context, int64, int64) (admin.AdminIdentity, error)

// ResolveActiveAdminIdentity 实现 IdentityStore。
func (f IdentityStoreFunc) ResolveActiveAdminIdentity(ctx context.Context, tenantID, userID int64) (admin.AdminIdentity, error) {
	if f == nil {
		return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
	}
	return f(ctx, tenantID, userID)
}

// Resolver 组合令牌通道 + session 通道。
type Resolver struct {
	token      TokenResolver
	session    SessionValidator
	identities IdentityStore
	clientIP   *clientip.Resolver
}

// New 组装组合解析器。
func New(token TokenResolver, session SessionValidator, identities IdentityStore, clientIP *clientip.Resolver) *Resolver {
	return &Resolver{token: token, session: session, identities: identities, clientIP: clientIP}
}

// Resolve 先令牌通道(hk_admin_ 前缀恒走),再 session 通道。
// session 任何失败一律 ErrAdminUnauthorized,与令牌通道反枚举语义一致。
func (r *Resolver) Resolve(ctx context.Context, req *http.Request) (admin.AdminIdentity, error) {
	if r == nil || r.token == nil {
		return admin.AdminIdentity{}, admin.ErrAdminBackend
	}
	bearer, hasBearer := parseBearer(req.Header.Get("Authorization"))
	if hasBearer && strings.HasPrefix(bearer, "hk_admin_") {
		return r.token.Resolve(ctx, req)
	}
	if r.session == nil || r.identities == nil || !hasBearer {
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
	identity, err := r.identities.ResolveActiveAdminIdentity(ctx, validated.TenantID, validated.UserID)
	if err != nil {
		return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
	}
	if !identity.IsValid() || identity.Source != admin.AdminSourceSession || identity.UserID != validated.UserID {
		return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
	}
	if !identity.IsPlatformWide() && identity.ScopeTenantID() != validated.TenantID {
		return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
	}
	// 只读方法(GET/HEAD)照放。写方法按路由在注册处标注的写分级 fail-closed 判定:
	// 未标注 → 拒；SessionSafe → 放。
	if !isReadOnlyMethod(req.Method) && writeClassFromContext(req.Context()) != SessionSafe {
		return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
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
