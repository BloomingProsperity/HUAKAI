package subscription

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// 用户订阅查询、到期和额度周期重置放在周期任务文件中，便于后续接调度器。
func (s *PostgresStore) ListUserSubscriptions(ctx context.Context, input ListUserSubscriptionsInput) ([]UserSubscription, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	query := `
SELECT id, tenant_id, user_id, plan_id, source_order_id, status,
	quota_limit, quota_used, quota_reset_period, quota_reset_interval_seconds,
	started_at, current_period_started_at, next_quota_reset_at, expires_at,
	created_at, updated_at
FROM user_subscriptions
WHERE tenant_id=$1 AND user_id=$2`
	args := []any{input.TenantID, input.UserID}
	if input.ActiveOnly {
		query += ` AND status='active' AND (expires_at IS NULL OR expires_at > $3)`
		args = append(args, time.Now().UTC())
	}
	query += ` ORDER BY expires_at DESC NULLS LAST, id DESC`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("subscription: list user subscriptions: %w", err)
	}
	defer rows.Close()
	var subs []UserSubscription
	for rows.Next() {
		sub, err := scanUserSubscription(rows)
		if err != nil {
			return nil, fmt.Errorf("subscription: scan user subscription: %w", err)
		}
		subs = append(subs, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscription: user subscription rows: %w", err)
	}
	return subs, nil
}

func (s *PostgresStore) ExpireDueSubscriptions(ctx context.Context, input ExpireDueInput) (int, error) {
	if s == nil || s.pool == nil {
		return 0, ErrStoreNotConfigured
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 200
	}
	tag, err := s.pool.Exec(ctx, `
WITH due AS (
	SELECT id
	FROM user_subscriptions
	WHERE tenant_id=$1
	  AND status='active'
	  AND expires_at IS NOT NULL
	  AND expires_at <= $2
	ORDER BY expires_at ASC, id ASC
	LIMIT $3
)
UPDATE user_subscriptions us
SET status='expired', updated_at=$2
FROM due
WHERE us.tenant_id=$1 AND us.id=due.id`, input.TenantID, input.Now, limit)
	if err != nil {
		return 0, fmt.Errorf("subscription: expire due subscriptions: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (s *PostgresStore) ResetDueSubscriptions(ctx context.Context, input ResetDueInput) (int, error) {
	if s == nil || s.pool == nil {
		return 0, ErrStoreNotConfigured
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, user_id, plan_id, source_order_id, status,
	quota_limit, quota_used, quota_reset_period, quota_reset_interval_seconds,
	started_at, current_period_started_at, next_quota_reset_at, expires_at,
	created_at, updated_at
FROM user_subscriptions
WHERE tenant_id=$1
  AND status='active'
  AND next_quota_reset_at IS NOT NULL
  AND next_quota_reset_at <= $2
ORDER BY next_quota_reset_at ASC, id ASC
LIMIT $3`, input.TenantID, input.Now, limit)
	if err != nil {
		return 0, fmt.Errorf("subscription: list due resets: %w", err)
	}
	defer rows.Close()
	var due []UserSubscription
	for rows.Next() {
		sub, err := scanUserSubscription(rows)
		if err != nil {
			return 0, err
		}
		due = append(due, sub)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	resetCount := 0
	for _, sub := range due {
		if err := s.resetOne(ctx, input.Now, sub); err != nil {
			return resetCount, err
		}
		resetCount++
	}
	return resetCount, nil
}

func (s *PostgresStore) resetOne(ctx context.Context, now time.Time, sub UserSubscription) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("subscription: begin reset: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locked, err := scanUserSubscription(tx.QueryRow(ctx, `
SELECT id, tenant_id, user_id, plan_id, source_order_id, status,
	quota_limit, quota_used, quota_reset_period, quota_reset_interval_seconds,
	started_at, current_period_started_at, next_quota_reset_at, expires_at,
	created_at, updated_at
FROM user_subscriptions
WHERE tenant_id=$1 AND id=$2 AND status='active'
FOR UPDATE`, sub.TenantID, sub.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("subscription: lock reset row: %w", err)
	}
	if locked.NextQuotaResetAt == nil || locked.NextQuotaResetAt.After(now) {
		return tx.Commit(ctx)
	}
	base := *locked.NextQuotaResetAt
	end := time.Time{}
	if locked.ExpiresAt != nil {
		end = *locked.ExpiresAt
	}
	next := nextResetAfter(base, end, locked.QuotaResetPeriod, locked.QuotaResetIntervalSeconds)
	for next != nil && !next.After(now) {
		base = *next
		next = nextResetAfter(base, end, locked.QuotaResetPeriod, locked.QuotaResetIntervalSeconds)
	}
	if _, err := tx.Exec(ctx, `
UPDATE user_subscriptions
SET quota_used=0, current_period_started_at=$3, next_quota_reset_at=$4, updated_at=$5
WHERE tenant_id=$1 AND id=$2`, locked.TenantID, locked.ID, base, next, now); err != nil {
		return fmt.Errorf("subscription: update reset row: %w", err)
	}
	return tx.Commit(ctx)
}
