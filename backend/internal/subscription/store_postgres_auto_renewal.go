// HUAKAI · iKun

package subscription

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/tenancy"
)

// ListAutoRenewDue 扫 expires_at<=dueCutoff 且 auto_renew=true 的 active 订阅 (worker 批处理)。
// dueCutoff = now + 提前续费窗口: 扫「已到点」与「即将到点」两类, 让续费抢在到期 worker 收割前完成。
// 比 ListDueExpiry 多 auto_renew=true 过滤 —— 只对用户 opt-in 的订阅尝试续费; 与 ListDueExpiry 保持
// 无 auto_renew 排除对偶, 续费失败(余额不足)的订阅仍在 expires_at 到点被 ExpiryWorker 收割, 不留白嫖窗口。
func (s *PostgresStore) ListAutoRenewDue(ctx context.Context, dueCutoff time.Time, after AutoRenewCursor, limit int) ([]UserSubscription, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT`+subscriptionSelectColumnsS+`
	FROM user_subscriptions s
	JOIN tenants t ON t.id=s.tenant_id
	JOIN users u ON u.id=s.user_id AND u.tenant_id=s.tenant_id
	WHERE s.status='active'
	  AND s.auto_renew=true
	  AND s.expires_at <= $1
	  AND t.status='active'
	  AND t.deleted_at IS NULL
	  AND u.principal_kind='human'
	  AND u.role='user'
	  AND u.status='active'
	  AND u.deleted_at IS NULL
	  AND (
    $2::bigint = 0
    OR s.expires_at > $3
    OR (s.expires_at = $3 AND s.id > $2)
  )
ORDER BY s.expires_at, s.id
LIMIT $4`, dueCutoff, after.ID, after.ExpiresAt, limit)
	if err != nil {
		return nil, fmt.Errorf("subscription: list auto renew due: %w", err)
	}
	defer rows.Close()
	var out []UserSubscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// TryAutoRenewSubscription 单事务尝试自动续费, 见 Store 接口注释的不变量。
// 瞬时序列化冲突 (40001/40P01) 内层重试; 幂等锚唯一冲突 (23505) 当"已续过"跳过。
func (s *PostgresStore) TryAutoRenewSubscription(ctx context.Context, rec autoRenewRecord) (AutoRenewResult, error) {
	if s == nil || s.pool == nil {
		return AutoRenewResult{}, ErrStoreNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < subscriptionTxRetryAttempts; attempt++ {
		res, err := s.tryAutoRenewOnce(ctx, rec)
		if err == nil {
			return res, nil
		}
		if isPgRetryableTxConflict(err) {
			lastErr = err
			continue
		}
		// 并发 worker / 重试已为同 (订阅, 周期) 写了扣款行 → 幂等跳过, 不重复扣。
		if isUniqueViolation(err) {
			return AutoRenewResult{Renewed: false, SkipReason: AutoRenewSkipAlreadyRenewed}, nil
		}
		return AutoRenewResult{}, err
	}
	return AutoRenewResult{}, fmt.Errorf("subscription: auto renew exhausted retries: %w", lastErr)
}

func (s *PostgresStore) tryAutoRenewOnce(ctx context.Context, rec autoRenewRecord) (AutoRenewResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AutoRenewResult{}, fmt.Errorf("subscription: begin auto renew: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1) 先锁住活跃租户事实，使停用与扣款严格串行。停用已经生效时零副作用跳过；
	// 本事务先拿到共享锁时，停用会等待本次已开始的续费完整提交或回滚。
	err = tenancy.LockActiveForWrite(ctx, tx, rec.TenantID)
	if errors.Is(err, tenancy.ErrTenantInactive) {
		return AutoRenewResult{Renewed: false, SkipReason: AutoRenewSkipTenantInactive}, nil
	}
	if err != nil {
		return AutoRenewResult{}, fmt.Errorf("subscription: lock active tenant for auto renew: %w", err)
	}

	// 2) 锁订阅行重查仍 active + auto_renew + due (并发/重复防护)。
	sub, err := getSubscriptionForUpdateTx(ctx, tx, rec.TenantID, rec.SubscriptionID)
	if err != nil {
		return AutoRenewResult{}, err
	}
	if sub.Status != StatusActive {
		return skipNoCommit(ctx, tx, sub, AutoRenewSkipNotDue)
	}
	if !sub.AutoRenew {
		return skipNoCommit(ctx, tx, sub, AutoRenewSkipAutoRenewOff)
	}
	// 锁内复查仍到点。cutoff = max(DueCutoff, Now): 用 DueCutoff(=批扫 now+提前窗口)放行提前扫出的
	// 「即将到期」行进续费(否则按 now 判定会把整个提前窗口候选全部误跳过); 兜底 Now 防调用方未设
	// DueCutoff(零值)时把所有行误判为不到点。到期日被别的路径(手动续期/延期/上轮续费)推到 cutoff
	// 之外 → 本周期不再到点, 零副作用跳过。
	cutoff := rec.DueCutoff
	if cutoff.Before(rec.Now) {
		cutoff = rec.Now
	}
	if sub.ExpiresAt.After(cutoff) {
		return skipNoCommit(ctx, tx, sub, AutoRenewSkipNotDue)
	}

	// 3) 锁内复查订阅所属主体仍是活跃最终用户。列表 JOIN 只减少无效候选，
	// 事务内锁才是直接重放、并发停用和删除场景的权威资金守卫。
	var lockedUserID int64
	if err := tx.QueryRow(ctx, `
SELECT id
FROM users
WHERE tenant_id=$1
  AND id=$2
  AND principal_kind='human'
  AND role='user'
  AND status='active'
  AND deleted_at IS NULL
FOR UPDATE`, rec.TenantID, sub.UserID).Scan(&lockedUserID); errors.Is(err, pgx.ErrNoRows) {
		return skipNoCommit(ctx, tx, sub, AutoRenewSkipUserInactive)
	} else if err != nil {
		return AutoRenewResult{}, fmt.Errorf("subscription: lock active user for auto renew: %w", err)
	}

	// 4) 续费周期标识 = 本次续费前订阅 expires_at; 同窗口幂等只续一次。
	periodKey := sub.ExpiresAt.UTC().Format(time.RFC3339Nano)
	if exists, err := autoRenewalChargeExistsTx(ctx, tx, rec.TenantID, sub.ID, periodKey); err != nil {
		return AutoRenewResult{}, err
	} else if exists {
		return skipNoCommit(ctx, tx, sub, AutoRenewSkipAlreadyRenewed)
	}

	// 5) 锁价: 续费价 = 套餐当前 price_cents (套餐停用/不存在 → 不续)。
	plan, err := getPlanTx(ctx, tx, rec.TenantID, sub.PlanID)
	if err != nil {
		if errors.Is(err, ErrPlanNotFound) {
			return skipNoCommit(ctx, tx, sub, AutoRenewSkipPlanUnavailable)
		}
		return AutoRenewResult{}, err
	}
	if !plan.Enabled {
		return skipNoCommit(ctx, tx, sub, AutoRenewSkipPlanUnavailable)
	}
	priceCents := plan.PriceCents
	if priceCents < 0 {
		priceCents = 0
	}

	// 6) 扣钱包余额 (price>0 才扣)。条件 UPDATE 原子守卫: 余额不足 → 不扣 → 跳过。
	if priceCents > 0 {
		ok, err := debitUserBalanceTx(ctx, tx, rec.TenantID, sub.UserID, priceCents, rec.Now)
		if err != nil {
			return AutoRenewResult{}, err
		}
		if !ok {
			// 余额不足: 绝不扣款, 不续期。回滚整事务零副作用。
			return skipNoCommit(ctx, tx, sub, AutoRenewSkipInsufficientFund)
		}
	}

	// 7) 续期 (同事务): 延长 expires_at + 刷新 caps 策略。EnforceUpgradeOnly=false: 续同档不触发降级闸。
	res, err := ActivateOrRenewTx(ctx, tx, ActivateInput{
		TenantID:           rec.TenantID,
		UserID:             sub.UserID,
		PlanID:             sub.PlanID,
		SourceKind:         EffectSourceAdmin, // 系统自动续费, 走非订单/非券路径。
		ActorKind:          ActorKindSystem,
		EnforceUpgradeOnly: false,
		Now:                rec.Now,
	})
	if err != nil {
		return AutoRenewResult{}, err
	}

	// 8) 写幂等锚 + money 日志行 (同事务)。撞唯一索引 → 外层当"已续过"。
	chargeID, err := insertAutoRenewalChargeTx(ctx, tx, autoRenewalCharge{
		TenantID:           rec.TenantID,
		UserID:             sub.UserID,
		UserSubscriptionID: sub.ID,
		PeriodKey:          periodKey,
		PlanID:             sub.PlanID,
		AmountCents:        priceCents,
		PrevExpiresAt:      sub.ExpiresAt,
		NewExpiresAt:       res.NewExpiresAt,
	})
	if err != nil {
		return AutoRenewResult{}, err
	}

	// 9) 统一 money 账本 (同事务): 扣款进 billing_events, 与充值/退款/兑换同流可对账。
	// 免费续费 (price<=0) 无钱移动, 不写事件行 (账本只记 money movement)。
	if priceCents > 0 {
		if err := insertAutoRenewalBillingEventTx(ctx, tx, rec.TenantID, sub.ID, chargeID, priceCents); err != nil {
			return AutoRenewResult{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return AutoRenewResult{}, fmt.Errorf("subscription: commit auto renew: %w", err)
	}
	return AutoRenewResult{
		Subscription: res.Subscription,
		Renewed:      true,
		ChargedCents: priceCents,
	}, nil
}

// skipNoCommit 提交一个无副作用的"跳过"结果。订阅状态/幂等命中等跳过分支不写任何行,
// 但仍 commit (回滚也可, commit 更明确表达"已查证无需动作"); 不持有写, 安全。
func skipNoCommit(ctx context.Context, tx pgx.Tx, sub UserSubscription, reason string) (AutoRenewResult, error) {
	if err := tx.Commit(ctx); err != nil {
		return AutoRenewResult{}, fmt.Errorf("subscription: commit auto renew skip: %w", err)
	}
	return AutoRenewResult{Subscription: sub, Renewed: false, SkipReason: reason}, nil
}

// debitUserBalanceTx 从可变钱包表 user_balances 条件扣减 (与退款扣款同表同形态)。
// 守卫 balance-held>=amount 进 WHERE: 余额不足时 RowsAffected==0 (返回 false), 不扣分文。
// cents → numeric(20,8) USD: cents/100 (与 payment 域 decimalFromCents 等价, 不跨包引私有 helper)。
func debitUserBalanceTx(ctx context.Context, tx pgx.Tx, tenantID, userID, amountCents int64, now time.Time) (bool, error) {
	amount := decimal.NewFromInt(amountCents).Div(decimal.NewFromInt(100))
	tag, err := tx.Exec(ctx, `
UPDATE user_balances
SET balance = balance - $3,
    version = version + 1,
    updated_at = $4
WHERE tenant_id=$1
  AND user_id=$2
  AND balance - held >= $3`, tenantID, userID, amount, now)
	if err != nil {
		return false, fmt.Errorf("subscription: debit wallet for auto renew: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// autoRenewalCharge 一条续费扣款账本行 (幂等锚 + money 审计)。
type autoRenewalCharge struct {
	TenantID           int64
	UserID             int64
	UserSubscriptionID int64
	PeriodKey          string
	PlanID             int64
	AmountCents        int64
	PrevExpiresAt      time.Time
	NewExpiresAt       time.Time
}

func insertAutoRenewalChargeTx(ctx context.Context, tx pgx.Tx, c autoRenewalCharge) (int64, error) {
	var id int64
	if err := tx.QueryRow(ctx, `
INSERT INTO subscription_auto_renewal_charges (
	tenant_id, user_id, user_subscription_id, period_key, plan_id,
	amount_cents, prev_expires_at, new_expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id`,
		c.TenantID, c.UserID, c.UserSubscriptionID, c.PeriodKey, c.PlanID,
		c.AmountCents, c.PrevExpiresAt, c.NewExpiresAt).Scan(&id); err != nil {
		return 0, fmt.Errorf("subscription: insert auto renewal charge: %w", err)
	}
	return id, nil
}

// insertAutoRenewalBillingEventTx 把续费扣款写进统一 money 账本并回链账本行。
// 符号约定沿用钱包流出先例 (payment_refunded): actual_cost=0, actual_cost_signed=-金额,
// SUM(actual_cost_signed) 即钱包净流向。事件类型与关联列配对由表 CHECK 约束把守。
func insertAutoRenewalBillingEventTx(ctx context.Context, tx pgx.Tx, tenantID, subscriptionID, chargeID, amountCents int64) error {
	signed := decimal.NewFromInt(amountCents).Div(decimal.NewFromInt(100)).Neg()
	fingerprint := fmt.Sprintf("sub-autorenew:t%d:s%d:c%d", tenantID, subscriptionID, chargeID)
	var billingID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO billing_events (tenant_id, event_type, actual_cost, actual_cost_signed,
	stream_state, delivered_token_count, fingerprint, subscription_auto_renewal_charge_id)
VALUES ($1, 'subscription_auto_renewed', 0, $2, 2, 0, $3, $4)
RETURNING id`, tenantID, signed, fingerprint, chargeID).Scan(&billingID); err != nil {
		return fmt.Errorf("subscription: insert auto renewal billing event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE subscription_auto_renewal_charges SET billing_event_id=$3
WHERE tenant_id=$1 AND id=$2`, tenantID, chargeID, billingID); err != nil {
		return fmt.Errorf("subscription: link auto renewal billing event: %w", err)
	}
	return nil
}

// autoRenewalChargeExistsTx 预查该 (订阅, 周期) 是否已扣过 (幂等命中即跳过)。
// 唯一索引仍是双扣的最终防线 (并发两 tx 同时预查均未命中时, 后提交者撞 23505)。
func autoRenewalChargeExistsTx(ctx context.Context, tx pgx.Tx, tenantID, subscriptionID int64, periodKey string) (bool, error) {
	var one int
	err := tx.QueryRow(ctx, `
SELECT 1 FROM subscription_auto_renewal_charges
WHERE tenant_id=$1 AND user_subscription_id=$2 AND period_key=$3
LIMIT 1`, tenantID, subscriptionID, periodKey).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("subscription: check auto renewal charge: %w", err)
	}
	return true, nil
}
