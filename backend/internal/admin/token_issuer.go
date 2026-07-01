// AdminTokenIssuer 负责签发 / 列举 / 吊销 admin token(运维凭证),
// 区别于 KeyIssuer(后者签发的是客户 api_keys 行)。
//
// 流水线(签发):
//
//	RBAC 检查(只 platform_admin)-> 生成 hk_admin_<...> bearer + bcrypt hash
//	-> TX:
//	   1. InsertAdminToken(只存 hash + prefix,不存明文)
//	   2. InsertAdminAuditEvent(action='issue_admin_token')
//	-> commit -> 一次性返回 TokenIssueResult{Plaintext, ...}。
//
// 安全不变量(对应 CLAUDE.md §4 secret-mask):
//   - 签发 admin token 是高权操作,只有 platform_admin 能调;tenant_operator
//     一律 ErrAdminForbidden。身份取自鉴权上下文(Caller),绝不信 body。
//   - 明文 bearer 只在 TokenIssueResult.Plaintext 中出现一次,绝不入库、
//     绝不记日志、绝不写进 admin_audit_events.payload。
//   - 列举只返元数据(prefix/role/状态/时间),绝不返明文或 hash。
//
// 仅写入 admin_tokens + admin_audit_events,不变更 billing/pool/registry。

package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// timePtrFromPg 把 pgtype.Timestamptz 转回可选的 *time.Time(NULL -> nil),
// 供 LIST 元数据投影使用。统一返回 UTC。
func timePtrFromPg(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	u := ts.Time.UTC()
	return &u
}

// TokenIssueRequest 是 AdminTokenIssuer.IssueToken 的输入。Caller 是已解析
// 的 admin 身份(取自鉴权上下文,而非请求 body)。
type TokenIssueRequest struct {
	Caller AdminIdentity
	// Role 是新 admin token 的角色:RolePlatformAdmin 或 RoleTenantOperator。
	Role string
	// ScopeTenantID 仅当 Role==RoleTenantOperator 时必填(且必须 >0);
	// platform_admin token 必须无 tenant scope(对应 admin_tokens 的
	// scope_tenant_consistency CHECK 约束)。
	ScopeTenantID *int64
	// ExpiresAt 可选:nil = 永久;给值 = 临时/一次性 token,到期后会被
	// resolver 拒绝。
	ExpiresAt *time.Time
	Note      string // 写入 audit 的 reason,也用作 token 的 name 备注
	Name      string // token 的人类可读标签;空则回退为 Note
	RequestID string // 由 chi middleware 设置;记入 audit
}

// TokenIssueResult 是签发返回值。Plaintext 仅在 HTTP 响应中展示一次;
// 绝不记日志,绝不持久化。
type TokenIssueResult struct {
	TokenID   int64
	Plaintext string // 机密 —— 被 String() 省略
	KeyPrefix string
	Role      string
	Status    string
	ExpiresAt *time.Time
	CreatedAt time.Time
}

// String 对 Plaintext 做脱敏,避免意外的 fmt.Printf("%v", res) 把 bearer
// 泄露进日志。
func (r TokenIssueResult) String() string {
	redacted := "<redacted>"
	if r.Plaintext == "" {
		redacted = "<empty>"
	}
	return fmt.Sprintf("TokenIssueResult{TokenID:%d KeyPrefix:%q Plaintext:%s Role:%q Status:%q}",
		r.TokenID, r.KeyPrefix, redacted, r.Role, r.Status)
}

// TokenRevokeRequest 捕获运维的 admin token 吊销调用。
type TokenRevokeRequest struct {
	Caller    AdminIdentity
	TokenID   int64
	Reason    string
	RequestID string
}

// TokenRevokeResult 告诉 handler 发生了什么。AlreadyRevoked=true 表示该
// token 在调用前就已不处于 active(幂等)。
type TokenRevokeResult struct {
	TokenID        int64
	AlreadyRevoked bool
}

// AdminTokenIssuer 铸造 / 吊销 admin_tokens 行。通过 NewAdminTokenIssuer 构造。
type AdminTokenIssuer struct {
	pool       *pgxpool.Pool
	bcryptCost int
}

// NewAdminTokenIssuer 包装一个 pgxpool。bcrypt cost 与客户 key 一致(10)。
func NewAdminTokenIssuer(pool *pgxpool.Pool) *AdminTokenIssuer {
	return &AdminTokenIssuer{
		pool:       pool,
		bcryptCost: bcrypt.DefaultCost,
	}
}

// requirePlatformAdmin 是签发 / 吊销 / 列举 admin token 的统一前置守卫。
// 签发 admin token 是高权操作 —— 只有 platform_admin 能调。tenant_operator
// 一律拒绝(fail-closed:未知角色也拒绝)。
func requirePlatformAdmin(caller AdminIdentity) error {
	if caller.Role != RolePlatformAdmin {
		return ErrAdminForbidden
	}
	return nil
}

// IssueToken 运行完整的签发流水线。返回 ErrAdminForbidden(非 platform_admin)/
// ErrAdminBadRequest(角色或 scope 非法)/ ErrAdminBackend(数据存储故障)。
func (t *AdminTokenIssuer) IssueToken(ctx context.Context, req TokenIssueRequest) (TokenIssueResult, error) {
	if t == nil || t.pool == nil {
		return TokenIssueResult{}, fmt.Errorf("%w: token issuer not configured", ErrAdminBackend)
	}

	// 高权 RBAC 前置且 fail-closed:必须在任何 bearer 生成 / 写库之前。
	if err := requirePlatformAdmin(req.Caller); err != nil {
		_ = t.auditDeny(ctx, req.Caller, "issue_admin_token", req.RequestID, "rbac_violation")
		return TokenIssueResult{}, err
	}

	// 校验目标角色 + scope 一致性(对应 admin_tokens 的
	// scope_tenant_consistency CHECK)。在生成昂贵的 bcrypt hash 之前拒绝
	// 结构性非法输入。
	switch req.Role {
	case RolePlatformAdmin:
		if req.ScopeTenantID != nil {
			return TokenIssueResult{}, fmt.Errorf("%w: platform_admin token must not carry a tenant scope", ErrAdminBadRequest)
		}
	case RoleTenantOperator:
		if req.ScopeTenantID == nil || *req.ScopeTenantID <= 0 {
			return TokenIssueResult{}, fmt.Errorf("%w: tenant_operator token requires a positive scope tenant_id", ErrAdminBadRequest)
		}
	default:
		return TokenIssueResult{}, fmt.Errorf("%w: role must be platform_admin or tenant_operator", ErrAdminBadRequest)
	}

	// 拒绝已经过期的请求:否则会铸出一个 token + 明文 bearer,而 resolver
	// 会在下一次请求时立刻拒绝它。
	if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
		return TokenIssueResult{}, fmt.Errorf("%w: expires_at must be strictly in the future", ErrAdminBadRequest)
	}

	// 生成 bearer + hash(在 TX 之前,避免慢速 bcrypt 持有行锁)。
	bearer, prefix, err := GenerateBearer(EnvAdmin)
	if err != nil {
		return TokenIssueResult{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(bearer), t.bcryptCost)
	if err != nil {
		return TokenIssueResult{}, fmt.Errorf("%w: bcrypt: %v", ErrAdminBackend, err)
	}

	name := req.Name
	if name == "" {
		name = req.Note
	}
	if name == "" {
		name = "admin-token"
	}

	out := TokenIssueResult{KeyPrefix: prefix, Role: req.Role, Status: "active"}
	err = t.tx(ctx, func(qtx *admindb.Queries) error {
		id, err := qtx.InsertAdminToken(ctx, admindb.InsertAdminTokenParams{
			Name:          name,
			KeyHash:       string(hash),
			KeyPrefix:     prefix,
			Role:          req.Role,
			ScopeTenantID: req.ScopeTenantID,
			Bootstrap:     false,
			ExpiresAt:     pgTimestampPtr(req.ExpiresAt),
		})
		if err != nil {
			return fmt.Errorf("%w: insert admin_token: %v", ErrAdminBackend, err)
		}
		out.TokenID = id

		// 审计 payload【绝不】含明文 bearer 或 hash —— 只放 prefix + 元数据。
		payload, _ := json.Marshal(map[string]any{
			"key_prefix":      prefix,
			"role":            req.Role,
			"scope_tenant_id": req.ScopeTenantID,
			"has_expiry":      req.ExpiresAt != nil,
		})
		if _, err := qtx.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID:   req.ScopeTenantID,
			ActorID:    req.Caller.AuditActor(),
			ActorRole:  req.Caller.Role,
			Action:     "issue_admin_token",
			TargetType: "admin_token",
			TargetID:   nullableInt64(id),
			RequestID:  nullableString(req.RequestID),
			Reason:     nullableString(req.Note),
			Payload:    payload,
		}); err != nil {
			return fmt.Errorf("%w: insert audit: %v", ErrAdminBackend, err)
		}
		return nil
	})
	if err != nil {
		return TokenIssueResult{}, err
	}

	// CreatedAt:用 now() 近似(InsertAdminToken 只 RETURNING id)。这是
	// 展示用元数据,无安全语义;权威值由后续 LIST 从 DB 读取。
	out.CreatedAt = time.Now().UTC()
	out.Plaintext = bearer
	out.ExpiresAt = req.ExpiresAt
	return out, nil
}

// RevokeToken 把某个 admin token 的 status 翻转为 'revoked'。RBAC:只
// platform_admin。幂等:吊销一个非 active 的 token 返回 AlreadyRevoked=true。
func (t *AdminTokenIssuer) RevokeToken(ctx context.Context, req TokenRevokeRequest) (TokenRevokeResult, error) {
	if t == nil || t.pool == nil {
		return TokenRevokeResult{}, fmt.Errorf("%w: token issuer not configured", ErrAdminBackend)
	}
	if req.TokenID <= 0 {
		return TokenRevokeResult{}, fmt.Errorf("%w: token id required", ErrAdminBadRequest)
	}
	if err := requirePlatformAdmin(req.Caller); err != nil {
		_ = t.auditDeny(ctx, req.Caller, "revoke_admin_token", req.RequestID, "rbac_violation")
		return TokenRevokeResult{}, err
	}

	out := TokenRevokeResult{TokenID: req.TokenID}
	err := t.tx(ctx, func(qtx *admindb.Queries) error {
		// 先核实 token 存在(未软删除);缺失 -> 404。
		row, err := qtx.GetAdminTokenByID(ctx, req.TokenID)
		if err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("%w: admin_token %d", ErrAdminNotFound, req.TokenID)
			}
			return fmt.Errorf("%w: get admin_token: %v", ErrAdminBackend, err)
		}
		if row.Status != "active" {
			out.AlreadyRevoked = true
		} else {
			rows, err := qtx.RevokeAdminToken(ctx, admindb.RevokeAdminTokenParams{
				ID:     req.TokenID,
				Reason: req.Reason,
			})
			if err != nil {
				return fmt.Errorf("%w: revoke admin_token: %v", ErrAdminBackend, err)
			}
			if rows == 0 {
				// 竞态:SELECT 与 UPDATE 之间被改成非 active。按幂等处理。
				out.AlreadyRevoked = true
			}
		}

		payload, _ := json.Marshal(map[string]any{
			"token_id":        req.TokenID,
			"already_revoked": out.AlreadyRevoked,
		})
		if _, err := qtx.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID:   row.ScopeTenantID,
			ActorID:    req.Caller.AuditActor(),
			ActorRole:  req.Caller.Role,
			Action:     "revoke_admin_token",
			TargetType: "admin_token",
			TargetID:   nullableInt64(req.TokenID),
			RequestID:  nullableString(req.RequestID),
			Reason:     nullableString(req.Reason),
			Payload:    payload,
		}); err != nil {
			return fmt.Errorf("%w: insert audit: %v", ErrAdminBackend, err)
		}
		return nil
	})
	if err != nil {
		return TokenRevokeResult{}, err
	}
	return out, nil
}

// TokenListItem 是 ListTokens 的单条元数据 —— 绝不含明文或 hash。
type TokenListItem struct {
	ID            int64
	Name          string
	KeyPrefix     string
	Role          string
	ScopeTenantID *int64
	Bootstrap     bool
	Status        string
	ExpiresAt     *time.Time
	LastUsedAt    *time.Time
	RevokedAt     *time.Time
	RevokedReason *string
	CreatedAt     time.Time
}

// ListTokens 返回 admin token 的元数据分页。RBAC:只 platform_admin。
// 绝不返回明文 bearer 或 key_hash(查询本身也不 SELECT key_hash)。
func (t *AdminTokenIssuer) ListTokens(ctx context.Context, caller AdminIdentity, limit, offset int32) ([]TokenListItem, error) {
	if t == nil || t.pool == nil {
		return nil, fmt.Errorf("%w: token issuer not configured", ErrAdminBackend)
	}
	if err := requirePlatformAdmin(caller); err != nil {
		return nil, err
	}
	q := admindb.New(t.pool)
	rows, err := q.ListAdminTokens(ctx, admindb.ListAdminTokensParams{
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: list admin_tokens: %v", ErrAdminBackend, err)
	}
	items := make([]TokenListItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, TokenListItem{
			ID:            r.ID,
			Name:          r.Name,
			KeyPrefix:     r.KeyPrefix,
			Role:          r.Role,
			ScopeTenantID: r.ScopeTenantID,
			Bootstrap:     r.Bootstrap,
			Status:        r.Status,
			ExpiresAt:     timePtrFromPg(r.ExpiresAt),
			LastUsedAt:    timePtrFromPg(r.LastUsedAt),
			RevokedAt:     timePtrFromPg(r.RevokedAt),
			RevokedReason: r.RevokedReason,
			CreatedAt:     r.CreatedAt.Time,
		})
	}
	return items, nil
}

// auditDeny 在任何 TX 之外写一条被拒绝路径的 admin token audit 行。
// tenant_id 始终为 NULL(拒绝时调用方可能针对任意 scope),被尝试的细节
// 留在 payload 中供取证。best-effort:吞掉 insert 错误。
func (t *AdminTokenIssuer) auditDeny(ctx context.Context, caller AdminIdentity, action, requestID, reason string) error {
	q := admindb.New(t.pool)
	payload, _ := json.Marshal(map[string]any{
		"outcome": "denied",
		"reason":  reason,
	})
	actorRole := caller.Role
	if actorRole == "" {
		// admin_audit_events.actor_role 有 CHECK 约束;未知角色回退为
		// tenant_operator,避免拒绝事件因约束违例而丢失。
		actorRole = RoleTenantOperator
	}
	_, err := q.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID:   nil,
		ActorID:    caller.AuditActor(),
		ActorRole:  actorRole,
		Action:     action,
		TargetType: "admin_token",
		RequestID:  nullableString(requestID),
		Reason:     nullableString(reason),
		Payload:    payload,
	})
	return err
}

// tx 在一个全新事务内运行 fn(与 KeyIssuer.tx 形态一致)。
func (t *AdminTokenIssuer) tx(ctx context.Context, fn func(*admindb.Queries) error) error {
	tx, err := t.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("%w: begin: %v", ErrAdminBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(admindb.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit: %v", ErrAdminBackend, err)
	}
	return nil
}
