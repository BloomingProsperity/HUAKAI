// AdminResolver 将运维的 bearer 对 admin_tokens 进行认证。
//
// 流水线(与 auth.APIKeyResolver 对应):
//
//	解析 Bearer header -> 派生 16 字符的 key_prefix -> LookupAdminTokenByPrefix
//	(<= 5 个候选)-> 对每个执行 bcrypt.CompareHashAndPassword -> 检查
//	status + expires_at -> 返回 AdminIdentity{TokenID, Role, ScopeTenantID}
//
// 该 resolver 位于 internal/admin,绝不被 internal/router 或 auth 的热路径
// 引入。错误信息【绝不】包含明文 bearer 或 hash。

package admin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// admin 身份来源:token=admin_tokens(hk_admin_ 程序化凭据),session=admin-role
// 用户会话(role 制单登录)。审计归属据此区分,带 admin_tokens(id) 外键的列只对 token 源写入。
const (
	AdminSourceToken   = "token"
	AdminSourceSession = "session"
)

// AdminIdentity 是 AdminResolver 产出的已解析运维上下文。
// ScopeTenantID 仅在 Role==RoleTenantOperator 时非零;对 platform_admin
// 该字段为 0,且 handler 的 RBAC 允许跨 tenant。
//
// Source 记录凭据通道:token 源 TokenID 有效、UserID=0;session 源反之
// (UserID=发起操作的 users.id、TokenID=0)。空 Source 视同 token(既有令牌通道零变)。
type AdminIdentity struct {
	TokenID       int64
	UserID        int64
	Source        string
	Role          string
	ScopeTenantID int64
	Bootstrap     bool
}

// AuditActor 返回稳定的审计归属串,按来源区分,不因 session-admin 的 TokenID=0
// 而误记成 token 0:
//
//	token 源(含空源,兼容既有)-> "admin_token:<TokenID>"
//	session 源                -> "admin_user:<UserID>"
//
// 本方法保留「程序化 token vs 人的会话」通道来源，确保取证可区分。
// 写端点接入 session 通道前，
// 所有 actor 字段须改走本方法——那是改动持久化审计格式的一步,Owner-gated。
func (i AdminIdentity) AuditActor() string {
	if i.Source == AdminSourceSession {
		return fmt.Sprintf("admin_user:%d", i.UserID)
	}
	return fmt.Sprintf("admin_token:%d", i.TokenID)
}

// AdminResolver 将入站 admin 请求对 admin_tokens 进行认证。
type AdminResolver struct {
	q *admindb.Queries
}

// NewAdminResolver 包装一个 sqlc.Queries 句柄。
func NewAdminResolver(q *admindb.Queries) *AdminResolver {
	return &AdminResolver{q: q}
}

// Resolve 解析 Authorization header 并认证运维。
// 成功时返回 AdminIdentity;对任何凭证失败模式返回 ErrAdminUnauthorized
// (D1 反枚举);对瞬时数据存储故障返回 ErrAdminBackend。
func (r *AdminResolver) Resolve(ctx context.Context, req *http.Request) (AdminIdentity, error) {
	if r == nil || r.q == nil {
		return AdminIdentity{}, fmt.Errorf("%w: resolver not configured", ErrAdminBackend)
	}
	bearer, ok := parseAdminBearer(req.Header.Get("Authorization"))
	if !ok {
		return AdminIdentity{}, ErrAdminUnauthorized
	}
	if !strings.HasPrefix(bearer, "hk_admin_") {
		// 客户 key(hk_live_/hk_test_)不是 admin 凭证。
		return AdminIdentity{}, ErrAdminUnauthorized
	}
	if len(bearer) < PrefixLen {
		return AdminIdentity{}, ErrAdminUnauthorized
	}
	prefix := bearer[:PrefixLen]

	rows, err := r.q.LookupAdminTokenByPrefix(ctx, prefix)
	if err != nil {
		return AdminIdentity{}, fmt.Errorf("%w: lookup: %v", ErrAdminBackend, err)
	}
	now := time.Now().UTC()
	for _, row := range rows {
		if row.Status != "active" {
			continue
		}
		if row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(now) {
			continue
		}
		if err := bcrypt.CompareHashAndPassword([]byte(row.KeyHash), []byte(bearer)); err != nil {
			continue
		}
		var scope int64
		if row.ScopeTenantID != nil {
			scope = *row.ScopeTenantID
		}
		return AdminIdentity{
			TokenID:       row.ID,
			Source:        AdminSourceToken,
			Role:          row.Role,
			ScopeTenantID: scope,
			Bootstrap:     row.Bootstrap,
		}, nil
	}
	return AdminIdentity{}, ErrAdminUnauthorized
}

// parseAdminBearer 从 "Authorization: Bearer <token>" 中提取 token。
// 与 auth.parseBearer 形态相同,但保留为本地实现,以避免本包从
// internal/auth 引入。
func parseAdminBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if tok == "" {
		return "", false
	}
	return tok, true
}

// CanIssueForTenant 判断该身份能否操作一个通用租户资源。
//
// 规则:
//   - platform_admin 可操作任意 tenant；
//   - tenant_operator 只可操作其 ScopeTenantID。
//
// 终端用户与用户 Key 不得使用这条宽权限合同，应改用
// CanManageFinalUsersForTenant。
func (i AdminIdentity) CanIssueForTenant(tenantID int64) error {
	switch i.Role {
	case RolePlatformAdmin:
		return nil
	case RoleTenantOperator:
		if i.ScopeTenantID == tenantID {
			return nil
		}
		return ErrAdminForbidden
	default:
		return ErrAdminUnauthorized
	}
}

// CanOperateOwnedTenant 判断该身份能否对目标租户执行经营性写操作。
// 这类操作包括管理终端用户、用户 Key、订单、订阅、兑换码和人工恢复。
// 它与平台级配置、只读日志和通用租户资源权限有意分离：
//
//   - platform_admin 只管理平台工作租户的终端用户；
//   - tenant_operator 只管理自身作用域租户的终端用户；
//   - 平台工作租户未接线时，platform_admin 必须 fail-closed。
func (i AdminIdentity) CanOperateOwnedTenant(tenantID, platformTenantID int64) error {
	if tenantID <= 0 {
		return ErrAdminForbidden
	}
	switch i.Role {
	case RolePlatformAdmin:
		if platformTenantID <= 0 {
			return fmt.Errorf("%w: platform tenant scope not configured", ErrAdminBackend)
		}
		if tenantID != platformTenantID {
			return ErrAdminForbidden
		}
		return nil
	case RoleTenantOperator:
		if i.ScopeTenantID > 0 && i.ScopeTenantID == tenantID {
			return nil
		}
		return ErrAdminForbidden
	default:
		return ErrAdminUnauthorized
	}
}

// CanManageFinalUsersForTenant 保留终端用户域的语义入口，底层复用统一的
// 所属租户经营边界，避免用户、资金和恢复各自维护一套容易漂移的角色判断。
func (i AdminIdentity) CanManageFinalUsersForTenant(tenantID, platformTenantID int64) error {
	return i.CanOperateOwnedTenant(tenantID, platformTenantID)
}
