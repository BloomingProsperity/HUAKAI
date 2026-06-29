// HUAKAI · iKun

package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// AssignSubscription 的 SERIALIZABLE 事务瞬时冲突最大重试次数。
const subscriptionTxRetryAttempts = 5

// quota cost_usd 策略默认优先级 (与 quota_policies.priority 默认一致)。
const subscriptionPolicyPriority = 100

const planSelectColumns = `
	id, tenant_id, name, description, price_cents, currency_code, validity_days,
	granted_group, daily_cap_usd, weekly_cap_usd, monthly_cap_usd,
	for_sale, enabled, sort_order, created_at, updated_at`

const subscriptionSelectColumns = `
	id, tenant_id, user_id, plan_id, granted_group,
	daily_cap_usd, weekly_cap_usd, monthly_cap_usd,
	status, source, auto_renew, assigned_by_admin_id, prev_user_group,
	starts_at, expires_at, cancelled_at, created_at, updated_at`

// subscriptionSelectColumnsS 是 subscriptionSelectColumns 的 s. 限定版, 用于 UPDATE ... FROM
// 带 CTE/join 的 RETURNING 子句: 此时 target CTE 也暴露 id, 裸 id 会触发 42702 列引用歧义。
const subscriptionSelectColumnsS = `
	s.id, s.tenant_id, s.user_id, s.plan_id, s.granted_group,
	s.daily_cap_usd, s.weekly_cap_usd, s.monthly_cap_usd,
	s.status, s.source, s.auto_renew, s.assigned_by_admin_id, s.prev_user_group,
	s.starts_at, s.expires_at, s.cancelled_at, s.created_at, s.updated_at`

// PostgresStore 订阅权威存储。配额策略写入共享的 quota_policies 表 (不 import internal/quota,
// 与 payment 写 billing_events 同一"共享表 seam"模式), quota 引擎只读解析这些策略。
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore 构造 PG 订阅存储。
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

var _ Store = (*PostgresStore)(nil)

// ---- plan 目录 ----

func (s *PostgresStore) CreatePlan(ctx context.Context, rec createPlanRecord) (Plan, error) {
	if s == nil || s.pool == nil {
		return Plan{}, ErrStoreNotConfigured
	}
	daily, err := capParam(rec.DailyCapUSD)
	if err != nil {
		return Plan{}, ErrInvalidInput
	}
	weekly, err := capParam(rec.WeeklyCapUSD)
	if err != nil {
		return Plan{}, ErrInvalidInput
	}
	monthly, err := capParam(rec.MonthlyCapUSD)
	if err != nil {
		return Plan{}, ErrInvalidInput
	}
	row := s.pool.QueryRow(ctx, `
INSERT INTO subscription_plans (
	tenant_id, name, description, price_cents, currency_code, validity_days,
	granted_group, daily_cap_usd, weekly_cap_usd, monthly_cap_usd,
	for_sale, enabled, sort_order, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, true, $12, $13, $13)
RETURNING`+planSelectColumns,
		rec.TenantID, rec.Name, rec.Description, rec.PriceCents, rec.CurrencyCode, rec.ValidityDays,
		rec.GrantedGroup, daily, weekly, monthly, rec.ForSale, rec.SortOrder, rec.Now)
	plan, err := scanPlan(row)
	if err != nil {
		return Plan{}, fmt.Errorf("subscription: create plan: %w", err)
	}
	return plan, nil
}

func (s *PostgresStore) GetPlan(ctx context.Context, tenantID, planID int64) (Plan, error) {
	if s == nil || s.pool == nil {
		return Plan{}, ErrStoreNotConfigured
	}
	row := s.pool.QueryRow(ctx, `SELECT`+planSelectColumns+`
FROM subscription_plans WHERE tenant_id=$1 AND id=$2`, tenantID, planID)
	plan, err := scanPlan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, ErrPlanNotFound
	}
	if err != nil {
		return Plan{}, fmt.Errorf("subscription: get plan: %w", err)
	}
	return plan, nil
}

func (s *PostgresStore) ListPlans(ctx context.Context, tenantID int64, onlyForSale bool) ([]Plan, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	query := `SELECT` + planSelectColumns + `
FROM subscription_plans WHERE tenant_id=$1`
	if onlyForSale {
		query += ` AND for_sale=true AND enabled=true`
	}
	query += ` ORDER BY sort_order, id`
	rows, err := s.pool.Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("subscription: list plans: %w", err)
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, plan)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DisablePlan(ctx context.Context, tenantID, planID int64) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE subscription_plans SET enabled=false, for_sale=false, updated_at=now()
WHERE tenant_id=$1 AND id=$2`, tenantID, planID)
	if err != nil {
		return fmt.Errorf("subscription: disable plan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPlanNotFound
	}
	return nil
}

// ---- 订阅授予 (核心事务) ----

func (s *PostgresStore) AssignSubscription(ctx context.Context, rec assignRecord) (AssignResult, error) {
	if s == nil || s.pool == nil {
		return AssignResult{}, ErrStoreNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < subscriptionTxRetryAttempts; attempt++ {
		res, err := s.assignOnce(ctx, rec)
		if err == nil {
			return res, nil
		}
		if isPgRetryableTxConflict(err) {
			lastErr = err
			continue
		}
		// 并发 tx 已为同 (user, granted_group) 建了 active 订阅 (partial unique 命中) → 幂等返回现有。
		if isUniqueViolation(err) {
			return s.readActiveAsIdempotent(ctx, rec)
		}
		return AssignResult{}, err
	}
	return AssignResult{}, fmt.Errorf("subscription: assign exhausted retries: %w", lastErr)
}

func (s *PostgresStore) assignOnce(ctx context.Context, rec assignRecord) (AssignResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AssignResult{}, fmt.Errorf("subscription: begin assign: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	plan, err := getPlanTx(ctx, tx, rec.TenantID, rec.PlanID)
	if err != nil {
		return AssignResult{}, err
	}
	if !plan.Enabled {
		return AssignResult{}, ErrPlanDisabled
	}

	// 先锁用户行: 串行化同一用户的并发授予/分组改动 (即使 READ COMMITTED 也不串组)。
	prevGroup, err := lockUserGroupTx(ctx, tx, rec.TenantID, rec.UserID)
	if err != nil {
		return AssignResult{}, err
	}

	// 幂等: 同 (tenant, user, granted_group) 已有 active 订阅 → 不重复授予。
	if existing, ok, err := getActiveByGroupForUpdateTx(ctx, tx, rec.TenantID, rec.UserID, plan.GrantedGroup); err != nil {
		return AssignResult{}, err
	} else if ok {
		if err := insertSubAuditTx(ctx, tx, subAuditInsert{
			TenantID:           rec.TenantID,
			UserSubscriptionID: existing.ID,
			EventType:          AuditIdempotentReplay,
			ActorKind:          ActorKindAdmin,
			ActorID:            rec.ActorAdminID,
			RequestID:          rec.RequestID,
			Payload:            map[string]any{"plan_id": rec.PlanID},
			Now:                rec.Now,
		}); err != nil {
			return AssignResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return AssignResult{}, fmt.Errorf("subscription: commit idempotent assign: %w", err)
		}
		return AssignResult{Subscription: existing, Idempotent: true}, nil
	}

	expiresAt := rec.Now.AddDate(0, 0, plan.ValidityDays)
	sub := UserSubscription{
		TenantID:          rec.TenantID,
		UserID:            rec.UserID,
		PlanID:            plan.ID,
		GrantedGroup:      plan.GrantedGroup,
		DailyCapUSD:       plan.DailyCapUSD,
		WeeklyCapUSD:      plan.WeeklyCapUSD,
		MonthlyCapUSD:     plan.MonthlyCapUSD,
		Status:            StatusActive,
		Source:            SourceAdmin,
		AutoRenew:         true,
		AssignedByAdminID: rec.ActorAdminID,
		PrevUserGroup:     prevGroup,
		StartsAt:          rec.Now,
		ExpiresAt:         expiresAt,
	}
	sub, err = insertSubscriptionTx(ctx, tx, sub, rec.Now)
	if err != nil {
		return AssignResult{}, err // 23505 由外层当幂等处理
	}

	if err := installCapsTx(ctx, tx, sub, rec.Now); err != nil {
		return AssignResult{}, err
	}

	// 升级用户分组 (granted_group 非空且与现组不同时)。
	if plan.GrantedGroup != "" && plan.GrantedGroup != prevGroup {
		if _, err := tx.Exec(ctx, `UPDATE users SET user_group=$3 WHERE tenant_id=$1 AND id=$2`,
			rec.TenantID, rec.UserID, plan.GrantedGroup); err != nil {
			return AssignResult{}, fmt.Errorf("subscription: upgrade user group: %w", err)
		}
		if err := insertSubAuditTx(ctx, tx, subAuditInsert{
			TenantID:           rec.TenantID,
			UserSubscriptionID: sub.ID,
			EventType:          AuditGroupUpgraded,
			ActorKind:          ActorKindAdmin,
			ActorID:            rec.ActorAdminID,
			RequestID:          rec.RequestID,
			Payload:            map[string]any{"from": prevGroup, "to": plan.GrantedGroup},
			Now:                rec.Now,
		}); err != nil {
			return AssignResult{}, err
		}
	}

	if err := insertSubAuditTx(ctx, tx, subAuditInsert{
		TenantID:           rec.TenantID,
		UserSubscriptionID: sub.ID,
		EventType:          AuditSubscriptionCreated,
		ActorKind:          ActorKindAdmin,
		ActorID:            rec.ActorAdminID,
		RequestID:          rec.RequestID,
		Payload:            assignAuditPayload(sub),
		Now:                rec.Now,
	}); err != nil {
		return AssignResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return AssignResult{}, fmt.Errorf("subscription: commit assign: %w", err)
	}
	return AssignResult{Subscription: sub, Idempotent: false}, nil
}

// readActiveAsIdempotent 在并发授予命中唯一冲突后, 以新事务读现有 active 订阅并记一次幂等审计。
func (s *PostgresStore) readActiveAsIdempotent(ctx context.Context, rec assignRecord) (AssignResult, error) {
	plan, err := s.GetPlan(ctx, rec.TenantID, rec.PlanID)
	if err != nil {
		return AssignResult{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AssignResult{}, fmt.Errorf("subscription: begin idempotent reread: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	existing, ok, err := getActiveByGroupForUpdateTx(ctx, tx, rec.TenantID, rec.UserID, plan.GrantedGroup)
	if err != nil {
		return AssignResult{}, err
	}
	if !ok {
		return AssignResult{}, ErrSubscriptionNotFound
	}
	if err := insertSubAuditTx(ctx, tx, subAuditInsert{
		TenantID:           rec.TenantID,
		UserSubscriptionID: existing.ID,
		EventType:          AuditIdempotentReplay,
		ActorKind:          ActorKindAdmin,
		ActorID:            rec.ActorAdminID,
		RequestID:          rec.RequestID,
		Payload:            map[string]any{"plan_id": rec.PlanID},
		Now:                rec.Now,
	}); err != nil {
		return AssignResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AssignResult{}, fmt.Errorf("subscription: commit idempotent reread: %w", err)
	}
	return AssignResult{Subscription: existing, Idempotent: true}, nil
}

func (s *PostgresStore) GetSubscription(ctx context.Context, tenantID, subscriptionID int64) (UserSubscription, error) {
	if s == nil || s.pool == nil {
		return UserSubscription{}, ErrStoreNotConfigured
	}
	row := s.pool.QueryRow(ctx, `SELECT`+subscriptionSelectColumns+`
FROM user_subscriptions WHERE tenant_id=$1 AND id=$2`, tenantID, subscriptionID)
	sub, err := scanSubscription(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserSubscription{}, ErrSubscriptionNotFound
	}
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: get subscription: %w", err)
	}
	return sub, nil
}

func (s *PostgresStore) ListUserSubscriptions(ctx context.Context, tenantID, userID int64) ([]UserSubscription, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `SELECT`+subscriptionSelectColumns+`
FROM user_subscriptions WHERE tenant_id=$1 AND user_id=$2
ORDER BY created_at DESC, id DESC`, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("subscription: list user subscriptions: %w", err)
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

func (s *PostgresStore) ListUserSubscriptionsByGroup(ctx context.Context, tenantID int64, grantedGroup string, limit int) ([]UserSubscription, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `SELECT`+subscriptionSelectColumns+`
FROM user_subscriptions
WHERE tenant_id=$1 AND granted_group=$2
ORDER BY id
LIMIT $3`, tenantID, grantedGroup, limit)
	if err != nil {
		return nil, fmt.Errorf("subscription: list subscriptions by group: %w", err)
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

func (s *PostgresStore) SetAutoRenew(ctx context.Context, tenantID, userID int64, autoRenew bool) (UserSubscription, error) {
	if s == nil || s.pool == nil {
		return UserSubscription{}, ErrStoreNotConfigured
	}
	row := s.pool.QueryRow(ctx, `
WITH target AS (
	SELECT id FROM user_subscriptions
	WHERE tenant_id=$1 AND user_id=$2 AND status='active'
	ORDER BY expires_at DESC, id DESC
	LIMIT 1
)
UPDATE user_subscriptions s
SET auto_renew=$3, updated_at=now()
FROM target
WHERE s.tenant_id=$1 AND s.id=target.id
RETURNING`+subscriptionSelectColumnsS, tenantID, userID, autoRenew)
	sub, err := scanSubscription(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserSubscription{}, ErrSubscriptionNotFound
	}
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: set auto renew: %w", err)
	}
	return sub, nil
}

// ---- 生命周期: 取消 / 到期 (共用 closeSubscriptionOnce) ----

func (s *PostgresStore) CancelSubscription(ctx context.Context, rec lifecycleRecord) (UserSubscription, error) {
	return s.closeSubscription(ctx, rec, StatusCancelled, AuditCancelled)
}

func (s *PostgresStore) ExpireSubscription(ctx context.Context, rec lifecycleRecord) (UserSubscription, error) {
	return s.closeSubscription(ctx, rec, StatusExpired, AuditExpired)
}

func (s *PostgresStore) closeSubscription(ctx context.Context, rec lifecycleRecord, terminal SubscriptionStatus, event string) (UserSubscription, error) {
	if s == nil || s.pool == nil {
		return UserSubscription{}, ErrStoreNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < subscriptionTxRetryAttempts; attempt++ {
		sub, err := s.closeSubscriptionOnce(ctx, rec, terminal, event)
		if err == nil {
			return sub, nil
		}
		if isPgRetryableTxConflict(err) {
			lastErr = err
			continue
		}
		return UserSubscription{}, err
	}
	return UserSubscription{}, fmt.Errorf("subscription: close exhausted retries: %w", lastErr)
}

func (s *PostgresStore) closeSubscriptionOnce(ctx context.Context, rec lifecycleRecord, terminal SubscriptionStatus, event string) (UserSubscription, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: begin close: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sub, err := getSubscriptionForUpdateTx(ctx, tx, rec.TenantID, rec.SubscriptionID)
	if err != nil {
		return UserSubscription{}, err
	}
	// 幂等: 已是终态直接返回, 不重复关策略/降级/审计。
	if sub.Status != StatusActive {
		if err := tx.Commit(ctx); err != nil {
			return UserSubscription{}, fmt.Errorf("subscription: commit no-op close: %w", err)
		}
		return sub, nil
	}

	var cancelledAt any
	if terminal == StatusCancelled {
		cancelledAt = rec.Now
	}
	row := tx.QueryRow(ctx, `
UPDATE user_subscriptions SET status=$3, cancelled_at=$4, updated_at=$5
WHERE tenant_id=$1 AND id=$2 AND status='active'
RETURNING`+subscriptionSelectColumns,
		rec.TenantID, rec.SubscriptionID, string(terminal), cancelledAt, rec.Now)
	sub, err = scanSubscription(row)
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: close update: %w", err)
	}

	if err := closeCapsTx(ctx, tx, rec.TenantID, sub.ID, rec.Now); err != nil {
		return UserSubscription{}, err
	}

	// 降级守卫: 仅当用户当前组仍是本订阅授予的组时才动 (有更新升级把用户移到别组则不动)。
	// 不盲目还原 prev_user_group: 链式升级 (default->basic->premium) 下 prev 可能指向已到期的组,
	// 还原会把用户留在无 active 订阅支撑的付费组。改为从剩余 active 订阅重算目标组, 无则回 default。
	if sub.GrantedGroup != "" {
		currentGroup, err := lockUserGroupTx(ctx, tx, rec.TenantID, sub.UserID)
		if err != nil {
			return UserSubscription{}, err
		}
		if currentGroup == sub.GrantedGroup {
			targetGroup, err := resolveGroupFromActiveTx(ctx, tx, rec.TenantID, sub.UserID)
			if err != nil {
				return UserSubscription{}, err
			}
			if targetGroup != currentGroup {
				if _, err := tx.Exec(ctx, `UPDATE users SET user_group=$3 WHERE tenant_id=$1 AND id=$2`,
					rec.TenantID, sub.UserID, targetGroup); err != nil {
					return UserSubscription{}, fmt.Errorf("subscription: downgrade user group: %w", err)
				}
				if err := insertSubAuditTx(ctx, tx, subAuditInsert{
					TenantID:           rec.TenantID,
					UserSubscriptionID: sub.ID,
					EventType:          AuditGroupDowngraded,
					ActorKind:          actorKindOrDefault(rec.ActorKind),
					ActorID:            rec.ActorID,
					RequestID:          rec.RequestID,
					Payload:            map[string]any{"from": currentGroup, "to": targetGroup},
					Now:                rec.Now,
				}); err != nil {
					return UserSubscription{}, err
				}
			}
		}
	}

	if err := insertSubAuditTx(ctx, tx, subAuditInsert{
		TenantID:           rec.TenantID,
		UserSubscriptionID: sub.ID,
		EventType:          event,
		ActorKind:          actorKindOrDefault(rec.ActorKind),
		ActorID:            rec.ActorID,
		RequestID:          rec.RequestID,
		Now:                rec.Now,
	}); err != nil {
		return UserSubscription{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: commit close: %w", err)
	}
	return sub, nil
}

// ListDueExpiry 跨租户扫到点 active 订阅 (worker 用; 返回行带 TenantID 供逐条处理)。
func (s *PostgresStore) ListDueExpiry(ctx context.Context, now time.Time, limit int) ([]UserSubscription, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	rows, err := s.pool.Query(ctx, `SELECT`+subscriptionSelectColumns+`
FROM user_subscriptions WHERE status='active' AND expires_at <= $1
ORDER BY expires_at, id LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("subscription: list due expiry: %w", err)
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

func (s *PostgresStore) ListAuditEvents(ctx context.Context, tenantID, subscriptionID int64) ([]AuditEvent, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, user_subscription_id, event_type, actor_kind, actor_id,
	COALESCE(reason_class, ''), COALESCE(request_id, ''), redacted_payload, occurred_at
FROM subscription_audit_events
WHERE tenant_id=$1 AND user_subscription_id=$2
ORDER BY occurred_at, id`, tenantID, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("subscription: list audit events: %w", err)
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var ev AuditEvent
		var actorID pgtype.Int8
		var payload []byte
		if err := rows.Scan(&ev.ID, &ev.TenantID, &ev.UserSubscriptionID, &ev.EventType, &ev.ActorKind,
			&actorID, &ev.ReasonClass, &ev.RequestID, &payload, &ev.OccurredAt); err != nil {
			return nil, err
		}
		if actorID.Valid {
			ev.ActorID = actorID.Int64
		}
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &ev.Payload)
		}
		ev.OccurredAt = ev.OccurredAt.UTC()
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ---- 到期提醒 (P3b-1) ----

// ListDueReminder 扫 active 且 expires_at 在 (now, now+within] 且 (expires_at, id) 大于游标的订阅,
// 按 (expires_at, id) 升序限量返回, 附用户邮箱与套餐名。游标用行值比较 (expires_at, id) > ($4, $5),
// 零值游标 (year-1, 0) 命中全部, 调用方逐页推进翻完整窗口。
// INNER JOIN users 过滤已删用户 (不给删号发提醒); email 为空时 RecipientEmail 返回空串 (上层记 skipped)。
func (s *PostgresStore) ListDueReminder(ctx context.Context, now time.Time, within time.Duration, after ReminderCursor, limit int) ([]ReminderCandidate, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	if within <= 0 {
		within = 7 * 24 * time.Hour
	}
	upper := now.Add(within)
	rows, err := s.pool.Query(ctx, `
SELECT s.tenant_id, s.id, s.user_id, s.expires_at,
	COALESCE(u.email, ''), COALESCE(p.name, '')
FROM user_subscriptions s
JOIN users u ON u.tenant_id = s.tenant_id AND u.id = s.user_id AND u.deleted_at IS NULL
LEFT JOIN subscription_plans p ON p.tenant_id = s.tenant_id AND p.id = s.plan_id
WHERE s.status = 'active' AND s.expires_at > $1 AND s.expires_at <= $2
	AND (s.expires_at, s.id) > ($4, $5)
ORDER BY s.expires_at, s.id
LIMIT $3`, now, upper, limit, after.ExpiresAt, after.ID)
	if err != nil {
		return nil, fmt.Errorf("subscription: list due reminder: %w", err)
	}
	defer rows.Close()
	var out []ReminderCandidate
	for rows.Next() {
		var c ReminderCandidate
		if err := rows.Scan(&c.TenantID, &c.SubscriptionID, &c.UserID, &c.ExpiresAt,
			&c.RecipientEmail, &c.PlanName); err != nil {
			return nil, err
		}
		c.ExpiresAt = c.ExpiresAt.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// SentReminderKeys 返回某订阅已记录的提醒档位集合 (任意 status)。
func (s *PostgresStore) SentReminderKeys(ctx context.Context, tenantID, subscriptionID int64) (map[string]struct{}, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT reminder_key FROM subscription_expiry_reminders
WHERE tenant_id=$1 AND user_subscription_id=$2`, tenantID, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("subscription: sent reminder keys: %w", err)
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = struct{}{}
	}
	return out, rows.Err()
}

// RecordReminder 记一条提醒投递结果, ON CONFLICT (tenant, sub, key) DO NOTHING 幂等。
// 返回是否新插入 (false = 该档位已存在)。
func (s *PostgresStore) RecordReminder(ctx context.Context, rec reminderRecord) (bool, error) {
	if s == nil || s.pool == nil {
		return false, ErrStoreNotConfigured
	}
	tag, err := s.pool.Exec(ctx, `
INSERT INTO subscription_expiry_reminders
	(tenant_id, user_subscription_id, reminder_key, status, recipient, expires_at_snapshot)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, user_subscription_id, reminder_key) DO NOTHING`,
		rec.TenantID, rec.SubscriptionID, rec.ReminderKey, rec.Status, rec.Recipient, rec.ExpiresAt)
	if err != nil {
		return false, fmt.Errorf("subscription: record reminder: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ---- 内部事务级辅助 ----

func getPlanTx(ctx context.Context, tx pgx.Tx, tenantID, planID int64) (Plan, error) {
	row := tx.QueryRow(ctx, `SELECT`+planSelectColumns+`
FROM subscription_plans WHERE tenant_id=$1 AND id=$2`, tenantID, planID)
	plan, err := scanPlan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, ErrPlanNotFound
	}
	if err != nil {
		return Plan{}, fmt.Errorf("subscription: lock plan: %w", err)
	}
	return plan, nil
}

// lockUserGroupTx 锁住用户行并返回当前分组; 用户不存在返回 ErrInvalidInput。
func lockUserGroupTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64) (string, error) {
	var group string
	err := tx.QueryRow(ctx, `SELECT user_group FROM users WHERE tenant_id=$1 AND id=$2 FOR UPDATE`,
		tenantID, userID).Scan(&group)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalidInput
	}
	if err != nil {
		return "", fmt.Errorf("subscription: lock user: %w", err)
	}
	return group, nil
}

func getActiveByGroupForUpdateTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64, group string) (UserSubscription, bool, error) {
	row := tx.QueryRow(ctx, `SELECT`+subscriptionSelectColumns+`
FROM user_subscriptions
WHERE tenant_id=$1 AND user_id=$2 AND granted_group=$3 AND status='active'
FOR UPDATE`, tenantID, userID, group)
	sub, err := scanSubscription(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserSubscription{}, false, nil
	}
	if err != nil {
		return UserSubscription{}, false, fmt.Errorf("subscription: lookup active subscription: %w", err)
	}
	return sub, true, nil
}

// resolveGroupFromActiveTx 返回该用户剩余 active 订阅中最新 (starts_at 最晚) 的非空 granted_group;
// 无则回 DefaultUserGroup。用于关闭订阅后重算正确分组, 避免还原到已失效的快照组。
func resolveGroupFromActiveTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64) (string, error) {
	var group string
	err := tx.QueryRow(ctx, `
SELECT granted_group FROM user_subscriptions
WHERE tenant_id=$1 AND user_id=$2 AND status='active' AND granted_group <> ''
ORDER BY starts_at DESC, id DESC
LIMIT 1`, tenantID, userID).Scan(&group)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultUserGroup, nil
	}
	if err != nil {
		return "", fmt.Errorf("subscription: resolve active group: %w", err)
	}
	return group, nil
}

func getSubscriptionForUpdateTx(ctx context.Context, tx pgx.Tx, tenantID, subscriptionID int64) (UserSubscription, error) {
	row := tx.QueryRow(ctx, `SELECT`+subscriptionSelectColumns+`
FROM user_subscriptions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, subscriptionID)
	sub, err := scanSubscription(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserSubscription{}, ErrSubscriptionNotFound
	}
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: lock subscription: %w", err)
	}
	return sub, nil
}

func getCurrentActiveByUserForUpdateTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64) (UserSubscription, error) {
	row := tx.QueryRow(ctx, `SELECT`+subscriptionSelectColumns+`
FROM user_subscriptions
WHERE tenant_id=$1 AND user_id=$2 AND status='active'
ORDER BY expires_at DESC, id DESC
LIMIT 1
FOR UPDATE`, tenantID, userID)
	sub, err := scanSubscription(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserSubscription{}, ErrSubscriptionNotFound
	}
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: lock current subscription: %w", err)
	}
	return sub, nil
}

func insertSubscriptionTx(ctx context.Context, tx pgx.Tx, sub UserSubscription, now time.Time) (UserSubscription, error) {
	daily, err := capParam(sub.DailyCapUSD)
	if err != nil {
		return UserSubscription{}, ErrInvalidInput
	}
	weekly, err := capParam(sub.WeeklyCapUSD)
	if err != nil {
		return UserSubscription{}, ErrInvalidInput
	}
	monthly, err := capParam(sub.MonthlyCapUSD)
	if err != nil {
		return UserSubscription{}, ErrInvalidInput
	}
	row := tx.QueryRow(ctx, `
INSERT INTO user_subscriptions (
	tenant_id, user_id, plan_id, granted_group,
	daily_cap_usd, weekly_cap_usd, monthly_cap_usd,
	status, source, auto_renew, assigned_by_admin_id, prev_user_group,
	starts_at, expires_at, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $15)
RETURNING`+subscriptionSelectColumns,
		sub.TenantID, sub.UserID, sub.PlanID, sub.GrantedGroup,
		daily, weekly, monthly,
		string(sub.Status), string(sub.Source), sub.AutoRenew, nullableInt64(sub.AssignedByAdminID), sub.PrevUserGroup,
		sub.StartsAt, sub.ExpiresAt, now)
	return scanSubscription(row)
}

// installCapsTx 为订阅每档非空 cap 装一条 quota cost_usd 日历窗口策略, 并记 policy link。
func renewSubscriptionTx(ctx context.Context, tx pgx.Tx, existing UserSubscription, plan Plan, newExpires, now time.Time) (UserSubscription, error) {
	daily, err := capParam(plan.DailyCapUSD)
	if err != nil {
		return UserSubscription{}, ErrInvalidInput
	}
	weekly, err := capParam(plan.WeeklyCapUSD)
	if err != nil {
		return UserSubscription{}, ErrInvalidInput
	}
	monthly, err := capParam(plan.MonthlyCapUSD)
	if err != nil {
		return UserSubscription{}, ErrInvalidInput
	}
	row := tx.QueryRow(ctx, `
UPDATE user_subscriptions
SET plan_id=$3, granted_group=$4, daily_cap_usd=$5, weekly_cap_usd=$6, monthly_cap_usd=$7, expires_at=$8, updated_at=$9
WHERE tenant_id=$1 AND id=$2 AND status='active'
RETURNING`+subscriptionSelectColumns,
		existing.TenantID, existing.ID, plan.ID, plan.GrantedGroup, daily, weekly, monthly, newExpires, now)
	sub, err := scanSubscription(row)
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: renew update: %w", err)
	}
	return sub, nil
}

const fulfillmentEffectColumns = `
	id, tenant_id, source_kind, payment_order_id, voucher_redemption_id,
	user_id, plan_id, user_subscription_id, result_kind, applied_validity_days,
	prev_expires_at, new_expires_at, reversal_state, reversed_at, created_at`

// insertFulfillmentEffectTx 写一条订阅履约效果行。撞 (tenant, order_id) / (tenant, redemption_id)
// 部分唯一索引返回 23505 (由调用方的幂等预检/重试处理), 本函数不吞冲突。
func insertFulfillmentEffectTx(ctx context.Context, tx pgx.Tx, e FulfillmentEffect) (FulfillmentEffect, error) {
	row := tx.QueryRow(ctx, `
INSERT INTO subscription_fulfillment_effects (
	tenant_id, source_kind, payment_order_id, voucher_redemption_id,
	user_id, plan_id, user_subscription_id, result_kind, applied_validity_days,
	prev_expires_at, new_expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING`+fulfillmentEffectColumns,
		e.TenantID, e.SourceKind, ptrInt64Param(e.PaymentOrderID), ptrInt64Param(e.VoucherRedemptionID),
		e.UserID, e.PlanID, e.UserSubscriptionID, e.ResultKind, e.AppliedValidityDays,
		ptrTimeParam(e.PrevExpiresAt), e.NewExpiresAt)
	return scanFulfillmentEffect(row)
}

// getFulfillmentEffectByOrderTx 查某支付订单是否已有履约效果 (调用方完成态幂等重放读)。
func getFulfillmentEffectByOrderTx(ctx context.Context, tx pgx.Tx, tenantID, orderID int64) (FulfillmentEffect, bool, error) {
	row := tx.QueryRow(ctx, `SELECT`+fulfillmentEffectColumns+`
FROM subscription_fulfillment_effects WHERE tenant_id=$1 AND payment_order_id=$2`, tenantID, orderID)
	return scanEffectOptional(row)
}

// getFulfillmentEffectByVoucherTx 查某兑换是否已有履约效果。
func getFulfillmentEffectByVoucherTx(ctx context.Context, tx pgx.Tx, tenantID, redemptionID int64) (FulfillmentEffect, bool, error) {
	row := tx.QueryRow(ctx, `SELECT`+fulfillmentEffectColumns+`
FROM subscription_fulfillment_effects WHERE tenant_id=$1 AND voucher_redemption_id=$2`, tenantID, redemptionID)
	return scanEffectOptional(row)
}

func scanEffectOptional(row pgx.Row) (FulfillmentEffect, bool, error) {
	e, err := scanFulfillmentEffect(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FulfillmentEffect{}, false, nil
	}
	if err != nil {
		return FulfillmentEffect{}, false, err
	}
	return e, true, nil
}

func scanFulfillmentEffect(row pgx.Row) (FulfillmentEffect, error) {
	var e FulfillmentEffect
	var orderID, redemptionID pgtype.Int8
	var prevExpires, reversedAt pgtype.Timestamptz
	if err := row.Scan(&e.ID, &e.TenantID, &e.SourceKind, &orderID, &redemptionID,
		&e.UserID, &e.PlanID, &e.UserSubscriptionID, &e.ResultKind, &e.AppliedValidityDays,
		&prevExpires, &e.NewExpiresAt, &e.ReversalState, &reversedAt, &e.CreatedAt); err != nil {
		return FulfillmentEffect{}, err
	}
	if orderID.Valid {
		v := orderID.Int64
		e.PaymentOrderID = &v
	}
	if redemptionID.Valid {
		v := redemptionID.Int64
		e.VoucherRedemptionID = &v
	}
	if prevExpires.Valid {
		t := prevExpires.Time.UTC()
		e.PrevExpiresAt = &t
	}
	if reversedAt.Valid {
		t := reversedAt.Time.UTC()
		e.ReversedAt = &t
	}
	e.NewExpiresAt = e.NewExpiresAt.UTC()
	e.CreatedAt = e.CreatedAt.UTC()
	return e, nil
}

// ptrInt64Param / ptrTimeParam: 指针为 nil 时传 NULL。
func ptrInt64Param(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func ptrTimeParam(v *time.Time) any {
	if v == nil {
		return nil
	}
	return *v
}

type subAuditInsert struct {
	TenantID           int64
	UserSubscriptionID int64
	EventType          string
	ActorKind          string
	ActorID            int64
	ReasonClass        string
	RequestID          string
	Payload            map[string]any
	Now                time.Time
}

func insertSubAuditTx(ctx context.Context, tx pgx.Tx, ev subAuditInsert) error {
	var raw []byte
	if len(ev.Payload) > 0 {
		raw, _ = json.Marshal(ev.Payload)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO subscription_audit_events (
	tenant_id, user_subscription_id, event_type, actor_kind, actor_id,
	reason_class, request_id, redacted_payload, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		ev.TenantID, ev.UserSubscriptionID, ev.EventType, actorKindOrDefault(ev.ActorKind),
		nullableInt64(ev.ActorID), nullableText(ev.ReasonClass), nullableText(ev.RequestID),
		nullableJSON(raw), ev.Now); err != nil {
		return fmt.Errorf("subscription: insert audit event: %w", err)
	}
	return nil
}

func assignAuditPayload(sub UserSubscription) map[string]any {
	p := map[string]any{
		"plan_id":       sub.PlanID,
		"granted_group": sub.GrantedGroup,
		"expires_at":    sub.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if sub.DailyCapUSD != nil {
		p["daily_cap_usd"] = sub.DailyCapUSD.String()
	}
	if sub.WeeklyCapUSD != nil {
		p["weekly_cap_usd"] = sub.WeeklyCapUSD.String()
	}
	if sub.MonthlyCapUSD != nil {
		p["monthly_cap_usd"] = sub.MonthlyCapUSD.String()
	}
	return p
}

// ---- 行扫描 ----

func scanPlan(row rowScanner) (Plan, error) {
	var p Plan
	var daily, weekly, monthly pgtype.Numeric
	if err := row.Scan(
		&p.ID, &p.TenantID, &p.Name, &p.Description, &p.PriceCents, &p.CurrencyCode, &p.ValidityDays,
		&p.GrantedGroup, &daily, &weekly, &monthly,
		&p.ForSale, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return Plan{}, err
	}
	p.CurrencyCode = strings.TrimSpace(p.CurrencyCode)
	p.DailyCapUSD = decodeCap(daily)
	p.WeeklyCapUSD = decodeCap(weekly)
	p.MonthlyCapUSD = decodeCap(monthly)
	p.CreatedAt = p.CreatedAt.UTC()
	p.UpdatedAt = p.UpdatedAt.UTC()
	return p, nil
}

func scanSubscription(row rowScanner) (UserSubscription, error) {
	var s UserSubscription
	var status, source string
	var daily, weekly, monthly pgtype.Numeric
	var assignedBy pgtype.Int8
	var cancelledAt pgtype.Timestamptz
	if err := row.Scan(
		&s.ID, &s.TenantID, &s.UserID, &s.PlanID, &s.GrantedGroup,
		&daily, &weekly, &monthly,
		&status, &source, &s.AutoRenew, &assignedBy, &s.PrevUserGroup,
		&s.StartsAt, &s.ExpiresAt, &cancelledAt, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		return UserSubscription{}, err
	}
	s.Status = SubscriptionStatus(status)
	s.Source = Source(source)
	s.DailyCapUSD = decodeCap(daily)
	s.WeeklyCapUSD = decodeCap(weekly)
	s.MonthlyCapUSD = decodeCap(monthly)
	if assignedBy.Valid {
		s.AssignedByAdminID = assignedBy.Int64
	}
	if cancelledAt.Valid {
		t := cancelledAt.Time.UTC()
		s.CancelledAt = &t
	}
	s.StartsAt = s.StartsAt.UTC()
	s.ExpiresAt = s.ExpiresAt.UTC()
	s.CreatedAt = s.CreatedAt.UTC()
	s.UpdatedAt = s.UpdatedAt.UTC()
	return s, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

// ---- numeric / nullable / 错误辅助 ----

func encodeNumeric(d decimal.Decimal) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(d.String()); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("subscription: encode numeric %s: %w", d.String(), err)
	}
	return n, nil
}

// capParam 把可空 cap 编码为参数: nil → NULL (Valid=false), 否则编码数值 (0 是有效的"封顶为0")。
func capParam(d *decimal.Decimal) (pgtype.Numeric, error) {
	if d == nil {
		return pgtype.Numeric{}, nil
	}
	return encodeNumeric(*d)
}

// decodeCap 把 numeric 还原为 *decimal.Decimal: NULL → nil (不设限), 否则保留值 (含 0)。
func decodeCap(n pgtype.Numeric) *decimal.Decimal {
	if !n.Valid || n.Int == nil {
		return nil
	}
	d := decimal.NewFromBigInt(n.Int, n.Exp)
	return &d
}

func actorKindOrDefault(kind string) string {
	switch kind {
	case ActorKindAdmin, ActorKindUser, ActorKindSystem:
		return kind
	default:
		return ActorKindSystem
	}
}

func nullableInt64(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

func nullableText(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullableJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isPgRetryableTxConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}
