// AdminResolver 将运维 bearer 对 admin_tokens 进行认证。
//
// 流水线(与 auth.APIKeyResolver 对应):
//
//	解析 Bearer header -> 派生 16 字符的 key_prefix -> LookupAdminTokenByPrefix
//	(<= 5 个候选)-> 对每个执行 bcrypt.CompareHashAndPassword -> 检查
//	status + expires_at -> 通过受控入口构造带私有作用域的 AdminIdentity
//
// 该 resolver 位于 internal/admin,绝不被 internal/router 或 auth 的热路径
// 引入。错误信息【绝不】包含明文 bearer 或 hash。

package admin

import (
	"context"
	"fmt"
	"log/slog"
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

// MaxTenantScopeDepth 是递归授权树可接受的最大边深度。
const MaxTenantScopeDepth int32 = 32

type tenantScopeKind uint8

const (
	tenantScopeInvalid tenantScopeKind = iota
	tenantScopePlatform
	tenantScopeSubtree
)

type tenantScope struct {
	kind              tenantScopeKind
	rootTenantID      int64
	descendantIDs     map[int64]struct{}
	rootIsChildTenant bool
}

// TenantScopeNode 是身份构造入口从可信租户查询接收的最小只读投影。
type TenantScopeNode struct {
	TenantID         int64
	Depth            int32
	CycleDetected    bool
	ScopeRootIsChild bool
}

// TenantScopeLoader 只允许 resolver 从可信存储装载租户树，handler 不应实现它。
type TenantScopeLoader func(context.Context, int64) ([]TenantScopeNode, error)

// IdentityClaims 是 resolver 已认证的主体声明。作用域仍由 TenantScopeLoader
// 从数据库装载，不能由请求字段或 handler 自行填充。
type IdentityClaims struct {
	TokenID       int64
	UserID        int64
	Source        string
	Role          string
	ScopeTenantID int64
	Bootstrap     bool
}

// AdminIdentity 是 resolver 产出的已解析运维上下文。租户根、后代集合与
// 根租户形态全部私有，只能经本文件的方法读取或裁决。
//
// Source 记录凭据通道:token 源 TokenID 有效、UserID=0;session 源反之
// (UserID=发起操作的 users.id、TokenID=0)。空 Source 视同 token(既有令牌通道零变)。
type AdminIdentity struct {
	TokenID   int64
	UserID    int64
	Source    string
	Role      string
	Bootstrap bool

	scope tenantScope
}

// NewAdminIdentity 是身份对象的唯一受控构造入口。平台全域身份不查租户树；
// 其他已知角色必须带正租户根，并从参数化查询装载完整活动子树。
func NewAdminIdentity(ctx context.Context, claims IdentityClaims, load TenantScopeLoader) (AdminIdentity, error) {
	identity := AdminIdentity{
		TokenID: claims.TokenID, UserID: claims.UserID, Source: claims.Source,
		Role: claims.Role, Bootstrap: claims.Bootstrap,
	}

	switch claims.Role {
	case RolePlatformAdmin:
		if claims.ScopeTenantID != 0 {
			// platform_admin 必须没有租户上限。任何非零 scope 都是结构性非法身份，
			// 在唯一构造入口 fail-closed，避免下游仅比较角色的 gate 误放。
			return AdminIdentity{}, ErrAdminUnauthorized
		}
		identity.scope = tenantScope{kind: tenantScopePlatform}
		return identity, nil
	case RoleTenantOperator:
		// 非平台身份继续进入下方的可信子树装载。
	default:
		return AdminIdentity{}, ErrAdminUnauthorized
	}
	if claims.ScopeTenantID <= 0 || load == nil {
		return AdminIdentity{}, ErrAdminUnauthorized
	}

	nodes, err := load(ctx, claims.ScopeTenantID)
	if err != nil {
		return AdminIdentity{}, fmt.Errorf("%w: load tenant scope: %v", ErrAdminBackend, err)
	}
	scope, err := validateTenantScope(claims.ScopeTenantID, nodes)
	if err != nil {
		slog.ErrorContext(ctx, "拒绝异常租户授权树",
			"scope_tenant_id", claims.ScopeTenantID,
			"reason", err,
		)
		return AdminIdentity{}, ErrAdminUnauthorized
	}
	identity.scope = scope
	return identity, nil
}

func validateTenantScope(rootTenantID int64, nodes []TenantScopeNode) (tenantScope, error) {
	if len(nodes) == 0 {
		return tenantScope{}, fmt.Errorf("作用域根不存在或未启用")
	}
	rootIsChild := nodes[0].ScopeRootIsChild
	descendants := make(map[int64]struct{}, len(nodes)-1)
	seen := make(map[int64]struct{}, len(nodes))
	for index, node := range nodes {
		if node.TenantID <= 0 || node.Depth < 0 {
			return tenantScope{}, fmt.Errorf("作用域节点非法")
		}
		if node.CycleDetected {
			return tenantScope{}, fmt.Errorf("作用域树检测到环")
		}
		if node.Depth > MaxTenantScopeDepth {
			return tenantScope{}, fmt.Errorf("作用域树深度超过 %d", MaxTenantScopeDepth)
		}
		if node.ScopeRootIsChild != rootIsChild {
			return tenantScope{}, fmt.Errorf("作用域根形态不一致")
		}
		if _, duplicate := seen[node.TenantID]; duplicate {
			return tenantScope{}, fmt.Errorf("作用域节点重复")
		}
		seen[node.TenantID] = struct{}{}
		if index == 0 {
			if node.TenantID != rootTenantID || node.Depth != 0 {
				return tenantScope{}, fmt.Errorf("作用域根不匹配")
			}
			continue
		}
		if node.Depth == 0 || node.TenantID == rootTenantID {
			return tenantScope{}, fmt.Errorf("作用域后代层级非法")
		}
		descendants[node.TenantID] = struct{}{}
	}
	return tenantScope{
		kind:              tenantScopeSubtree,
		rootTenantID:      rootTenantID,
		descendantIDs:     descendants,
		rootIsChildTenant: rootIsChild,
	}, nil
}

// IsValid 表示身份角色与私有作用域快照相互一致。
func (i AdminIdentity) IsValid() bool {
	switch i.Role {
	case RolePlatformAdmin:
		return i.scope.kind == tenantScopePlatform ||
			(i.scope.kind == tenantScopeSubtree && i.scope.rootTenantID > 0)
	case RoleTenantOperator:
		return i.scope.kind == tenantScopeSubtree && i.scope.rootTenantID > 0
	default:
		return false
	}
}

// IsPlatformWide 仅对经过受控构造的无租户上限平台身份返回 true。
func (i AdminIdentity) IsPlatformWide() bool {
	return i.Role == RolePlatformAdmin && i.scope.kind == tenantScopePlatform
}

// ScopeTenantID 返回受控作用域根；平台全域或非法身份返回 0。
func (i AdminIdentity) ScopeTenantID() int64 {
	if !i.IsValid() || i.scope.kind != tenantScopeSubtree {
		return 0
	}
	return i.scope.rootTenantID
}

// CanAccessProviderAccountControlPlane 拒绝所有子租户分销商触达账号、凭证、
// 明文密钥或密钥加密相关控制面。根租户 scoped operator 保持单租户兼容语义。
func (i AdminIdentity) CanAccessProviderAccountControlPlane() error {
	if !i.IsValid() {
		return ErrAdminUnauthorized
	}
	if i.scope.kind == tenantScopeSubtree && i.scope.rootIsChildTenant {
		return ErrAdminForbidden
	}
	return nil
}

// AuditActor 返回稳定的审计归属串,按来源区分,不因 session-admin 的 TokenID=0
// 而误记成 token 0:
//
//	token 源(含空源,兼容既有)-> "admin_token:<TokenID>"
//	session 源                -> "admin_user:<UserID>"
//
// 本方法保留「程序化 token vs 人的会话」通道来源(取证可分)。写端点接入 session 通道前,
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
		var scopeTenantID int64
		if row.ScopeTenantID != nil {
			scopeTenantID = *row.ScopeTenantID
		}
		return NewAdminIdentity(ctx, IdentityClaims{
			TokenID: row.ID, Source: AdminSourceToken, Role: row.Role,
			ScopeTenantID: scopeTenantID, Bootstrap: row.Bootstrap,
		}, r.loadTenantScope)
	}
	return AdminIdentity{}, ErrAdminUnauthorized
}

func (r *AdminResolver) loadTenantScope(ctx context.Context, rootTenantID int64) ([]TenantScopeNode, error) {
	rows, err := r.q.ListActiveTenantScope(ctx, rootTenantID)
	if err != nil {
		return nil, err
	}
	nodes := make([]TenantScopeNode, 0, len(rows))
	for _, row := range rows {
		nodes = append(nodes, TenantScopeNode{
			TenantID: row.ID, Depth: row.Depth, CycleDetected: row.CycleDetected,
			ScopeRootIsChild: row.ScopeRootIsChild,
		})
	}
	return nodes, nil
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

// CanActOnTenant 是所有 admin handler 的唯一租户目标裁决口。
func (i AdminIdentity) CanActOnTenant(tenantID int64) error {
	if !i.IsValid() {
		return ErrAdminUnauthorized
	}
	if tenantID <= 0 {
		return ErrAdminForbidden
	}
	if i.IsPlatformWide() {
		return nil
	}
	if tenantID == i.scope.rootTenantID {
		return nil
	}
	if _, ok := i.scope.descendantIDs[tenantID]; ok {
		return nil
	}
	return ErrAdminForbidden
}
