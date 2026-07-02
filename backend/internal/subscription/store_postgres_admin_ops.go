// HUAKAI · iKun

package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func (s *PostgresStore) UpdatePlan(ctx context.Context, rec updatePlanRecord) (Plan, error) {
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
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Plan{}, fmt.Errorf("subscription: begin update plan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `
UPDATE subscription_plans
SET name=$3, description=$4, price_cents=$5, currency_code=$6, validity_days=$7,
	granted_group=$8, daily_cap_usd=$9, weekly_cap_usd=$10, monthly_cap_usd=$11,
	for_sale=$12, sort_order=$13, updated_at=$14
WHERE tenant_id=$1 AND id=$2
RETURNING`+planSelectColumns,
		rec.TenantID, rec.PlanID, rec.Name, rec.Description, rec.PriceCents, rec.CurrencyCode, rec.ValidityDays,
		rec.GrantedGroup, daily, weekly, monthly, rec.ForSale, rec.SortOrder, rec.Now)
	plan, err := scanPlan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, ErrPlanNotFound
	}
	if err != nil {
		return Plan{}, fmt.Errorf("subscription: update plan: %w", err)
	}
	if err := insertPlanAuditTx(ctx, tx, planAuditInsert{
		TenantID: rec.TenantID, PlanID: plan.ID, EventType: AuditSubscriptionPlanUpdated,
		ActorKind: ActorKindAdmin, ActorID: rec.ActorAdminID, ActorRef: rec.ActorRef, RequestID: rec.RequestID,
		Payload: map[string]any{
			"name":            plan.Name,
			"price_cents":     plan.PriceCents,
			"validity_days":   plan.ValidityDays,
			"granted_group":   plan.GrantedGroup,
			"daily_cap_usd":   capAuditString(plan.DailyCapUSD),
			"weekly_cap_usd":  capAuditString(plan.WeeklyCapUSD),
			"monthly_cap_usd": capAuditString(plan.MonthlyCapUSD),
			"for_sale":        plan.ForSale,
			"sort_order":      plan.SortOrder,
		},
		Now: rec.Now,
	}); err != nil {
		return Plan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Plan{}, fmt.Errorf("subscription: commit update plan: %w", err)
	}
	return plan, nil
}

func (s *PostgresStore) ExtendSubscription(ctx context.Context, rec extendRecord) (UserSubscription, error) {
	if s == nil || s.pool == nil {
		return UserSubscription{}, ErrStoreNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < subscriptionTxRetryAttempts; attempt++ {
		sub, err := s.extendSubscriptionOnce(ctx, rec)
		if err == nil {
			return sub, nil
		}
		if isPgRetryableTxConflict(err) {
			lastErr = err
			continue
		}
		return UserSubscription{}, err
	}
	return UserSubscription{}, fmt.Errorf("subscription: extend exhausted retries: %w", lastErr)
}

func (s *PostgresStore) extendSubscriptionOnce(ctx context.Context, rec extendRecord) (UserSubscription, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: begin extend: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sub, err := getSubscriptionForUpdateTx(ctx, tx, rec.TenantID, rec.SubscriptionID)
	if err != nil {
		return UserSubscription{}, err
	}
	if ok, err := hasSubAuditRequestTx(ctx, tx, rec.TenantID, sub.ID, AuditSubscriptionExtended, rec.RequestID); err != nil {
		return UserSubscription{}, err
	} else if ok {
		if err := tx.Commit(ctx); err != nil {
			return UserSubscription{}, fmt.Errorf("subscription: commit idempotent extend: %w", err)
		}
		return sub, nil
	}
	if sub.Status != StatusActive || !sub.ExpiresAt.After(rec.Now) {
		return UserSubscription{}, ErrSubscriptionNotActive
	}
	var newExpires time.Time
	if rec.Until != nil {
		newExpires = rec.Until.UTC()
	} else {
		newExpires = sub.ExpiresAt.AddDate(0, 0, rec.Days)
	}
	newExpires = capExpiry(newExpires)
	if !newExpires.After(sub.ExpiresAt) {
		return UserSubscription{}, ErrInvalidInput
	}
	prev := sub.ExpiresAt
	row := tx.QueryRow(ctx, `
UPDATE user_subscriptions
SET expires_at=$3, updated_at=$4
WHERE tenant_id=$1 AND id=$2 AND status='active'
RETURNING`+subscriptionSelectColumns,
		rec.TenantID, sub.ID, newExpires, rec.Now)
	sub, err = scanSubscription(row)
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: extend update: %w", err)
	}
	if err := updateActiveCapsValidUntilTx(ctx, tx, rec.TenantID, sub.ID, newExpires, rec.Now); err != nil {
		return UserSubscription{}, err
	}
	if err := insertSubAuditTx(ctx, tx, subAuditInsert{
		TenantID:           rec.TenantID,
		UserSubscriptionID: sub.ID,
		EventType:          AuditSubscriptionExtended,
		ActorKind:          ActorKindAdmin,
		ActorID:            rec.ActorAdminID,
		ActorRef:           rec.ActorRef,
		RequestID:          rec.RequestID,
		Payload: map[string]any{
			"from_expires": prev.UTC(),
			"to_expires":   newExpires.UTC(),
		},
		Now: rec.Now,
	}); err != nil {
		return UserSubscription{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: commit extend: %w", err)
	}
	return sub, nil
}

func (s *PostgresStore) ResetQuota(ctx context.Context, rec lifecycleRecord) (UserSubscription, error) {
	if s == nil || s.pool == nil {
		return UserSubscription{}, ErrStoreNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < subscriptionTxRetryAttempts; attempt++ {
		sub, err := s.resetQuotaOnce(ctx, rec)
		if err == nil {
			return sub, nil
		}
		if isPgRetryableTxConflict(err) {
			lastErr = err
			continue
		}
		return UserSubscription{}, err
	}
	return UserSubscription{}, fmt.Errorf("subscription: reset quota exhausted retries: %w", lastErr)
}

func (s *PostgresStore) resetQuotaOnce(ctx context.Context, rec lifecycleRecord) (UserSubscription, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: begin reset quota: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sub, err := getSubscriptionForUpdateTx(ctx, tx, rec.TenantID, rec.SubscriptionID)
	if err != nil {
		return UserSubscription{}, err
	}
	if ok, err := hasSubAuditRequestTx(ctx, tx, rec.TenantID, sub.ID, AuditSubscriptionQuotaReset, rec.RequestID); err != nil {
		return UserSubscription{}, err
	} else if ok {
		if err := tx.Commit(ctx); err != nil {
			return UserSubscription{}, fmt.Errorf("subscription: commit idempotent reset quota: %w", err)
		}
		return sub, nil
	}
	if sub.Status != StatusActive || !sub.ExpiresAt.After(rec.Now) {
		return UserSubscription{}, ErrSubscriptionNotActive
	}
	if err := closeCapsTx(ctx, tx, rec.TenantID, sub.ID, rec.Now); err != nil {
		return UserSubscription{}, err
	}
	if err := installCapsTx(ctx, tx, sub, rec.Now); err != nil {
		return UserSubscription{}, err
	}
	row := tx.QueryRow(ctx, `
UPDATE user_subscriptions
SET updated_at=$3
WHERE tenant_id=$1 AND id=$2
RETURNING`+subscriptionSelectColumns, rec.TenantID, sub.ID, rec.Now)
	sub, err = scanSubscription(row)
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: reset quota touch subscription: %w", err)
	}
	if err := insertSubAuditTx(ctx, tx, subAuditInsert{
		TenantID:           rec.TenantID,
		UserSubscriptionID: sub.ID,
		EventType:          AuditSubscriptionQuotaReset,
		ActorKind:          actorKindOrDefault(rec.ActorKind),
		ActorID:            rec.ActorID,
		ActorRef:           rec.ActorRef,
		RequestID:          rec.RequestID,
		Payload:            assignAuditPayload(sub),
		Now:                rec.Now,
	}); err != nil {
		return UserSubscription{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: commit reset quota: %w", err)
	}
	return sub, nil
}

func (s *PostgresStore) ChangePlan(ctx context.Context, rec changePlanRecord) (UserSubscription, error) {
	if s == nil || s.pool == nil {
		return UserSubscription{}, ErrStoreNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < subscriptionTxRetryAttempts; attempt++ {
		sub, err := s.changePlanOnce(ctx, rec)
		if err == nil {
			return sub, nil
		}
		if isPgRetryableTxConflict(err) {
			lastErr = err
			continue
		}
		return UserSubscription{}, err
	}
	return UserSubscription{}, fmt.Errorf("subscription: change plan exhausted retries: %w", lastErr)
}

func (s *PostgresStore) changePlanOnce(ctx context.Context, rec changePlanRecord) (UserSubscription, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: begin change plan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sub, err := resolveChangePlanTargetTx(ctx, tx, rec)
	if err != nil {
		return UserSubscription{}, err
	}
	if ok, err := hasSubAuditRequestTx(ctx, tx, rec.TenantID, sub.ID, AuditSubscriptionRenewed, rec.RequestID); err != nil {
		return UserSubscription{}, err
	} else if ok {
		if err := tx.Commit(ctx); err != nil {
			return UserSubscription{}, fmt.Errorf("subscription: commit idempotent change plan: %w", err)
		}
		return sub, nil
	}
	if sub.Status != StatusActive || !sub.ExpiresAt.After(rec.Now) {
		return UserSubscription{}, ErrSubscriptionNotActive
	}

	plan, err := getPlanTx(ctx, tx, rec.TenantID, rec.NewPlanID)
	if err != nil {
		return UserSubscription{}, err
	}
	if !plan.Enabled {
		return UserSubscription{}, ErrPlanDisabled
	}
	if !rec.AllowDowngrade && !capsDominate(planCapsTriple(plan), subCapsTriple(sub)) {
		return UserSubscription{}, ErrDowngradeNotAllowed
	}

	currentGroup, err := lockUserGroupTx(ctx, tx, rec.TenantID, sub.UserID)
	if err != nil {
		return UserSubscription{}, err
	}
	prevGroup := sub.GrantedGroup
	prevPlanID := sub.PlanID
	prevExpires := sub.ExpiresAt
	base := rec.Now
	if sub.ExpiresAt.After(base) {
		base = sub.ExpiresAt
	}
	newExpires := capExpiry(base.AddDate(0, 0, plan.ValidityDays))

	updated, err := renewSubscriptionTx(ctx, tx, sub, plan, newExpires, rec.Now)
	if err != nil {
		return UserSubscription{}, err
	}
	// 换套餐恒发生在未过期订阅上(上方 :276 已要求 active 且 ExpiresAt>now),即恒为"期中"操作。
	// 故 caps 一律走 reconcileCapsTx 原地调和(保留 policy_id 与 quota_windows 已用计数),绝不
	// 关旧装新铸新 policy_id。修复点:自助 /change-plan 不收费、仅 capsDominate 闸(同档即放行),
	// 此前 close+install 会把当月 cost_usd 计数归零——这是与 ActivateOrRenewTx 同源的护栏绕过第二扇门
	// (用户用满月度 cap 后免费换到同档套餐即清零白吃成本)。reconcile 的"新增/移除窗口"分支天然
	// 覆盖换套餐可能的窗口集合变化。
	if err := reconcileCapsTx(ctx, tx, updated, rec.Now); err != nil {
		return UserSubscription{}, err
	}

	actorKind, actorID, actorRef := changePlanActor(rec, sub)
	if err := maybeAuditGroupChangeTx(ctx, tx, rec, sub, plan, currentGroup, prevGroup, actorKind, actorID, actorRef); err != nil {
		return UserSubscription{}, err
	}
	if err := insertSubAuditTx(ctx, tx, subAuditInsert{
		TenantID:           rec.TenantID,
		UserSubscriptionID: updated.ID,
		EventType:          AuditSubscriptionRenewed,
		ActorKind:          actorKind,
		ActorID:            actorID,
		ActorRef:           actorRef,
		RequestID:          rec.RequestID,
		Payload: map[string]any{
			"source":          "change_plan",
			"from_plan_id":    prevPlanID,
			"to_plan_id":      plan.ID,
			"from_expires":    prevExpires.UTC(),
			"to_expires":      newExpires.UTC(),
			"allow_downgrade": rec.AllowDowngrade,
		},
		Now: rec.Now,
	}); err != nil {
		return UserSubscription{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: commit change plan: %w", err)
	}
	return updated, nil
}

func (s *PostgresStore) RevokeSubscription(ctx context.Context, rec revokeRecord) (UserSubscription, error) {
	if s == nil || s.pool == nil {
		return UserSubscription{}, ErrStoreNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < subscriptionTxRetryAttempts; attempt++ {
		sub, err := s.revokeSubscriptionOnce(ctx, rec)
		if err == nil {
			return sub, nil
		}
		if isPgRetryableTxConflict(err) {
			lastErr = err
			continue
		}
		return UserSubscription{}, err
	}
	return UserSubscription{}, fmt.Errorf("subscription: revoke exhausted retries: %w", lastErr)
}

func (s *PostgresStore) revokeSubscriptionOnce(ctx context.Context, rec revokeRecord) (UserSubscription, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: begin revoke: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sub, err := getSubscriptionForUpdateTx(ctx, tx, rec.TenantID, rec.SubscriptionID)
	if err != nil {
		return UserSubscription{}, err
	}
	if sub.Status == StatusRevoked {
		if err := tx.Commit(ctx); err != nil {
			return UserSubscription{}, fmt.Errorf("subscription: commit idempotent revoke: %w", err)
		}
		return sub, nil
	}
	if ok, err := hasSubAuditRequestTx(ctx, tx, rec.TenantID, sub.ID, AuditSubscriptionRevoked, rec.RequestID); err != nil {
		return UserSubscription{}, err
	} else if ok {
		if err := tx.Commit(ctx); err != nil {
			return UserSubscription{}, fmt.Errorf("subscription: commit idempotent revoke audit: %w", err)
		}
		return sub, nil
	}
	if sub.Status != StatusActive || !sub.ExpiresAt.After(rec.Now) {
		return UserSubscription{}, ErrSubscriptionNotActive
	}
	row := tx.QueryRow(ctx, `
UPDATE user_subscriptions SET status=$3, updated_at=$4
WHERE tenant_id=$1 AND id=$2 AND status='active'
RETURNING`+subscriptionSelectColumns,
		rec.TenantID, rec.SubscriptionID, string(StatusRevoked), rec.Now)
	sub, err = scanSubscription(row)
	if err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: revoke update: %w", err)
	}
	if err := closeCapsTx(ctx, tx, rec.TenantID, sub.ID, rec.Now); err != nil {
		return UserSubscription{}, err
	}
	if err := downgradeAfterCloseTx(ctx, tx, lifecycleRecord{
		TenantID: rec.TenantID, SubscriptionID: rec.SubscriptionID, ActorKind: ActorKindAdmin,
		ActorID: rec.ActorAdminID, RequestID: rec.RequestID, Now: rec.Now,
	}, sub); err != nil {
		return UserSubscription{}, err
	}
	if err := insertSubAuditTx(ctx, tx, subAuditInsert{
		TenantID:           rec.TenantID,
		UserSubscriptionID: sub.ID,
		EventType:          AuditSubscriptionRevoked,
		ActorKind:          ActorKindAdmin,
		ActorID:            rec.ActorAdminID,
		ActorRef:           rec.ActorRef,
		ReasonClass:        rec.Reason,
		RequestID:          rec.RequestID,
		Payload:            map[string]any{"reason": rec.Reason},
		Now:                rec.Now,
	}); err != nil {
		return UserSubscription{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return UserSubscription{}, fmt.Errorf("subscription: commit revoke: %w", err)
	}
	return sub, nil
}

func resolveChangePlanTargetTx(ctx context.Context, tx pgx.Tx, rec changePlanRecord) (UserSubscription, error) {
	if rec.SubscriptionID > 0 {
		return getSubscriptionForUpdateTx(ctx, tx, rec.TenantID, rec.SubscriptionID)
	}
	return getCurrentActiveByUserForUpdateTx(ctx, tx, rec.TenantID, rec.UserID)
}

func changePlanActor(rec changePlanRecord, sub UserSubscription) (string, int64, string) {
	// admin 判定不能只看 ActorAdminID>0:session-admin 的 TokenID=0,须靠 ActorRef 非空识别
	//(自助 change-plan 两者皆空 → user actor)。
	if rec.ActorAdminID > 0 || rec.ActorRef != "" {
		return ActorKindAdmin, rec.ActorAdminID, rec.ActorRef
	}
	return ActorKindUser, sub.UserID, ""
}

func maybeAuditGroupChangeTx(ctx context.Context, tx pgx.Tx, rec changePlanRecord, before UserSubscription, plan Plan, currentGroup, prevGroup, actorKind string, actorID int64, actorRef string) error {
	if plan.GrantedGroup == prevGroup {
		return nil
	}
	currentOwnedByTarget := false
	if prevGroup != "" {
		currentOwnedByTarget = currentGroup == prevGroup
	} else {
		currentOwnedByTarget = currentGroup == DefaultUserGroup
	}
	if !currentOwnedByTarget {
		return nil
	}
	targetGroup := plan.GrantedGroup
	if targetGroup == "" {
		var err error
		targetGroup, err = resolveGroupFromActiveTx(ctx, tx, rec.TenantID, before.UserID)
		if err != nil {
			return err
		}
	}
	if targetGroup == currentGroup {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET user_group=$3 WHERE tenant_id=$1 AND id=$2`,
		rec.TenantID, before.UserID, targetGroup); err != nil {
		return fmt.Errorf("subscription: change plan user group: %w", err)
	}
	eventType := AuditGroupUpgraded
	if targetGroup == DefaultUserGroup || !capsDominate(planCapsTriple(plan), subCapsTriple(before)) {
		eventType = AuditGroupDowngraded
	}
	if err := insertSubAuditTx(ctx, tx, subAuditInsert{
		TenantID:           rec.TenantID,
		UserSubscriptionID: before.ID,
		EventType:          eventType,
		ActorKind:          actorKind,
		ActorID:            actorID,
		ActorRef:           actorRef,
		RequestID:          rec.RequestID,
		Payload:            map[string]any{"from": currentGroup, "to": targetGroup},
		Now:                rec.Now,
	}); err != nil {
		return err
	}
	return nil
}

func updateActiveCapsValidUntilTx(ctx context.Context, tx pgx.Tx, tenantID, subscriptionID int64, newExpires, now time.Time) error {
	if _, err := tx.Exec(ctx, `
UPDATE quota_policies SET valid_until=$3, last_modified_by_actor=$4, updated_at=$5
WHERE tenant_id=$1 AND id IN (
	SELECT quota_policy_id FROM subscription_policy_links
	WHERE tenant_id=$1 AND user_subscription_id=$2 AND status='active'
)`, tenantID, subscriptionID, newExpires, fmt.Sprintf("subscription:%d", subscriptionID), now); err != nil {
		return fmt.Errorf("subscription: extend quota policies: %w", err)
	}
	return nil
}

func hasSubAuditRequestTx(ctx context.Context, tx pgx.Tx, tenantID, subscriptionID int64, eventType, requestID string) (bool, error) {
	if requestID == "" {
		return false, nil
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1 FROM subscription_audit_events
	WHERE tenant_id=$1 AND user_subscription_id=$2 AND event_type=$3 AND request_id=$4
)`, tenantID, subscriptionID, eventType, requestID).Scan(&exists); err != nil {
		return false, fmt.Errorf("subscription: check audit request: %w", err)
	}
	return exists, nil
}

func downgradeAfterCloseTx(ctx context.Context, tx pgx.Tx, rec lifecycleRecord, sub UserSubscription) error {
	if sub.GrantedGroup == "" {
		return nil
	}
	currentGroup, err := lockUserGroupTx(ctx, tx, rec.TenantID, sub.UserID)
	if err != nil {
		return err
	}
	if currentGroup != sub.GrantedGroup {
		return nil
	}
	targetGroup, err := resolveGroupFromActiveTx(ctx, tx, rec.TenantID, sub.UserID)
	if err != nil {
		return err
	}
	if targetGroup == currentGroup {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET user_group=$3 WHERE tenant_id=$1 AND id=$2`,
		rec.TenantID, sub.UserID, targetGroup); err != nil {
		return fmt.Errorf("subscription: downgrade user group: %w", err)
	}
	if err := insertSubAuditTx(ctx, tx, subAuditInsert{
		TenantID:           rec.TenantID,
		UserSubscriptionID: sub.ID,
		EventType:          AuditGroupDowngraded,
		ActorKind:          actorKindOrDefault(rec.ActorKind),
		ActorID:            rec.ActorID,
		ActorRef:           rec.ActorRef,
		RequestID:          rec.RequestID,
		Payload:            map[string]any{"from": currentGroup, "to": targetGroup},
		Now:                rec.Now,
	}); err != nil {
		return err
	}
	return nil
}

type planAuditInsert struct {
	TenantID  int64
	PlanID    int64
	EventType string
	ActorKind string
	ActorID   int64
	ActorRef  string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	RequestID string
	Payload   map[string]any
	Now       time.Time
}

func insertPlanAuditTx(ctx context.Context, tx pgx.Tx, ev planAuditInsert) error {
	var raw []byte
	if len(ev.Payload) > 0 {
		raw, _ = json.Marshal(ev.Payload)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO subscription_plan_audit_events (
	tenant_id, plan_id, event_type, actor_kind, actor_id, actor_ref, request_id, redacted_payload, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		ev.TenantID, ev.PlanID, ev.EventType, actorKindOrDefault(ev.ActorKind),
		nullableInt64(ev.ActorID), nullableText(ev.ActorRef), nullableText(ev.RequestID), nullableJSON(raw), ev.Now); err != nil {
		return fmt.Errorf("subscription: insert plan audit event: %w", err)
	}
	return nil
}

func capAuditString(d *decimal.Decimal) any {
	if d == nil {
		return nil
	}
	return d.String()
}
