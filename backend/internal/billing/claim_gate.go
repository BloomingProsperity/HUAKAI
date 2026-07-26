package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/tenancy"
)

// 对应 F-OBS-001 各 Failure Path 类别的哨兵错误。
var (
	// ErrFingerprintConflict ↔ 规格 §Failure Path TX1_FINGERPRINT_CONFLICT。
	// 同一 logical_request_id 此前已用不同的 payload hash 预扣过——
	// 表明存在重放攻击。
	ErrFingerprintConflict = errors.New("billing: TX1_FINGERPRINT_CONFLICT")

	// ErrClaimRace ↔ 规格 §Failure Path TX1_CLAIM_RACE。
	// 并发尝试赢得了幂等 claim;gateway 应在有限的重试预算内
	// 重新读取已结算的响应。
	ErrClaimRace = errors.New("billing: TX1_CLAIM_RACE")

	// ErrPoolNotConfigured 在构造 DefaultClaimGate 时没有传入真实的
	// pgxpool.Pool 即触发。按集成冲刺契约:PG 不可达时
	//"函数返回一个有类型的错误,而非 200 OK"。
	ErrPoolNotConfigured = errors.New("billing: pgx pool not configured")
)

// DefaultClaimGate 是经由 pgx + sqlc 以 PostgreSQL 为后端的生产级 Tx1
// ClaimGate。通过 NewClaimGate(pool) 构造;各方法总是在事务中运行,
// 当 store 缺失时绝不静默成功。
// DefaultClaimLeaseWindow 是 reserving claim 的孤儿回收租约默认窗口。
//
// 该窗口必须显著大于"单个请求的最大生命周期 + 结算/DLQ 重放余量"。reserve 时
// claim 的 lease_expires_at 设为 now+window 且请求生命周期内不续租;LeaseSweeper
// 会 Abort 任何 lease 过期仍 reserving 的 claim。若窗口短于请求时长,跑得久的合法
// 流式请求(大输出/慢上游/长 tool-use,可达 HUAKAI_STREAM_TOTAL_TIMEOUT 默认 600s)
// 会在仍在传输时被 sweeper 误 Abort —— 已交付内容永不计费(亏钱)且 in_flight
// 在流仍活时被减低估致上游账号超并发。故默认取 30min,远大于 600s 流上限 + 结算余量。
// (旧值 90s 是按 slot 抢占窗口设的,误用到必须活过整个请求的 claim 上。)
// 真孤儿(进程崩溃)仍在此窗口后被回收,仅回收延迟变长,money 安全无丢失。
const DefaultClaimLeaseWindow = 30 * time.Minute

type DefaultClaimGate struct {
	pool *pgxpool.Pool
	q    *dbbilling.Queries
	// Lease window for claim row orphan-sweep recovery; 必须 > 请求最大生命周期,
	// 见 DefaultClaimLeaseWindow。
	LeaseWindow time.Duration
	// 可选注入:Serializable 重试的退避 sleeper 与随机源。生产留 nil 走默认
	// (真实退避);单测注入确定性实现,免真睡眠又能驱动重试路径。
	reserveSleep func(context.Context, time.Duration) bool
	reserveRand  func(int64) int64
}

// NewClaimGate 构造一个 DefaultClaimGate。传入 nil pool 会得到一个其各方法
// 返回 ErrPoolNotConfigured 的 gate——调用方可以围绕它做 no-op,但在生产中
// 必须将其视为不可恢复的错误配置。
func NewClaimGate(pool *pgxpool.Pool) *DefaultClaimGate {
	if pool == nil {
		return &DefaultClaimGate{pool: nil}
	}
	return &DefaultClaimGate{
		pool:        pool,
		q:           dbbilling.New(pool),
		LeaseWindow: DefaultClaimLeaseWindow,
	}
}

// Reserve 执行完整的 Tx1 协议:对候选行 SELECT FOR UPDATE,
// 区分幂等重放与指纹冲突,两者都不是则 INSERT 一条新的 reserving
// claim,最后 COMMIT。
//
// 返回的 *ReserveResult 携带:
//   - IdempotencyHit=true    → 调用方跳过上游调用并重放缓存
//   - FingerprintConflict=true → 调用方向客户端返回 409;不计费
//   - ClaimID > 0 且两标志皆否 → 调用方继续进行 Pool acquire + 上游调用
func (g *DefaultClaimGate) Reserve(ctx context.Context, req ReserveRequest) (*ReserveResult, error) {
	if g == nil || g.pool == nil {
		return nil, ErrPoolNotConfigured
	}
	billingEffect, err := NormalizeBillingEffect(req.BillingEffect)
	if err != nil {
		return nil, err
	}
	req.BillingEffect = billingEffect
	idempotencyKey := ComputeIdempotencyFingerprint(req)
	// Serializable 隔离下同一用户并发争抢 user_balances 行会抛 40001;retryReserve 在
	// 序列化冲突上做有限退避重试(每次重跑一整个干净事务),预算耗尽映射 ErrClaimRace
	// →调用方返回可重试的 409+Retry-After,而非不透明 500。
	return retryReserve(ctx, func(ctx context.Context) (*ReserveResult, error) {
		return g.reserveOnce(ctx, req, idempotencyKey)
	}, g.reserveSleep, g.reserveRand)
}

// reserveOnce 执行一次完整 Tx1 事务(BeginTx→Commit/Rollback)。由 retryReserve 在
// Serializable 冲突时重跑:每次都是全新干净事务,幂等查找 / 指纹重放检查 / hold 三个
// 不变量逐次重建,失败整事务回滚不留 claim/hold,故重跑既不重复扣也不漏扣。
func (g *DefaultClaimGate) reserveOnce(ctx context.Context, req ReserveRequest, idempotencyKey string) (*ReserveResult, error) {
	tx, err := g.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("billing: begin Tx1: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := g.q.WithTx(tx)

	// 租户状态与本次预扣在同一事务内串行。租户停用/删除持有该行的 UPDATE 锁；
	// 本请求持有 SHARE 锁。两者谁先获得锁就成为明确边界：先预扣的请求继续
	// 结算恢复，先停用的租户不再产生新 claim、hold 或上游副作用。
	// 不能使用 KEY SHARE：租户状态是非键列，PostgreSQL 的 NO KEY UPDATE
	// 与 KEY SHARE 可以并存，会让停用事务与新预扣同时越过边界。
	err = tenancy.LockActiveForWrite(ctx, tx, req.TenantID)
	if errors.Is(err, tenancy.ErrTenantInactive) {
		return nil, ErrTenantInactive
	}
	if err != nil {
		return nil, fmt.Errorf("billing: lock active tenant: %w", err)
	}

	// 步骤 1:带行锁的幂等查找。
	existing, err := qtx.GetClaimByIdempotency(ctx, dbbilling.GetClaimByIdempotencyParams{
		TenantID:       req.TenantID,
		APIKeyID:       req.APIKeyID,
		IdempotencyKey: idempotencyKey,
	})
	if err == nil {
		// 已存在且指纹匹配的 claim——按规格 §Tx1 步骤 3 走重放路径。
		switch existing.Status {
		case "committed":
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("billing: commit idempotent-hit Tx1: %w", err)
			}
			return &ReserveResult{ClaimID: existing.ID, AttemptSeq: existing.AttemptSeq, IdempotencyHit: true}, nil
		case "reserving":
			return nil, ErrClaimRace
		case "aborted":
			existingEffect, effectErr := NormalizeBillingEffect(BillingEffect(existing.BillingEffect))
			if effectErr != nil || existingEffect != req.BillingEffect {
				return nil, ErrFingerprintConflict
			}
			// 前驱已 aborted——通过复活该行来重试。
			// 在相同的 (tenant, api_key, idempotency_key) 下插入新行
			// 会违反 uq_claims_idempotency。ReReserveAbortedClaim 把
			// status 翻回 'reserving' 并递增 attempt_seq。
			leaseExpiresAt := time.Now().UTC().Add(g.leaseWindow())
			row, err := qtx.ReReserveAbortedClaim(ctx, dbbilling.ReReserveAbortedClaimParams{
				ID:             existing.ID,
				LeaseExpiresAt: pgtype.Timestamptz{Time: leaseExpiresAt, Valid: true},
				PredictedCost:  req.PredictedCost,
				PoolingGroupID: nullableInt64(req.PoolingGroupID),
				TenantID:       req.TenantID,
			})
			if err != nil {
				return nil, fmt.Errorf("billing: re-reserve aborted claim: %w", err)
			}
			if req.BillingEffect == BillingEffectUserCharge {
				if _, err := Reserve(ctx, tx, ReserveParams{
					TenantID:        req.TenantID,
					UserID:          req.UserID,
					ClaimID:         row.ID,
					Cost:            req.PredictedCost,
					EnforcementMode: balanceHoldEnforcementMode(req.BalanceEnforcementMode),
				}); err != nil {
					if errors.Is(err, ErrBalanceHoldInsufficientBalance) {
						return nil, ErrInsufficientBalance
					}
					return nil, fmt.Errorf("billing: hold for re-reserve: %w", err)
				}
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("billing: commit re-reserve Tx1: %w", err)
			}
			return &ReserveResult{ClaimID: row.ID, AttemptSeq: row.AttemptSeq}, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("billing: claim idempotency lookup: %w", err)
	}

	// 步骤 2:重放攻击检查——同一 logical_request_id 携带不同指纹。
	if req.LogicalRequestID != "" {
		rows, err := qtx.GetClaimFingerprintByLogicalRequestID(ctx, dbbilling.GetClaimFingerprintByLogicalRequestIDParams{
			TenantID:         req.TenantID,
			APIKeyID:         req.APIKeyID,
			LogicalRequestID: req.LogicalRequestID,
		})
		if err != nil {
			return nil, fmt.Errorf("billing: claim fingerprint scan: %w", err)
		}
		for _, r := range rows {
			if r.RequestFingerprint != idempotencyKey {
				return &ReserveResult{FingerprintConflict: true}, ErrFingerprintConflict
			}
		}
	}

	// 步骤 3:插入一条新的 reserving claim。
	leaseExpiresAt := time.Now().UTC().Add(g.leaseWindow())
	inserted, err := qtx.InsertClaim(ctx, dbbilling.InsertClaimParams{
		TenantID:             req.TenantID,
		IdempotencyKey:       idempotencyKey,
		RequestFingerprint:   idempotencyKey,
		APIKeyID:             req.APIKeyID,
		UserID:               req.UserID,
		LogicalRequestID:     req.LogicalRequestID,
		EndpointFamily:       req.EndpointFamily,
		RequestedModel:       req.RequestedModel,
		PoolingGroupID:       nullableInt64(req.PoolingGroupID),
		BillingPolicyVersion: req.BillingPolicyVersion,
		RequestClass:         req.RequestClass,
		PredictedCost:        req.PredictedCost,
		CurrencyCode:         "USD",
		LeaseExpiresAt:       pgtype.Timestamptz{Time: leaseExpiresAt, Valid: true},
		BillingEffect:        string(req.BillingEffect),
	})
	if err != nil {
		// 唯一约束冲突 = 幂等竞争(并发插入者)。按竞争处理。
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrClaimRace
		}
		return nil, fmt.Errorf("billing: insert claim: %w", err)
	}
	if req.BillingEffect == BillingEffectUserCharge {
		if _, err := Reserve(ctx, tx, ReserveParams{
			TenantID:        req.TenantID,
			UserID:          req.UserID,
			ClaimID:         inserted.ID,
			Cost:            req.PredictedCost,
			EnforcementMode: balanceHoldEnforcementMode(req.BalanceEnforcementMode),
		}); err != nil {
			if errors.Is(err, ErrBalanceHoldInsufficientBalance) {
				return nil, ErrInsufficientBalance
			}
			return nil, fmt.Errorf("billing: hold for new claim: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("billing: commit Tx1: %w", err)
	}
	return &ReserveResult{ClaimID: inserted.ID, AttemptSeq: inserted.AttemptSeq}, nil
}

// ComputeIdempotencyFingerprint 对 9 个已持久化字段做 hash。
// ReserveRequest 中的 IdempotencyKeyClientHeader 被有意排除，因为客户端
// 重试标识不属于同一服务端请求事实的内容摘要。
//
// PoolingGroupID 同样被排除:pool group 现在由 Registry/Router 从可变的 admin
// 状态推导,而非来自客户端请求。若某 admin 在请求进行途中改写了 model→pool
// 绑定,使用相同 Idempotency-Key 的合法重试否则会 hash 出一个新指纹,
// 并表现为 idempotency_conflict。排除它使幂等仅依赖客户端可控的输入
// (tenant + key + logical id + payload + model alias + endpoint +
// billing policy + request class)。
func ComputeIdempotencyFingerprint(r ReserveRequest) string {
	billingEffect, err := NormalizeBillingEffect(r.BillingEffect)
	if err != nil {
		billingEffect = r.BillingEffect
	}
	h := sha256.New()
	for _, field := range []string{
		strconv.FormatInt(r.TenantID, 10),
		strconv.FormatInt(r.APIKeyID, 10),
		r.LogicalRequestID,
		r.EndpointFamily,
		r.NormalizedPayloadHash,
		r.RequestedModel,
		r.BillingPolicyVersion,
		r.RequestClass,
		string(billingEffect),
	} {
		h.Write([]byte(field))
		h.Write([]byte{0x1F}) // 单元分隔符:防止相邻字段拼接产生碰撞
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (g *DefaultClaimGate) leaseWindow() time.Duration {
	if g.LeaseWindow > 0 {
		return g.LeaseWindow
	}
	return DefaultClaimLeaseWindow
}

func nullableInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func balanceHoldEnforcementMode(mode BalanceEnforcementMode) EnforcementMode {
	if mode == BalanceEnforcementModeOptIn {
		return EnforcementModeOptIn
	}
	return EnforcementModeMandatory
}

// 编译期接口检查——DefaultClaimGate 必须满足 ClaimGate。
var _ ClaimGate = (*DefaultClaimGate)(nil)

// 在 Settler 实现之前,抑制 decimal 的未使用导入告警。
var _ = decimal.Zero
