package credentialworker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

const (
	ProviderAccountHealthHealthy   = "healthy"
	ProviderAccountHealthThrottled = "throttled"
	ProviderAccountHealthRevoked   = "revoked"
	ProviderAccountHealthCooldown  = "cooldown"

	DefaultThrottledCooldown = 3 * time.Minute
)

type ProviderAccountHealthPolicy struct {
	ThrottledCooldown time.Duration
}

func DefaultProviderAccountHealthPolicy() ProviderAccountHealthPolicy {
	return ProviderAccountHealthPolicy{
		ThrottledCooldown: DefaultThrottledCooldown,
	}
}

func (p ProviderAccountHealthPolicy) normalized() ProviderAccountHealthPolicy {
	if p.ThrottledCooldown <= 0 {
		p.ThrottledCooldown = DefaultThrottledCooldown
	}
	return p
}

type ProviderAccountHealthChange struct {
	TenantID          int64
	ProviderAccountID int64
	HealthState       string
	HealthStateUntil  *time.Time
	Alert             bool
}

func (p ProviderAccountHealthPolicy) Transition(outcome auth.Outcome, now time.Time) (ProviderAccountHealthChange, bool) {
	p = p.normalized()
	now = now.UTC()
	switch outcome {
	case auth.RefreshAuditOutcome("auth_expired"):
		// 刷新令牌被上游判定失效属终态,定时器无法自愈。HealthStateUntil 留 nil,
		// 使 eligibility SQL(health_state_until IS NOT NULL 谓词)与 router gate(until.IsZero
		// 即不可用)都拒绝自动恢复——与 account_disabled 同范式。恢复只能由后续刷新成功
		// (scanner 不按 health 过滤,仍会重试)或 operator 介入触发;Alert 通知 operator 需重新登录。
		return ProviderAccountHealthChange{
			HealthState: ProviderAccountHealthRevoked,
			Alert:       true,
		}, true
	case auth.RefreshAuditOutcome("rate_limit_exceeded"):
		// 限流是真正的 transient 类:保留有限冷却,到期后由 eligibility SQL/gate 自动恢复。
		return ProviderAccountHealthChange{
			HealthState:      ProviderAccountHealthThrottled,
			HealthStateUntil: timePtrValue(now.Add(p.ThrottledCooldown)),
		}, true
	case auth.RefreshAuditOutcome("risk_control_triggered"):
		// 风控触发同属终态。旧的定时自愈会在风控尚未解除时过早把账号投回路由,
		// 请求被上游拒、浪费 attempt 并可能加重风控;改为终态(nil until),恢复同样由成功刷新
		// 或 operator 驱动,Alert 维持以通知 operator 介入。
		return ProviderAccountHealthChange{
			HealthState: ProviderAccountHealthRevoked,
			Alert:       true,
		}, true
	case auth.RefreshAuditOutcome("account_disabled"):
		return ProviderAccountHealthChange{HealthState: ProviderAccountHealthRevoked}, true
	case auth.OutcomeRefreshSucceeded:
		return ProviderAccountHealthChange{HealthState: ProviderAccountHealthHealthy}, true
	default:
		return ProviderAccountHealthChange{}, false
	}
}

type providerAccountHealthStore interface {
	UpdateProviderAccountHealth(context.Context, ProviderAccountHealthChange) error
}

type providerAccountHealthDBStore struct {
	db db.DBTX
}

func (s providerAccountHealthDBStore) UpdateProviderAccountHealth(ctx context.Context, change ProviderAccountHealthChange) error {
	if s.db == nil {
		return fmt.Errorf("credentialworker: provider account health db is nil")
	}
	return updateProviderAccountHealth(ctx, s.db, change)
}

func (s *Scheduler) providerAccountHealthPolicy() ProviderAccountHealthPolicy {
	if s == nil {
		return DefaultProviderAccountHealthPolicy()
	}
	return s.healthPolicy.normalized()
}

func (s *Scheduler) providerAccountHealthChange(accountID, tenantID int64, outcome auth.Outcome, now time.Time) (ProviderAccountHealthChange, bool) {
	change, ok := s.providerAccountHealthPolicy().Transition(outcome, now)
	if !ok {
		return ProviderAccountHealthChange{}, false
	}
	change.TenantID = tenantID
	change.ProviderAccountID = accountID
	return change, true
}

// updateProviderAccountHealthSQL 写回 health 状态。$5 (is_transient) 为 true 时表示本次是一个
// 带冷却期的瞬态写入(目前仅 rate_limit_exceeded -> throttled)。: 瞬态写入绝不能降级一个
// 已经终态撤销(revoked + health_state_until IS NULL)的账号——否则一次偶发的 rate_limit 重试就会
// 把因 auth_expired/risk_control 而终态的账号改写成 throttled+3min,重新打开终态本要关闭的定时自愈
// 通道。CASE 让终态行在瞬态写入下保持原值(仅刷新 updated_at);成功刷新(healthy,无 deadline)与
// 显式 revoke 不受影响,恢复路径得以保留。用 CASE 而非 WHERE 守卫,使 RowsAffected 恒为 1(行存在),
// 不会把"终态保护跳过"与"账号不存在"混淆。
const updateProviderAccountHealthSQL = `
UPDATE provider_accounts
SET
    health_state = CASE
        WHEN $5::boolean AND health_state = 'revoked' AND health_state_until IS NULL
        THEN health_state
        ELSE $3
    END,
    health_state_until = CASE
        WHEN $5::boolean AND health_state = 'revoked' AND health_state_until IS NULL
        THEN health_state_until
        ELSE $4
    END,
    updated_at = NOW()
WHERE tenant_id = $1
  AND id = $2`

func updateProviderAccountHealth(ctx context.Context, exec db.DBTX, change ProviderAccountHealthChange) error {
	if exec == nil {
		return fmt.Errorf("credentialworker: provider account health db is nil")
	}
	var until any
	if change.HealthStateUntil != nil {
		until = change.HealthStateUntil.UTC()
	}
	// 带 deadline 的写入即瞬态类(throttled 冷却);终态(revoked+nil)与 healthy 的 until 均为 nil。
	isTransient := change.HealthStateUntil != nil
	tag, err := exec.Exec(ctx, updateProviderAccountHealthSQL,
		change.TenantID,
		change.ProviderAccountID,
		change.HealthState,
		until,
		isTransient,
	)
	if err != nil {
		return fmt.Errorf("provider account health update: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("provider account health update affected %d rows for tenant=%d account=%d",
			tag.RowsAffected(), change.TenantID, change.ProviderAccountID)
	}
	return nil
}

func (s *Scheduler) maybeLogProviderAccountHealthAlert(ctx context.Context, change ProviderAccountHealthChange, outcome auth.Outcome) {
	if !change.Alert {
		return
	}
	slog.WarnContext(ctx, "provider account health_state revoked after credential refresh risk signal",
		"tenant_id", change.TenantID,
		"provider_account_id", change.ProviderAccountID,
		"outcome", outcome,
		"health_state", change.HealthState,
	)
}

func timePtrValue(v time.Time) *time.Time {
	v = v.UTC()
	return &v
}
