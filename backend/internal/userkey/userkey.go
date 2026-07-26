// Package userkey 提供 session 认证用户**自助**管理 api_keys 行的服务。
//
// 与 internal/admin 的区别:
//   - admin 包是 operator 视角:caller 是 platform_admin / tenant_operator,
//     可越用户给一个 tenant 内任意 user 签发 / 撤销 key,审计落 admin_audit_events。
//   - 本包是 end user 视角:caller 是 SessionIdentity (TenantID, UserID),
//     只能操作**自己** UserID 下的 key;审计走 slog structured log
//     (durable user_audit_events 表升级见 RR-W5-009)。
//
// 与 internal/auth 的区别:
//   - auth.APIKeyResolver 是 inbound 校验路径 (LookupAPIKeysByPrefix + bcrypt),
//     写不到 api_keys 表;只读 + 不审计。
//   - 本包是写路径 (INSERT / UPDATE / 列表),与 inbound 校验互不重叠。
//
// 信任链承诺 ([[project_core_trust_chain_differentiator]]):用户拿到的 bearer
// 明文**只在 POST 响应里出现一次**,后续 GET 永不返回明文,任何代码路径
// 都不许把 plaintext 写日志/写表 (复用 admin.IssueResult.String 模式)。
package userkey

import (
	"github.com/BloomingProsperity/HUAKAI/internal/textsafe"

	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/tenancy"
	"github.com/BloomingProsperity/HUAKAI/internal/userauditlog"
)

// Environment 复用 admin 包枚举,但拒 EnvAdmin (用户绝不应签发 hk_admin_ 前缀)。
type Environment = admin.Environment

const (
	EnvLive = admin.EnvLive
	EnvTest = admin.EnvTest
)

// MaxActiveKeysPerUser 单个 user 同时持有的 active api_keys 行数上限。
// 设计:防滥用 (用户脚本 bug 无限创建) + 操作面友好 (用户实际不会有 20 个并发用途);
// 升级到 SaaS 多租户时按计划再可配置 (RR-W5-009 跟踪)。
const MaxActiveKeysPerUser = 20

// MaxNameLen 单 key name 最大长度;长度超界 → ErrInvalidName。
const MaxNameLen = 128

// PageLimitMax / PageLimitDefault 分页边界。
const (
	PageLimitDefault = 50
	PageLimitMax     = 200
)

// 错误。
var (
	ErrInvalidName      = errors.New("userkey: name invalid")
	ErrInvalidExpiry    = errors.New("userkey: expires_at must be future")
	ErrInvalidEnv       = errors.New("userkey: environment must be live or test")
	ErrInvalidStatus    = errors.New("userkey: status must be active or revoked")
	ErrActiveKeyCapHit  = errors.New("userkey: user has reached active key cap")
	ErrNotFound         = errors.New("userkey: api_key not found for owner")
	ErrAlreadyRevoked   = errors.New("userkey: revoked api_key cannot be reactivated")
	ErrStatusManaged    = errors.New("userkey: api_key status is managed by an operator")
	ErrServiceMisconfig = errors.New("userkey: service not configured")
	ErrBackend          = errors.New("userkey: backend datastore error")
)

// IssueRequest 用户签发自己 key 的入参。
//
// TenantID / UserID 由 caller (handler) 从 SessionIdentity 直接填,不允许 body 覆盖。
type IssueRequest struct {
	TenantID    int64
	UserID      int64
	Name        string
	Environment Environment // 缺省 EnvLive
	ExpiresAt   *time.Time  // nil = 永不过期
	RequestID   string      // 用于 slog audit log
}

// IssueResult 签发结果。Plaintext 只此一次出现;后续 List / Get 仅返回 KeyPrefix。
type IssueResult struct {
	APIKeyID  int64
	Plaintext string // SECRET — String() 自动脱敏
	KeyPrefix string
	Status    string
	ExpiresAt *time.Time
	CreatedAt time.Time
}

// String 复用 admin.IssueResult 的脱敏模式,防 fmt.Printf("%v", res) 泄露明文。
func (r IssueResult) String() string {
	plaintext := "<redacted>"
	if r.Plaintext == "" {
		plaintext = "<empty>"
	}
	return fmt.Sprintf("userkey.IssueResult{APIKeyID:%d KeyPrefix:%q Plaintext:%s Status:%q}",
		r.APIKeyID, r.KeyPrefix, plaintext, r.Status)
}

// KeyDescriptor List / Get 视图。绝不含 KeyHash 或 Plaintext。
type KeyDescriptor struct {
	APIKeyID      int64      `json:"api_key_id"`
	Name          string     `json:"name"`
	KeyPrefix     string     `json:"key_prefix"`
	Status        string     `json:"status"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	RevokedReason string     `json:"revoked_reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// RevokeRequest 用户撤销自己 key 的入参。
type RevokeRequest struct {
	TenantID  int64
	UserID    int64
	APIKeyID  int64
	Reason    string
	RequestID string
}

// RevokeResult 撤销结果。AlreadyRevoked=true 表示幂等命中。
type RevokeResult struct {
	APIKeyID       int64
	AlreadyRevoked bool
}

// ListRequest 列出当前 session user 名下所有 api_keys (含已 revoked,便于审计回溯)。
// Offset / Limit 为分页参数;handler 层 sanitize 后传入。
type ListRequest struct {
	TenantID int64
	UserID   int64
	Offset   int
	Limit    int
}

// Service 是用户自助 api_keys CRUD 的入口。
//
// 三件事:
//   - Issue: 生成 bearer + 写 api_keys 行 + 写 slog audit
//   - List: 按 (tenant_id, user_id) 列出 caller 自己的所有 key (含 revoked)
//   - Get: 按 (tenant_id, user_id, id) 取单条;不归属 caller → ErrNotFound
//   - Revoke: 按 (tenant_id, user_id, id) 撤销;不归属 → ErrNotFound,已撤销 → 幂等
type Service struct {
	pool            *pgxpool.Pool
	logger          *slog.Logger
	auditSink       userauditlog.UserAuditSink
	bcryptCost      int
	now             func() time.Time
	defaultKeyQuota defaultKeyQuotaConfig
}

type Option func(*Service)

func WithAuditSink(sink userauditlog.UserAuditSink) Option {
	return func(s *Service) {
		if sink != nil {
			s.auditSink = sink
		}
	}
}

// NewService 构造。pool 必填;logger nil 则用 slog.Default;now nil 则用 time.Now。
//
// bcryptCost 复用 admin.KeyIssuer 默认 (cost 10),与 inbound resolver 解析时
// 的 bcrypt.CompareHashAndPassword 配套。
func NewService(pool *pgxpool.Pool, logger *slog.Logger, opts ...Option) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{
		pool:       pool,
		logger:     logger,
		auditSink:  userauditlog.NoopSink{},
		bcryptCost: bcrypt.DefaultCost,
		now:        time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Issue 给当前 session user 签发一条新 api_keys 行。返回明文一次。
//
// 流程:
//  1. 参数校验 (Name 非空且 ≤ MaxNameLen,Environment 合法,ExpiresAt 未来时刻)
//  2. 生成 bearer + bcrypt hash (重 CPU,放 TX 外避免持锁)
//  3. BeginTx:
//     a. 数当前 active 数,≥ MaxActiveKeysPerUser → ErrActiveKeyCapHit
//     b. INSERT api_keys (条件 EXISTS tenant + user 都 active)
//     c. NoRows → tenant/user 失效 → ErrNotFound (映射 400 缺 user/tenant)
//  4. slog INFO audit (action=issue, outcome=committed, key_id/key_prefix/tenant/user)
//  5. 返回 IssueResult + Plaintext
//
// 失败路径:slog WARN audit (action=issue, outcome=denied/error, reason)。
func (s *Service) Issue(ctx context.Context, req IssueRequest) (IssueResult, error) {
	if s == nil || s.pool == nil {
		return IssueResult{}, fmt.Errorf("%w: pool unset", ErrServiceMisconfig)
	}
	if req.TenantID <= 0 || req.UserID <= 0 {
		return IssueResult{}, fmt.Errorf("%w: tenant_id / user_id required", ErrInvalidName)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > MaxNameLen {
		s.logIssue(req, "denied", "invalid_name", 0, "")
		return IssueResult{}, ErrInvalidName
	}
	env := req.Environment
	if env == "" {
		env = EnvLive
	}
	if env != EnvLive && env != EnvTest {
		s.logIssue(req, "denied", "invalid_env", 0, "")
		return IssueResult{}, ErrInvalidEnv
	}
	now := s.now().UTC()
	if req.ExpiresAt != nil && !req.ExpiresAt.After(now) {
		s.logIssue(req, "denied", "expires_in_past", 0, "")
		return IssueResult{}, ErrInvalidExpiry
	}
	if err := s.requireActiveFinalUser(ctx, req.TenantID, req.UserID); err != nil {
		s.logIssue(req, "denied", "tenant_or_final_user_inactive", 0, "")
		return IssueResult{}, err
	}

	bearer, prefix, err := admin.GenerateBearer(env)
	if err != nil {
		s.logIssue(req, "error", "bearer_gen_failed", 0, "")
		return IssueResult{}, fmt.Errorf("%w: bearer: %v", ErrBackend, err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(bearer), s.bcryptCost)
	if err != nil {
		s.logIssue(req, "error", "bcrypt_failed", 0, "")
		return IssueResult{}, fmt.Errorf("%w: bcrypt: %v", ErrBackend, err)
	}

	out := IssueResult{KeyPrefix: prefix, Status: "active", ExpiresAt: req.ExpiresAt}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if err := tenancy.LockActiveForWrite(ctx, tx, req.TenantID); err != nil {
			if errors.Is(err, tenancy.ErrTenantInactive) {
				return fmt.Errorf("%w: tenant inactive", ErrNotFound)
			}
			return fmt.Errorf("%w: lock tenant: %v", ErrBackend, err)
		}
		// 拿 (tenant_id, user_id) 的 transaction-scoped advisory lock,保证并发 Issue
		// 不会同时读到 cap 之下的 count 再各自 INSERT 越界。
		// PostgreSQL pg_advisory_xact_lock 单 bigint 签名;Go 端把 (tenant, user)
		// 拼成 text 喂 hashtextextended,tx 结束自动释放。
		lockKey := fmt.Sprintf("userkey:%d:%d", req.TenantID, req.UserID)
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`,
			lockKey,
		); err != nil {
			return fmt.Errorf("%w: advisory lock: %v", ErrBackend, err)
		}
		var activeCount int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM api_keys
				   WHERE tenant_id = $1 AND user_id = $2
				     AND purpose = 'user' AND status = 'active' AND deleted_at IS NULL
				     AND EXISTS (
				         SELECT 1 FROM users u
				          WHERE u.tenant_id=api_keys.tenant_id AND u.id=api_keys.user_id
				            AND u.principal_kind='human' AND u.role='user'
				            AND u.status='active' AND u.deleted_at IS NULL)`,
			req.TenantID, req.UserID,
		).Scan(&activeCount); err != nil {
			return fmt.Errorf("%w: count active: %v", ErrBackend, err)
		}
		if activeCount >= MaxActiveKeysPerUser {
			return ErrActiveKeyCapHit
		}

		var (
			id        int64
			createdAt time.Time
		)
		expiresParam := pgtype.Timestamptz{}
		if req.ExpiresAt != nil {
			expiresParam = pgtype.Timestamptz{Time: *req.ExpiresAt, Valid: true}
		}
		err := tx.QueryRow(ctx,
			`WITH eligible_target AS MATERIALIZED (
			     SELECT t.id AS tenant_id, u.id AS user_id
			       FROM tenants t
			       JOIN users u
			         ON u.tenant_id=t.id
			        AND u.id=$2::bigint
			        AND u.principal_kind='human'
			        AND u.role='user'
			        AND u.deleted_at IS NULL
			        AND u.status='active'
			      WHERE t.id=$1::bigint
			        AND t.deleted_at IS NULL
			        AND t.status='active'
			      FOR SHARE OF t, u
			 )
			 INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status, expires_at)
			 SELECT tenant_id, user_id, $3::text, $4::text, $5::text, 'active', $6::timestamptz
			   FROM eligible_target
			 RETURNING id, created_at`,
			req.TenantID, req.UserID, name, string(hash), prefix, expiresParam,
		).Scan(&id, &createdAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: tenant or user inactive", ErrNotFound)
			}
			return fmt.Errorf("%w: insert: %v", ErrBackend, err)
		}
		out.APIKeyID = id
		out.CreatedAt = createdAt
		if err := s.seedDefaultKeyQuota(ctx, tx, req.TenantID, req.UserID, id); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrActiveKeyCapHit):
			s.logIssue(req, "denied", "active_key_cap", 0, prefix)
		case errors.Is(err, ErrNotFound):
			s.logIssue(req, "denied", "tenant_or_user_inactive", 0, prefix)
		default:
			s.logIssue(req, "error", "tx_failed", 0, prefix)
		}
		return IssueResult{}, err
	}
	s.logIssue(req, "committed", "ok", out.APIKeyID, out.KeyPrefix)
	out.Plaintext = bearer
	return out, nil
}

// List 列出 caller 自己名下的 api_keys (含已 revoked,按 created_at DESC)。
//
// Offset / Limit 上层 handler sanitize;此处兜底:Limit 不在 (0, PageLimitMax] →
// 改为 PageLimitDefault;Offset < 0 → 改为 0。
func (s *Service) List(ctx context.Context, req ListRequest) ([]KeyDescriptor, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("%w: pool unset", ErrServiceMisconfig)
	}
	if req.TenantID <= 0 || req.UserID <= 0 {
		return nil, ErrInvalidName
	}
	limit := req.Limit
	if limit <= 0 || limit > PageLimitMax {
		limit = PageLimitDefault
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	// 与 tenants/users 双 JOIN 强制要求 active + 未软删除:
	// 即使有 stale session token 漏过 middleware,失活租户/用户的 key 元数据也读不到。
	rows, err := s.pool.Query(ctx,
		`SELECT k.id, k.name, k.key_prefix, k.status,
		        k.expires_at, k.last_used_at, k.revoked_at, k.revoked_reason,
		        k.created_at, k.updated_at
		   FROM api_keys k
			   JOIN tenants t ON t.id = k.tenant_id AND t.deleted_at IS NULL AND t.status = 'active'
				   JOIN users   u ON u.id = k.user_id   AND u.tenant_id = k.tenant_id
				                 AND u.principal_kind = 'human' AND u.role = 'user' AND u.deleted_at IS NULL AND u.status = 'active'
			  WHERE k.tenant_id = $1 AND k.user_id = $2 AND k.purpose = 'user' AND k.deleted_at IS NULL
		  ORDER BY k.created_at DESC, k.id DESC
		  LIMIT $3 OFFSET $4`,
		req.TenantID, req.UserID, int32(limit), int32(offset),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: list: %v", ErrBackend, err)
	}
	defer rows.Close()
	var out []KeyDescriptor
	for rows.Next() {
		var (
			d             KeyDescriptor
			expiresAt     pgtype.Timestamptz
			lastUsedAt    pgtype.Timestamptz
			revokedAt     pgtype.Timestamptz
			revokedReason *string
			createdAt     pgtype.Timestamptz
			updatedAt     pgtype.Timestamptz
		)
		if err := rows.Scan(&d.APIKeyID, &d.Name, &d.KeyPrefix, &d.Status,
			&expiresAt, &lastUsedAt, &revokedAt, &revokedReason,
			&createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("%w: scan: %v", ErrBackend, err)
		}
		if expiresAt.Valid {
			t := expiresAt.Time
			d.ExpiresAt = &t
		}
		if lastUsedAt.Valid {
			t := lastUsedAt.Time
			d.LastUsedAt = &t
		}
		if revokedAt.Valid {
			t := revokedAt.Time
			d.RevokedAt = &t
		}
		if revokedReason != nil {
			d.RevokedReason = *revokedReason
		}
		if createdAt.Valid {
			d.CreatedAt = createdAt.Time
		}
		if updatedAt.Valid {
			d.UpdatedAt = updatedAt.Time
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: rows: %v", ErrBackend, err)
	}
	return out, nil
}

// Count 返回 caller 自己名下未软删除的全部 api_keys 数量，不受列表分页影响。
// 查询使用与 List 相同的租户、用户有效性约束，避免总数泄漏失活主体的元数据。
func (s *Service) Count(ctx context.Context, tenantID, userID int64) (int, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("%w: pool unset", ErrServiceMisconfig)
	}
	if tenantID <= 0 || userID <= 0 {
		return 0, ErrInvalidName
	}
	var total int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*)
		   FROM api_keys k
			   JOIN tenants t ON t.id = k.tenant_id AND t.deleted_at IS NULL AND t.status = 'active'
				   JOIN users   u ON u.id = k.user_id AND u.tenant_id = k.tenant_id
				                  AND u.principal_kind = 'human' AND u.role = 'user' AND u.deleted_at IS NULL AND u.status = 'active'
			  WHERE k.tenant_id = $1 AND k.user_id = $2 AND k.purpose = 'user' AND k.deleted_at IS NULL`,
		tenantID, userID,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("%w: count: %v", ErrBackend, err)
	}
	return total, nil
}

// Get 取 caller 自己的单条 api_keys。归属不符 → ErrNotFound (不区分"不存在"与
// "别人的",防 ID 枚举泄漏)。
func (s *Service) Get(ctx context.Context, tenantID, userID, apiKeyID int64) (KeyDescriptor, error) {
	if s == nil || s.pool == nil {
		return KeyDescriptor{}, fmt.Errorf("%w: pool unset", ErrServiceMisconfig)
	}
	if tenantID <= 0 || userID <= 0 || apiKeyID <= 0 {
		return KeyDescriptor{}, ErrNotFound
	}
	var (
		d             KeyDescriptor
		expiresAt     pgtype.Timestamptz
		lastUsedAt    pgtype.Timestamptz
		revokedAt     pgtype.Timestamptz
		revokedReason *string
		createdAt     pgtype.Timestamptz
		updatedAt     pgtype.Timestamptz
	)
	// 与 List 同样的双 JOIN 防御:失活 tenant/user 拿不到 key。
	err := s.pool.QueryRow(ctx,
		`SELECT k.id, k.name, k.key_prefix, k.status,
		        k.expires_at, k.last_used_at, k.revoked_at, k.revoked_reason,
		        k.created_at, k.updated_at
		   FROM api_keys k
			   JOIN tenants t ON t.id = k.tenant_id AND t.deleted_at IS NULL AND t.status = 'active'
				   JOIN users   u ON u.id = k.user_id   AND u.tenant_id = k.tenant_id
				                 AND u.principal_kind = 'human' AND u.role = 'user' AND u.deleted_at IS NULL AND u.status = 'active'
			  WHERE k.id = $1 AND k.tenant_id = $2 AND k.user_id = $3 AND k.purpose = 'user' AND k.deleted_at IS NULL`,
		apiKeyID, tenantID, userID,
	).Scan(&d.APIKeyID, &d.Name, &d.KeyPrefix, &d.Status,
		&expiresAt, &lastUsedAt, &revokedAt, &revokedReason,
		&createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return KeyDescriptor{}, ErrNotFound
		}
		return KeyDescriptor{}, fmt.Errorf("%w: get: %v", ErrBackend, err)
	}
	if expiresAt.Valid {
		t := expiresAt.Time
		d.ExpiresAt = &t
	}
	if lastUsedAt.Valid {
		t := lastUsedAt.Time
		d.LastUsedAt = &t
	}
	if revokedAt.Valid {
		t := revokedAt.Time
		d.RevokedAt = &t
	}
	if revokedReason != nil {
		d.RevokedReason = *revokedReason
	}
	if createdAt.Valid {
		d.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		d.UpdatedAt = updatedAt.Time
	}
	return d, nil
}

// Revoke 撤销 caller 自己的 api_key。
//
// 安全语义:WHERE 子句强制 (id, tenant_id, user_id) 三元组匹配 — 别的 user 的
// key 用 caller 的 (tenant, user) 永查不到 → ErrNotFound (与"不存在"同响应);
// 防 ID 枚举泄漏其他 user 的 key 存在性。
func (s *Service) Revoke(ctx context.Context, req RevokeRequest) (RevokeResult, error) {
	if s == nil || s.pool == nil {
		return RevokeResult{}, fmt.Errorf("%w: pool unset", ErrServiceMisconfig)
	}
	if req.TenantID <= 0 || req.UserID <= 0 || req.APIKeyID <= 0 {
		return RevokeResult{}, ErrNotFound
	}
	// rune 安全截断:裸 reason[:256] 切半中文 → PG 22021 → 吊销事务整体失败,
	// 泄露的 key 反而杀不掉(delta-mine #2)。
	reason := textsafe.TruncateBytes(strings.TrimSpace(req.Reason), 256)
	out := RevokeResult{APIKeyID: req.APIKeyID}
	keyPrefix := ""
	err := s.tx(ctx, func(tx pgx.Tx) error {
		// 单点 owner+active-parent 校验:JOIN tenants/users 强制 active,
		// SELECT FOR UPDATE 锁住 row 避免并发 Revoke 竞态。
		// 移除 UPDATE 的 user_id 冗余条件后,任何 ownership / parent-active 检查
		// 缺失都会让这唯一 gate 失守,mutation 自检有判别力。
		var status string
		err := tx.QueryRow(ctx,
			`SELECT k.status, k.key_prefix FROM api_keys k
					   JOIN tenants t ON t.id = k.tenant_id AND t.deleted_at IS NULL AND t.status = 'active'
					   JOIN users   u ON u.id = k.user_id   AND u.tenant_id = k.tenant_id
					                 AND u.principal_kind = 'human' AND u.role = 'user' AND u.deleted_at IS NULL AND u.status = 'active'
				  WHERE k.id = $1 AND k.tenant_id = $2 AND k.user_id = $3 AND k.purpose = 'user' AND k.deleted_at IS NULL
			  FOR UPDATE OF k
			  FOR SHARE OF t, u`,
			req.APIKeyID, req.TenantID, req.UserID,
		).Scan(&status, &keyPrefix)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("%w: get: %v", ErrBackend, err)
		}
		if status == "revoked" {
			out.AlreadyRevoked = true
			return nil
		}
		// 已通过 SELECT FOR UPDATE 锁住正确归属的 row;UPDATE 只匹配 id 不再带
		// user_id 冗余条件,真单点 gate (移除上面 SELECT 的 user_id 子句 → 任何 caller
		// 都能 SELECT 到别人的 row → integration_pg 测试变红)。
		cmd, err := tx.Exec(ctx,
			`UPDATE api_keys
				    SET status = 'revoked', revoked_at = NOW(), revoked_reason = $1::text, updated_at = NOW()
				  WHERE id = $2 AND purpose = 'user' AND status <> 'revoked' AND deleted_at IS NULL`,
			reason, req.APIKeyID,
		)
		if err != nil {
			return fmt.Errorf("%w: revoke: %v", ErrBackend, err)
		}
		if cmd.RowsAffected() == 0 {
			out.AlreadyRevoked = true
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.logRevoke(req, "denied", "not_found_for_owner", false, "")
			return RevokeResult{}, err
		}
		s.logRevoke(req, "error", "tx_failed", false, keyPrefix)
		return RevokeResult{}, err
	}
	s.logRevoke(req, "committed", "ok", out.AlreadyRevoked, keyPrefix)
	return out, nil
}

func (s *Service) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("%w: begin: %v", ErrBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit: %v", ErrBackend, err)
	}
	return nil
}

// logIssue / logRevoke 写 structured slog;不含 plaintext / key_hash。
//
// RR-W5-009 跟踪未来升级到 durable user_self_audit_events 表 (schema gate)。
func (s *Service) logIssue(req IssueRequest, outcome, reason string, apiKeyID int64, prefix string) {
	level := slog.LevelInfo
	if outcome != "committed" {
		level = slog.LevelWarn
	}
	s.logger.Log(context.Background(), level, "userkey.issue",
		slog.String("action", "issue_api_key"),
		slog.String("outcome", outcome),
		slog.String("reason", reason),
		slog.Int64("tenant_id", req.TenantID),
		slog.Int64("user_id", req.UserID),
		slog.Int64("api_key_id", apiKeyID),
		slog.String("key_prefix", prefix),
		slog.String("request_id", req.RequestID),
	)
	s.recordAudit(context.Background(), userauditlog.Event{
		TenantID:  req.TenantID,
		UserID:    req.UserID,
		Action:    userauditlog.ActionIssueAPIKey,
		Outcome:   outcome,
		APIKeyID:  auditAPIKeyID(apiKeyID),
		KeyPrefix: prefix,
		Reason:    reason,
		RequestID: req.RequestID,
	})
}

func (s *Service) logRevoke(req RevokeRequest, outcome, reason string, alreadyRevoked bool, prefix string) {
	level := slog.LevelInfo
	if outcome != "committed" {
		level = slog.LevelWarn
	}
	s.logger.Log(context.Background(), level, "userkey.revoke",
		slog.String("action", "revoke_api_key"),
		slog.String("outcome", outcome),
		slog.String("reason", reason),
		slog.Int64("tenant_id", req.TenantID),
		slog.Int64("user_id", req.UserID),
		slog.Int64("api_key_id", req.APIKeyID),
		slog.Bool("already_revoked", alreadyRevoked),
		slog.String("key_prefix", prefix),
		slog.String("request_id", req.RequestID),
	)
	s.recordAudit(context.Background(), userauditlog.Event{
		TenantID:  req.TenantID,
		UserID:    req.UserID,
		Action:    userauditlog.ActionRevokeAPIKey,
		Outcome:   outcome,
		APIKeyID:  auditAPIKeyID(req.APIKeyID),
		KeyPrefix: prefix,
		Reason:    reason,
		RequestID: req.RequestID,
	})
}

func (s *Service) recordAudit(ctx context.Context, event userauditlog.Event) {
	if s == nil || s.auditSink == nil {
		return
	}
	if err := s.auditSink.Record(ctx, event); err != nil && s.logger != nil {
		s.logger.WarnContext(ctx, "userkey.audit_sink_failed",
			slog.String("action", event.Action),
			slog.String("outcome", event.Outcome),
			slog.String("reason", event.Reason),
			slog.Int64("tenant_id", event.TenantID),
			slog.Int64("user_id", event.UserID),
			slog.String("request_id", event.RequestID),
			slog.String("error", err.Error()),
		)
	}
}

func auditAPIKeyID(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}
