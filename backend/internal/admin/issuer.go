// KeyIssuer 代表一个已认证的运维创建新的 api_keys 行。流水线:
//
//	RBAC 检查 -> 速率限制检查 -> 生成 bearer + bcrypt hash
//	-> 开启 TX:
//	   1. AdminInsertAPIKey
//	   2. InsertAdminAuditEvent (action='issue_api_key')
//	-> commit -> 一次性返回 IssueResult{Plaintext, ...}。
//
// IssueResult.Plaintext 是明文唯一出现的地方。
// audit payload 在写入时【不含】明文或 hash。调用方在把 IssueResult
// 交给 HTTP 响应后【绝不可】再记日志。
//
// 仅写入 api_keys + admin_audit_events。不变更 billing/pool/registry。

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// IssueRequest 是 KeyIssuer.Issue 的输入。Caller 是已解析的 admin 身份。
// TenantID/UserID 是新 api_keys 行将归属的目标 tenant + 终端用户。
type IssueRequest struct {
	Caller      AdminIdentity
	TenantID    int64
	UserID      int64
	Name        string
	Environment Environment // EnvLive 或 EnvTest;EnvAdmin 会被拒绝
	ExpiresAt   *time.Time
	Reason      string
	RequestID   string // 由 chi middleware 设置;记入 audit
}

// IssueResult 是 issuer 的返回值。Plaintext 仅在 HTTP 响应中展示一次;
// 绝不记日志,绝不持久化。
type IssueResult struct {
	APIKeyID  int64
	Plaintext string // 机密 —— 被 String() 省略
	KeyPrefix string
	Status    string
	ExpiresAt *time.Time
	CreatedAt time.Time
}

// String 对 Plaintext 做脱敏,这样意外的 fmt.Printf("%v", res) 不会把
// bearer 泄露进日志。
func (r IssueResult) String() string {
	plaintextRedacted := "<redacted>"
	if r.Plaintext == "" {
		plaintextRedacted = "<empty>"
	}
	return fmt.Sprintf("IssueResult{APIKeyID:%d KeyPrefix:%q Plaintext:%s Status:%q}",
		r.APIKeyID, r.KeyPrefix, plaintextRedacted, r.Status)
}

// KeyIssuer 铸造 api_keys 行。通过 NewKeyIssuer 构造。
type KeyIssuer struct {
	pool                *pgxpool.Pool
	bcryptCost          int
	rateLimitPerHour    int
	rateLimitWindowSecs int
}

// NewKeyIssuer 包装一个 pgxpool。默认值:bcrypt cost 10(与客户 key 一致)、
// 每 actor 30 次签发/小时(D4)。
func NewKeyIssuer(pool *pgxpool.Pool) *KeyIssuer {
	return &KeyIssuer{
		pool:                pool,
		bcryptCost:          bcrypt.DefaultCost,
		rateLimitPerHour:    30,
		rateLimitWindowSecs: 3600,
	}
}

// Issue 运行完整的签发流水线。依据综合方案 §D1+D4 返回 ErrAdminUnauthorized /
// ErrAdminForbidden / ErrAdminRateLimited / ErrAdminBadRequest /
// ErrAdminBackend。
func (i *KeyIssuer) Issue(ctx context.Context, req IssueRequest) (IssueResult, error) {
	if i == nil || i.pool == nil {
		return IssueResult{}, fmt.Errorf("%w: issuer not configured", ErrAdminBackend)
	}
	if req.Name == "" || req.TenantID == 0 || req.UserID == 0 {
		return IssueResult{}, fmt.Errorf("%w: name, tenant_id, user_id required", ErrAdminBadRequest)
	}
	if req.Environment != EnvLive && req.Environment != EnvTest {
		return IssueResult{}, fmt.Errorf("%w: environment must be live or test", ErrAdminBadRequest)
	}

	// RBAC。
	if err := req.Caller.CanIssueForTenant(req.TenantID); err != nil {
		_ = i.audit(ctx, req, "denied", "rbac_violation", 0)
		return IssueResult{}, err
	}

	// 在铸造 bearer【之前】校验目标 tenant + user 处于 active 且未被
	// 软删除。否则一个非法目标要么表现为 503(FK 违例被包装成
	// ErrAdminBackend),要么 —— 更糟 —— 表现为一把完美签发的 key,
	// 却因软删除状态在下一次请求时被客户 resolver 拒绝。
	{
		q := admindb.New(i.pool)
		check, err := q.AdminCheckIssuanceTarget(ctx, admindb.AdminCheckIssuanceTargetParams{
			TenantID: req.TenantID,
			UserID:   req.UserID,
		})
		if err != nil {
			return IssueResult{}, fmt.Errorf("%w: validate target: %v", ErrAdminBackend, err)
		}
		if !check.TenantOk {
			_ = i.audit(ctx, req, "denied", "tenant_inactive_or_missing", 0)
			return IssueResult{}, fmt.Errorf("%w: target tenant inactive or missing", ErrAdminBadRequest)
		}
		if !check.UserOk {
			_ = i.audit(ctx, req, "denied", "user_inactive_or_missing", 0)
			return IssueResult{}, fmt.Errorf("%w: target user inactive or missing for tenant", ErrAdminBadRequest)
		}
	}

	// 在 bcrypt【之前】做一次廉价的预检速率限制检查,这样一个超额的
	// actor 无法让每个垃圾请求都烧掉 cost-10 的 hash CPU。权威的原子检查
	// 仍然在 TX 内、持有 per-actor advisory lock 时运行 —— 此预检为
	// best-effort。
	{
		q := admindb.New(i.pool)
		count, err := q.CountIssuanceInWindow(ctx, admindb.CountIssuanceInWindowParams{
			ActorID:       req.Caller.AuditActor(),
			WindowSeconds: int32(i.rateLimitWindowSecs),
			LegacyActorID: legacyActorKey(req.Caller),
		})
		if err != nil {
			return IssueResult{}, fmt.Errorf("%w: rate-limit preflight: %v", ErrAdminBackend, err)
		}
		if int(count) >= i.rateLimitPerHour {
			_ = i.audit(ctx, req, "denied", "rate_limited", 0)
			return IssueResult{}, ErrAdminRateLimited
		}
	}

	// 生成 bearer + hash。这些在 TX【之前】运行,这样慢速 bcrypt 不会
	// 持有行锁。
	bearer, prefix, err := GenerateBearer(req.Environment)
	if err != nil {
		return IssueResult{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(bearer), i.bcryptCost)
	if err != nil {
		return IssueResult{}, fmt.Errorf("%w: bcrypt: %v", ErrAdminBackend, err)
	}

	// TX:原子地完成 per-actor 加锁 + 速率限制 + 插入 api_keys + audit 行。
	// count 与 insert 必须在同一个 TX 内、置于 per-actor advisory lock
	// 之后,否则并发请求会竞态越过 30/小时 的上限。该 advisory lock 在
	// TX 结束时自动释放。
	// actorID 同时用作 per-actor advisory lock 键和速率窗口计数键;它必须与
	// 写入 admin_audit_events.actor_id 的审计归属串一致,否则计数查询
	// (WHERE actor_id = $1)会与实际写入的行错位。统一走 AuditActor()。
	actorID := req.Caller.AuditActor()
	rateLimited := false
	out := IssueResult{KeyPrefix: prefix, Status: "active"}
	err = i.tx(ctx, func(qtx *admindb.Queries) error {
		if err := qtx.AcquireAdminIssuanceLock(ctx, actorID); err != nil {
			return fmt.Errorf("%w: advisory lock: %v", ErrAdminBackend, err)
		}
		count, err := qtx.CountIssuanceInWindow(ctx, admindb.CountIssuanceInWindowParams{
			ActorID:       actorID,
			WindowSeconds: int32(i.rateLimitWindowSecs),
			LegacyActorID: legacyActorKey(req.Caller),
		})
		if err != nil {
			return fmt.Errorf("%w: rate-limit count: %v", ErrAdminBackend, err)
		}
		if int(count) >= i.rateLimitPerHour {
			rateLimited = true
			return ErrAdminRateLimited
		}
		row, err := qtx.AdminInsertAPIKey(ctx, admindb.AdminInsertAPIKeyParams{
			TenantID:  req.TenantID,
			UserID:    req.UserID,
			Name:      req.Name,
			KeyHash:   string(hash),
			KeyPrefix: prefix,
			ExpiresAt: pgTimestampPtr(req.ExpiresAt),
		})
		if err != nil {
			// AdminInsertAPIKey 现在以写入时刻 tenant + user
			// 处于 active 为条件。NoRows = 目标在预检与 commit 之间被
			// 竞态改为 disabled/deleted;以 bad-request 而非 backend
			// 表面化。
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: target tenant or user became inactive", ErrAdminBadRequest)
			}
			return fmt.Errorf("%w: insert api_key: %v", ErrAdminBackend, err)
		}
		out.APIKeyID = row.ID
		out.CreatedAt = row.CreatedAt.Time

		// 审计 payload:prefix + tenant + user + environment。
		// 【绝不】包含明文 bearer 或 hash。
		payloadBytes, _ := json.Marshal(map[string]any{
			"key_prefix":  prefix,
			"tenant_id":   req.TenantID,
			"user_id":     req.UserID,
			"environment": req.Environment,
			"name":        req.Name,
		})
		actorRole := req.Caller.Role
		if actorRole == "" {
			actorRole = RoleTenantOperator
		}
		if _, err := qtx.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID:   nullableInt64(req.TenantID),
			ActorID:    req.Caller.AuditActor(),
			ActorRole:  actorRole,
			Action:     "issue_api_key",
			TargetType: "api_key",
			TargetID:   nullableInt64(row.ID),
			RequestID:  nullableString(req.RequestID),
			Reason:     nullableString(req.Reason),
			Payload:    payloadBytes,
		}); err != nil {
			return fmt.Errorf("%w: insert audit: %v", ErrAdminBackend, err)
		}
		return nil
	})
	if err != nil {
		// 速率限制拒绝路径:TX 已回滚,因此在一个全新的连接里写拒绝
		// audit 行。best-effort;吞掉 audit-insert 的错误,因为拒绝结果
		// 已经返回给调用方。
		if rateLimited {
			_ = i.audit(ctx, req, "denied", "rate_limited", 0)
		}
		return IssueResult{}, err
	}

	// 若调用方是 bootstrap 行且刚刚签发了一个非 bootstrap 的 admin
	//(通过 /admin/v1/api-keys 走到这一步并不常见;bootstrap 通常面向
	// api_keys 而非 admin_tokens),则自动禁用 bootstrap 行,使 env-var
	// token 停止工作。safe-best-effort。
	// 这里跳过,因为 /admin/v1/api-keys 签发的是 api_keys 行而非
	// admin_tokens 行;bootstrap 停用 hook 位于 admin_tokens 签发流程
	//(Phase E)。

	out.Plaintext = bearer
	out.ExpiresAt = req.ExpiresAt
	return out, nil
}

// audit 在任何 TX 之外记录一条拒绝路径的 admin_audit_events 行。
// 返回 insert 错误(供调用方记日志);我们在调用处吞掉它,因为拒绝
// 结果已经返回给调用方。
func (i *KeyIssuer) audit(ctx context.Context, req IssueRequest, outcome, reason string, targetID int64) error {
	q := admindb.New(i.pool)
	payload, _ := json.Marshal(map[string]any{
		"outcome":   outcome,
		"reason":    reason,
		"tenant_id": req.TenantID,
		"user_id":   req.UserID,
	})
	actorRole := req.Caller.Role
	if actorRole == "" {
		actorRole = RoleTenantOperator
	}
	// deny-audit【始终】把 tenant_id 设为 NULL,因为调用方可能针对了
	// 一个不存在的 tenant;否则 admin_audit_events.tenant_id 上的 FK 会
	// 拒绝该行,我们会悄无声息地丢掉这个拒绝事件。被尝试的 tenant_id
	// 留在 payload jsonb 中供取证审查。
	_, err := q.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID:   nil,
		ActorID:    req.Caller.AuditActor(),
		ActorRole:  actorRole,
		Action:     "issue_api_key",
		TargetType: "api_key",
		TargetID:   nullableInt64(targetID),
		RequestID:  nullableString(req.RequestID),
		Reason:     nullableString(reason),
		Payload:    payload,
	})
	return err
}

// tx 在一个全新的事务内运行 fn。与 registry.PostgresRegistry.ResolveModel
// (使用 REPEATABLE READ 只读)所用的模式对应。签发会写入,故我们使用
// 默认隔离级别,但保持相同形态。
func (i *KeyIssuer) tx(ctx context.Context, fn func(*admindb.Queries) error) error {
	tx, err := i.pool.BeginTx(ctx, pgx.TxOptions{})
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

// pgTimestampPtr 把一个可选的 *time.Time 转换为 pgtype.Timestamptz。
// 空指针 => 零值(Valid=false),Postgres 将其存为 NULL。
func pgTimestampPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func nullableInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

var _ = errors.New // 预留未来扩展

// legacyActorKey 返回 P2b-1 之前的老格式限流键(裸 TokenID 串),让限流窗跨审计格式
// 迁移保持连续(老 "5" 行与新 "admin_token:5" 行同桶)。无老格式的来源(session)返回
// 当前键同串,OR 谓词下无副作用。
func legacyActorKey(caller AdminIdentity) string {
	if caller.TokenID > 0 {
		return fmt.Sprintf("%d", caller.TokenID)
	}
	return caller.AuditActor()
}
