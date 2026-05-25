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
	DefaultRevokedCooldown   = 30 * time.Minute
)

type ProviderAccountHealthPolicy struct {
	ThrottledCooldown time.Duration
	RevokedCooldown   time.Duration
}

func DefaultProviderAccountHealthPolicy() ProviderAccountHealthPolicy {
	return ProviderAccountHealthPolicy{
		ThrottledCooldown: DefaultThrottledCooldown,
		RevokedCooldown:   DefaultRevokedCooldown,
	}
}

func (p ProviderAccountHealthPolicy) normalized() ProviderAccountHealthPolicy {
	if p.ThrottledCooldown <= 0 {
		p.ThrottledCooldown = DefaultThrottledCooldown
	}
	if p.RevokedCooldown <= 0 {
		p.RevokedCooldown = DefaultRevokedCooldown
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
		return ProviderAccountHealthChange{
			HealthState:      ProviderAccountHealthRevoked,
			HealthStateUntil: timePtrValue(now.Add(p.RevokedCooldown)),
		}, true
	case auth.RefreshAuditOutcome("rate_limit_exceeded"):
		return ProviderAccountHealthChange{
			HealthState:      ProviderAccountHealthThrottled,
			HealthStateUntil: timePtrValue(now.Add(p.ThrottledCooldown)),
		}, true
	case auth.RefreshAuditOutcome("risk_control_triggered"):
		return ProviderAccountHealthChange{
			HealthState:      ProviderAccountHealthRevoked,
			HealthStateUntil: timePtrValue(now.Add(p.RevokedCooldown)),
			Alert:            true,
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

const updateProviderAccountHealthSQL = `
UPDATE provider_accounts
SET
    health_state = $3,
    health_state_until = $4,
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
	tag, err := exec.Exec(ctx, updateProviderAccountHealthSQL,
		change.TenantID,
		change.ProviderAccountID,
		change.HealthState,
		until,
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
