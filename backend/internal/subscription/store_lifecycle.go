package subscription

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// 支付回调后的订阅激活集中在这里，避免和计划 CRUD 混在一个文件里。
func (s *PostgresStore) ActivatePaidOrder(ctx context.Context, input ActivatePaidOrderInput) (ActivationResult, error) {
	if s == nil || s.pool == nil {
		return ActivationResult{}, ErrStoreNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		result, err := s.activatePaidOrderOnce(ctx, input)
		if isSerializationFailure(err) {
			lastErr = err
			continue
		}
		return result, err
	}
	return ActivationResult{}, lastErr
}

func (s *PostgresStore) activatePaidOrderOnce(ctx context.Context, input ActivatePaidOrderInput) (ActivationResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ActivationResult{}, fmt.Errorf("subscription: begin activation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	order, err := scanOrder(tx.QueryRow(ctx, `
SELECT id, tenant_id, user_id, plan_id, recharge_order_id, trade_no, status,
	price, currency_code, provider, plan_code, plan_name,
	duration_unit, duration_value, duration_seconds,
	quota_limit, quota_reset_period, quota_reset_interval_seconds,
	created_at, paid_at, activated_at, updated_at
FROM subscription_orders
WHERE tenant_id=$1 AND trade_no=$2
FOR UPDATE`, input.TenantID, input.TradeNo))
	if errors.Is(err, pgx.ErrNoRows) {
		return ActivationResult{Matched: false}, ErrOrderNotFound
	}
	if err != nil {
		return ActivationResult{}, fmt.Errorf("subscription: lock paid order: %w", err)
	}
	if input.UserID > 0 && order.UserID != input.UserID {
		return ActivationResult{}, ErrPaymentMismatch
	}
	if input.RechargeOrderID > 0 && order.RechargeOrderID != input.RechargeOrderID {
		return ActivationResult{}, ErrPaymentMismatch
	}
	if order.Status == OrderStatusActive {
		sub, err := getSubscriptionByOrder(ctx, tx, order.TenantID, order.ID)
		if err != nil {
			return ActivationResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ActivationResult{}, fmt.Errorf("subscription: commit activation replay: %w", err)
		}
		return ActivationResult{Matched: true, Idempotent: true, Order: order, Subscription: sub}, nil
	}
	if order.Status != OrderStatusPending {
		return ActivationResult{}, ErrOrderStateConflict
	}
	end, err := planEnd(input.PaidAt, order)
	if err != nil {
		return ActivationResult{}, err
	}
	nextReset := nextResetAfter(input.PaidAt, end, order.QuotaResetPeriod, order.QuotaResetIntervalSeconds)
	sub, err := scanUserSubscription(tx.QueryRow(ctx, `
INSERT INTO user_subscriptions (
	tenant_id, user_id, plan_id, source_order_id, status,
	quota_limit, quota_used, quota_reset_period, quota_reset_interval_seconds,
	started_at, current_period_started_at, next_quota_reset_at, expires_at,
	created_at, updated_at
) VALUES (
	$1, $2, $3, $4, 'active',
	$5, 0, $6, $7,
	$8, $8, $9, $10,
	$8, $8
)
RETURNING id, tenant_id, user_id, plan_id, source_order_id, status,
	quota_limit, quota_used, quota_reset_period, quota_reset_interval_seconds,
	started_at, current_period_started_at, next_quota_reset_at, expires_at,
	created_at, updated_at`,
		order.TenantID, order.UserID, order.PlanID, order.ID,
		order.QuotaLimit, order.QuotaResetPeriod, order.QuotaResetIntervalSeconds,
		input.PaidAt, nextReset, end,
	))
	if err != nil {
		if isUniqueViolation(err) {
			existing, getErr := getSubscriptionByOrder(ctx, tx, order.TenantID, order.ID)
			if getErr != nil {
				return ActivationResult{}, getErr
			}
			if err := markOrderActive(ctx, tx, order, input.PaidAt); err != nil {
				return ActivationResult{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return ActivationResult{}, fmt.Errorf("subscription: commit activation duplicate: %w", err)
			}
			return ActivationResult{Matched: true, Idempotent: true, Order: order, Subscription: existing}, nil
		}
		return ActivationResult{}, fmt.Errorf("subscription: insert user subscription: %w", err)
	}
	if err := markOrderActive(ctx, tx, order, input.PaidAt); err != nil {
		return ActivationResult{}, err
	}
	order.Status = OrderStatusActive
	order.PaidAt = &input.PaidAt
	order.ActivatedAt = &input.PaidAt
	order.UpdatedAt = input.PaidAt
	if err := tx.Commit(ctx); err != nil {
		return ActivationResult{}, fmt.Errorf("subscription: commit activation: %w", err)
	}
	return ActivationResult{Matched: true, Order: order, Subscription: sub}, nil
}

func markOrderActive(ctx context.Context, tx pgx.Tx, order Order, paidAt time.Time) error {
	if _, err := tx.Exec(ctx, `
UPDATE subscription_orders
SET status='active', paid_at=$3, activated_at=$3, updated_at=$3
WHERE tenant_id=$1 AND id=$2 AND status IN ('pending','active')`, order.TenantID, order.ID, paidAt); err != nil {
		return fmt.Errorf("subscription: mark order active: %w", err)
	}
	return nil
}

func getSubscriptionByOrder(ctx context.Context, q querier, tenantID, orderID int64) (UserSubscription, error) {
	sub, err := scanUserSubscription(q.QueryRow(ctx, `
SELECT id, tenant_id, user_id, plan_id, source_order_id, status,
	quota_limit, quota_used, quota_reset_period, quota_reset_interval_seconds,
	started_at, current_period_started_at, next_quota_reset_at, expires_at,
	created_at, updated_at
FROM user_subscriptions
WHERE tenant_id=$1 AND source_order_id=$2`, tenantID, orderID))
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: get by order: %w", err)
	}
	return sub, nil
}
