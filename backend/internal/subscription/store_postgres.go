package subscription

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

type createOrderRecord struct {
	TenantID        int64
	UserID          int64
	Plan            Plan
	RechargeOrderID int64
	TradeNo         string
	Provider        string
	Now             time.Time
}

func (s *PostgresStore) CreatePlan(ctx context.Context, input PlanInput) (Plan, error) {
	if s == nil || s.pool == nil {
		return Plan{}, ErrStoreNotConfigured
	}
	row := s.pool.QueryRow(ctx, `
INSERT INTO subscription_plans (
	tenant_id, code, name, description, enabled, price, currency_code,
	duration_unit, duration_value, duration_seconds,
	quota_limit, quota_reset_period, quota_reset_interval_seconds,
	max_purchases_per_user, sort_order, created_at, updated_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7,
	$8, $9, $10,
	$11, $12, $13,
	$14, $15, $16, $16
)
RETURNING id, tenant_id, code, name, description, enabled, price, currency_code,
	duration_unit, duration_value, duration_seconds,
	quota_limit, quota_reset_period, quota_reset_interval_seconds,
	max_purchases_per_user, sort_order, created_at, updated_at, archived_at`,
		input.TenantID, input.Code, input.Name, input.Description, input.Enabled, input.Price, input.CurrencyCode,
		input.DurationUnit, input.DurationValue, input.DurationSeconds,
		input.QuotaLimit, input.QuotaResetPeriod, input.QuotaResetIntervalSeconds,
		input.MaxPurchasesPerUser, input.SortOrder, input.Now,
	)
	plan, err := scanPlan(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Plan{}, ErrPlanConflict
		}
		return Plan{}, fmt.Errorf("subscription: create plan: %w", err)
	}
	return plan, nil
}

func (s *PostgresStore) ListPlans(ctx context.Context, tenantID int64, includeArchived bool) ([]Plan, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	query := `
SELECT id, tenant_id, code, name, description, enabled, price, currency_code,
	duration_unit, duration_value, duration_seconds,
	quota_limit, quota_reset_period, quota_reset_interval_seconds,
	max_purchases_per_user, sort_order, created_at, updated_at, archived_at
FROM subscription_plans
WHERE tenant_id=$1`
	if !includeArchived {
		query += ` AND archived_at IS NULL`
	}
	query += ` ORDER BY sort_order ASC, id ASC`
	rows, err := s.pool.Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("subscription: list plans: %w", err)
	}
	defer rows.Close()
	var plans []Plan
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			return nil, fmt.Errorf("subscription: scan plan: %w", err)
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscription: list plan rows: %w", err)
	}
	return plans, nil
}

func (s *PostgresStore) GetPlan(ctx context.Context, tenantID, planID int64) (Plan, error) {
	if s == nil || s.pool == nil {
		return Plan{}, ErrStoreNotConfigured
	}
	return s.getPlan(ctx, s.pool, tenantID, planID)
}

func (s *PostgresStore) getPlan(ctx context.Context, q querier, tenantID, planID int64) (Plan, error) {
	plan, err := scanPlan(q.QueryRow(ctx, `
SELECT id, tenant_id, code, name, description, enabled, price, currency_code,
	duration_unit, duration_value, duration_seconds,
	quota_limit, quota_reset_period, quota_reset_interval_seconds,
	max_purchases_per_user, sort_order, created_at, updated_at, archived_at
FROM subscription_plans
WHERE tenant_id=$1 AND id=$2 AND archived_at IS NULL`, tenantID, planID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, ErrPlanNotFound
	}
	if err != nil {
		return Plan{}, fmt.Errorf("subscription: get plan: %w", err)
	}
	return plan, nil
}

func (s *PostgresStore) UpdatePlan(ctx context.Context, patch PlanPatch) (Plan, error) {
	if s == nil || s.pool == nil {
		return Plan{}, ErrStoreNotConfigured
	}
	current, err := s.GetPlan(ctx, patch.TenantID, patch.ID)
	if err != nil {
		return Plan{}, err
	}
	input := PlanInput{
		TenantID:                  current.TenantID,
		Code:                      current.Code,
		Name:                      current.Name,
		Description:               current.Description,
		Enabled:                   current.Enabled,
		Price:                     current.Price,
		CurrencyCode:              current.CurrencyCode,
		DurationUnit:              current.DurationUnit,
		DurationValue:             current.DurationValue,
		DurationSeconds:           current.DurationSeconds,
		QuotaLimit:                current.QuotaLimit,
		QuotaResetPeriod:          current.QuotaResetPeriod,
		QuotaResetIntervalSeconds: current.QuotaResetIntervalSeconds,
		MaxPurchasesPerUser:       current.MaxPurchasesPerUser,
		SortOrder:                 current.SortOrder,
		Now:                       patch.Now,
	}
	if patch.Code != nil {
		input.Code = strings.TrimSpace(*patch.Code)
	}
	if patch.Name != nil {
		input.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Description != nil {
		input.Description = strings.TrimSpace(*patch.Description)
	}
	if patch.Enabled != nil {
		input.Enabled = *patch.Enabled
	}
	if patch.Price != nil {
		input.Price = *patch.Price
	}
	if patch.CurrencyCode != nil {
		input.CurrencyCode = *patch.CurrencyCode
	}
	if patch.DurationUnit != nil {
		input.DurationUnit = *patch.DurationUnit
	}
	if patch.DurationValue != nil {
		input.DurationValue = *patch.DurationValue
	}
	if patch.DurationSeconds != nil {
		input.DurationSeconds = *patch.DurationSeconds
	}
	if patch.QuotaLimit != nil {
		input.QuotaLimit = *patch.QuotaLimit
	}
	if patch.QuotaResetPeriod != nil {
		input.QuotaResetPeriod = *patch.QuotaResetPeriod
	}
	if patch.QuotaResetIntervalSeconds != nil {
		input.QuotaResetIntervalSeconds = *patch.QuotaResetIntervalSeconds
	}
	if patch.MaxPurchasesPerUser != nil {
		input.MaxPurchasesPerUser = *patch.MaxPurchasesPerUser
	}
	if patch.SortOrder != nil {
		input.SortOrder = *patch.SortOrder
	}
	input = normalizePlanInput(input, patch.Now)
	if err := validatePlanInput(input); err != nil {
		return Plan{}, err
	}
	plan, err := scanPlan(s.pool.QueryRow(ctx, `
UPDATE subscription_plans
SET code=$3, name=$4, description=$5, enabled=$6, price=$7, currency_code=$8,
	duration_unit=$9, duration_value=$10, duration_seconds=$11,
	quota_limit=$12, quota_reset_period=$13, quota_reset_interval_seconds=$14,
	max_purchases_per_user=$15, sort_order=$16, updated_at=$17
WHERE tenant_id=$1 AND id=$2 AND archived_at IS NULL
RETURNING id, tenant_id, code, name, description, enabled, price, currency_code,
	duration_unit, duration_value, duration_seconds,
	quota_limit, quota_reset_period, quota_reset_interval_seconds,
	max_purchases_per_user, sort_order, created_at, updated_at, archived_at`,
		patch.TenantID, patch.ID, input.Code, input.Name, input.Description, input.Enabled, input.Price, input.CurrencyCode,
		input.DurationUnit, input.DurationValue, input.DurationSeconds,
		input.QuotaLimit, input.QuotaResetPeriod, input.QuotaResetIntervalSeconds,
		input.MaxPurchasesPerUser, input.SortOrder, input.Now,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, ErrPlanNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return Plan{}, ErrPlanConflict
		}
		return Plan{}, fmt.Errorf("subscription: update plan: %w", err)
	}
	return plan, nil
}

func (s *PostgresStore) ArchivePlan(ctx context.Context, tenantID, planID int64, now time.Time) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE subscription_plans
SET enabled=false, archived_at=$3, updated_at=$3
WHERE tenant_id=$1 AND id=$2 AND archived_at IS NULL`, tenantID, planID, now)
	if err != nil {
		return fmt.Errorf("subscription: archive plan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPlanNotFound
	}
	return nil
}

func (s *PostgresStore) CreateOrder(ctx context.Context, record createOrderRecord) (Order, error) {
	if s == nil || s.pool == nil {
		return Order{}, ErrStoreNotConfigured
	}
	record.TradeNo = strings.TrimSpace(record.TradeNo)
	record.Provider = strings.ToLower(strings.TrimSpace(record.Provider))
	if record.TenantID <= 0 || record.UserID <= 0 || record.Plan.ID <= 0 ||
		record.RechargeOrderID <= 0 || record.TradeNo == "" || record.Provider == "" {
		return Order{}, ErrInvalidInput
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		order, err := s.createOrderOnce(ctx, record)
		if isSerializationFailure(err) {
			lastErr = err
			continue
		}
		return order, err
	}
	return Order{}, lastErr
}

func (s *PostgresStore) CancelRechargeOrder(ctx context.Context, tenantID, rechargeOrderID int64, now time.Time) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	if tenantID <= 0 || rechargeOrderID <= 0 {
		return ErrInvalidInput
	}
	if _, err := s.pool.Exec(ctx, `
UPDATE recharge_orders
SET status='CANCELLED', updated_at=$3
WHERE tenant_id=$1 AND id=$2 AND status='PENDING'`, tenantID, rechargeOrderID, now); err != nil {
		return fmt.Errorf("subscription: cancel orphan recharge order: %w", err)
	}
	return nil
}

func (s *PostgresStore) createOrderOnce(ctx context.Context, record createOrderRecord) (Order, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Order{}, fmt.Errorf("subscription: begin create order: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if record.Plan.MaxPurchasesPerUser > 0 {
		var count int
		if err := tx.QueryRow(ctx, `
SELECT
	(
		SELECT count(*)
		FROM user_subscriptions
		WHERE tenant_id=$1 AND user_id=$2 AND plan_id=$3
	) +
	(
		SELECT count(*)
		FROM subscription_orders
		WHERE tenant_id=$1 AND user_id=$2 AND plan_id=$3 AND status='pending'
	)`,
			record.TenantID, record.UserID, record.Plan.ID).Scan(&count); err != nil {
			return Order{}, fmt.Errorf("subscription: count purchases: %w", err)
		}
		if count >= record.Plan.MaxPurchasesPerUser {
			return Order{}, ErrPurchaseLimit
		}
	}
	order, err := scanOrder(tx.QueryRow(ctx, `
INSERT INTO subscription_orders (
	tenant_id, user_id, plan_id, recharge_order_id, trade_no, status,
	price, currency_code, provider, plan_code, plan_name,
	duration_unit, duration_value, duration_seconds,
	quota_limit, quota_reset_period, quota_reset_interval_seconds,
	created_at, updated_at
) VALUES (
	$1, $2, $3, $4, $5, 'pending',
	$6, $7, $8, $9, $10,
	$11, $12, $13,
	$14, $15, $16,
	$17, $17
)
RETURNING id, tenant_id, user_id, plan_id, recharge_order_id, trade_no, status,
	price, currency_code, provider, plan_code, plan_name,
	duration_unit, duration_value, duration_seconds,
	quota_limit, quota_reset_period, quota_reset_interval_seconds,
	created_at, paid_at, activated_at, updated_at`,
		record.TenantID, record.UserID, record.Plan.ID, record.RechargeOrderID, record.TradeNo,
		record.Plan.Price, record.Plan.CurrencyCode, record.Provider, record.Plan.Code, record.Plan.Name,
		record.Plan.DurationUnit, record.Plan.DurationValue, record.Plan.DurationSeconds,
		record.Plan.QuotaLimit, record.Plan.QuotaResetPeriod, record.Plan.QuotaResetIntervalSeconds,
		record.Now,
	))
	if err != nil {
		if isUniqueViolation(err) {
			return Order{}, ErrOrderStateConflict
		}
		return Order{}, fmt.Errorf("subscription: insert order: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, fmt.Errorf("subscription: commit create order: %w", err)
	}
	return order, nil
}
