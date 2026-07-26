package tenantadmin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func (s *Service) InspectDelete(ctx context.Context, tenantID int64) (DeleteImpact, error) {
	if err := s.configured(); err != nil {
		return DeleteImpact{}, err
	}
	if tenantID <= 0 {
		return DeleteImpact{}, ErrInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return DeleteImpact{}, fmt.Errorf("tenantadmin: begin deletion impact: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenant, err := lockTenantForRead(ctx, tx, tenantID, s.platformTenantID)
	if err != nil {
		return DeleteImpact{}, err
	}
	if tenant.IsPlatform {
		return DeleteImpact{}, ErrPlatformTenant
	}
	impact, err := queryDeleteImpact(ctx, tx, tenant)
	if err != nil {
		return DeleteImpact{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DeleteImpact{}, fmt.Errorf("tenantadmin: commit deletion impact: %w", err)
	}
	return impact, nil
}

func (s *Service) Delete(ctx context.Context, input DeleteInput) (DeleteResult, error) {
	if err := s.configured(); err != nil {
		return DeleteResult{}, err
	}
	if input.TenantID <= 0 || input.ExpectedVersion <= 0 || strings.TrimSpace(input.ImpactHash) == "" {
		return DeleteResult{}, ErrInvalidInput
	}
	audit, err := normalizeAudit(input.Audit, true)
	if err != nil {
		return DeleteResult{}, err
	}
	var result DeleteResult
	now := s.clockNow()
	err = runSerializableMutation(ctx, s.pool, func(tx pgx.Tx) error {
		before, err := lockTenant(ctx, tx, input.TenantID, s.platformTenantID)
		if err != nil {
			return err
		}
		if before.IsPlatform {
			return ErrPlatformTenant
		}
		if before.Status != StatusDisabled {
			return ErrInvalidTransition
		}
		if before.Version != input.ExpectedVersion {
			return ErrVersionConflict
		}
		impact, err := queryDeleteImpact(ctx, tx, before)
		if err != nil {
			return err
		}
		if impact.ImpactHash != strings.TrimSpace(input.ImpactHash) {
			return ErrImpactChanged
		}
		if impact.Blocked {
			return ErrDeleteBlocked
		}
		after, err := scanTenant(tx.QueryRow(ctx, `
UPDATE tenants
SET status='deleted', deleted_at=$2, version=version+1,
    status_reason=$3, status_changed_at=$2, status_changed_by=$4, updated_at=$2
WHERE id=$1 AND version=$5 AND status='disabled' AND deleted_at IS NULL
RETURNING id, name, status, version, COALESCE(status_reason, ''),
          status_changed_at, COALESCE(status_changed_by, ''),
          created_at, updated_at, deleted_at`,
			input.TenantID, now, audit.Reason, audit.ActorID, input.ExpectedVersion,
		), s.platformTenantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVersionConflict
		}
		if err != nil {
			return err
		}
		sessionsRevoked, err := revokeTenantSessions(ctx, tx, input.TenantID, "tenant_deleted", now)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"version_before": before.Version, "version_after": after.Version,
			"impact_hash": impact.ImpactHash, "sessions_revoked": sessionsRevoked,
			"resources_retained": impact.Resources,
		})
		if err != nil {
			return err
		}
		if err := insertAudit(ctx, tx, input.TenantID, input.TenantID, "delete_tenant", audit, payload); err != nil {
			return err
		}
		result = DeleteResult{Tenant: after, SessionsRevoked: sessionsRevoked}
		return nil
	})
	if err != nil {
		return DeleteResult{}, fmt.Errorf("tenantadmin: delete tenant: %w", err)
	}
	return result, nil
}

func lockTenantForRead(ctx context.Context, tx pgx.Tx, tenantID, platformTenantID int64) (Tenant, error) {
	item, err := scanTenant(tx.QueryRow(ctx, `
SELECT id, name, status, version, COALESCE(status_reason, ''),
       status_changed_at, COALESCE(status_changed_by, ''),
       created_at, updated_at, deleted_at
FROM tenants
WHERE id=$1`, tenantID), platformTenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tenant{}, ErrNotFound
	}
	return item, err
}

func queryDeleteImpact(ctx context.Context, tx pgx.Tx, tenant Tenant) (DeleteImpact, error) {
	impact := DeleteImpact{
		TenantID: tenant.ID, TenantVersion: tenant.Version, TenantStatus: tenant.Status,
	}
	err := tx.QueryRow(ctx, `
SELECT
    COALESCE((SELECT balance::text FROM tenant_wallets WHERE tenant_id=$1), '0'),
    (SELECT count(*) FROM users WHERE tenant_id=$1 AND role='admin' AND principal_kind='human' AND deleted_at IS NULL),
    (SELECT count(*) FROM users WHERE tenant_id=$1 AND role='user' AND principal_kind='human' AND deleted_at IS NULL),
    (SELECT count(*) FROM api_keys WHERE tenant_id=$1 AND deleted_at IS NULL),
    (SELECT count(*) FROM provider_accounts WHERE tenant_id=$1 AND deleted_at IS NULL),
    (SELECT count(*) FROM account_credentials WHERE tenant_id=$1 AND deleted_at IS NULL),
    (SELECT count(*) FROM pool_groups WHERE tenant_id=$1 AND deleted_at IS NULL),
    (SELECT count(*) FROM proxies WHERE tenant_id=$1 AND deleted_at IS NULL),
    (SELECT count(*) FROM session_families WHERE tenant_id=$1),
    (SELECT count(*) FROM user_balances WHERE tenant_id=$1 AND (balance <> 0 OR held <> 0)),
    (SELECT count(*) FROM billing_ledger_claims WHERE tenant_id=$1 AND status='reserving'),
    (SELECT count(*) FROM balance_holds WHERE tenant_id=$1 AND state='held'),
    (SELECT count(*) FROM pool_slot_acquisitions WHERE tenant_id=$1 AND status='acquired'),
    (SELECT count(*) FROM quota_reservations WHERE tenant_id=$1 AND status IN ('reserved', 'reconciliation_needed')),
    (SELECT count(*) FROM quota_concurrency_slots WHERE tenant_id=$1 AND status='acquired'),
    (SELECT count(*) FROM settlement_intents WHERE tenant_id=$1 AND status IN ('pending', 'delivering', 'settling', 'failed')),
    (SELECT count(*) FROM media_tasks WHERE tenant_id=$1 AND status IN (
        'queued', 'submitting', 'submission_unknown', 'submission_releasing', 'in_progress', 'settlement_pending'
    )),
    (SELECT count(*) FROM media_task_orphans WHERE tenant_id=$1 AND reconcile_status IN ('pending', 'release_requested')),
    (SELECT count(*) FROM quota_reconciliation_jobs WHERE tenant_id=$1 AND status IN ('queued', 'running', 'failed')),
    (SELECT count(*) FROM usage_record_dlq WHERE tenant_id=$1 AND replayed_at IS NULL),
    (SELECT count(*) FROM outbox_events
        WHERE tenant_id=$1
          AND event_type='payment.signup_reward'
          AND status <> 'completed'),
    (SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND status IN ('pending', 'paid', 'recharging')),
    (SELECT count(*) FROM recharge_orders WHERE tenant_id=$1 AND status IN ('PENDING', 'PAID', 'CREDITING')),
    (SELECT count(*) FROM payment_refund_requests WHERE tenant_id=$1 AND status='pending'),
    (SELECT count(*) FROM cost_disputes WHERE tenant_id=$1 AND status IN ('open', 'reviewing'))`,
		tenant.ID,
	).Scan(
		&impact.TenantWalletBalance,
		&impact.Resources.TenantAdmins,
		&impact.Resources.FinalUsers,
		&impact.Resources.APIKeys,
		&impact.Resources.ProviderAccounts,
		&impact.Resources.AccountCredentials,
		&impact.Resources.PoolGroups,
		&impact.Resources.Proxies,
		&impact.Resources.SessionFamilies,
		&impact.Blockers.UserBalanceRows,
		&impact.Blockers.ReservingClaims,
		&impact.Blockers.HeldBalances,
		&impact.Blockers.PoolSlots,
		&impact.Blockers.QuotaReservations,
		&impact.Blockers.QuotaSlots,
		&impact.Blockers.SettlementIntents,
		&impact.Blockers.MediaTasks,
		&impact.Blockers.MediaOrphans,
		&impact.Blockers.QuotaReconciliationJobs,
		&impact.Blockers.UsageDLQ,
		&impact.Blockers.SignupRewardRecoveries,
		&impact.Blockers.PaymentOrders,
		&impact.Blockers.RechargeOrders,
		&impact.Blockers.RefundRequests,
		&impact.Blockers.CostDisputes,
	)
	if err != nil {
		return DeleteImpact{}, fmt.Errorf("tenantadmin: query deletion impact: %w", err)
	}
	impact.Blocked = moneyNonZero(impact.TenantWalletBalance) || blockerTotal(impact.Blockers) > 0
	hashInput := impact
	hashInput.ImpactHash = ""
	raw, err := json.Marshal(hashInput)
	if err != nil {
		return DeleteImpact{}, fmt.Errorf("tenantadmin: encode deletion impact: %w", err)
	}
	sum := sha256.Sum256(raw)
	impact.ImpactHash = hex.EncodeToString(sum[:])
	return impact, nil
}

func moneyNonZero(raw string) bool {
	value, err := decimal.NewFromString(strings.TrimSpace(raw))
	return err != nil || !value.IsZero()
}

func blockerTotal(value BlockerCounts) int64 {
	return value.UserBalanceRows +
		value.ReservingClaims +
		value.HeldBalances +
		value.PoolSlots +
		value.QuotaReservations +
		value.QuotaSlots +
		value.SettlementIntents +
		value.MediaTasks +
		value.MediaOrphans +
		value.QuotaReconciliationJobs +
		value.UsageDLQ +
		value.SignupRewardRecoveries +
		value.PaymentOrders +
		value.RechargeOrders +
		value.RefundRequests +
		value.CostDisputes
}
