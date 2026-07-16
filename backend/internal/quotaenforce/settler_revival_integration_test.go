//go:build integration_pg

package quotaenforce

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

// TestSettlerAbort_RevivalGapTreatsRealPGReleaseSentinelAsNoOp 固定 billing Abort
// 已提交、quota Release 尚未开始时 claim 被复活的真实数据库插缝。新 attempt 的活预留
// 必须保持 reserved，组合 settler 同时把 Release sentinel 当作成功 no-op。
// 变异：Abort 只忽略 reservation-not-found，或 Release 删除复活守卫，本测试分别在返回错误或状态断言上变红。
func TestSettlerAbort_RevivalGapTreatsRealPGReleaseSentinelAsNoOp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openQuotaEnforceIntegrationPool(t, ctx)
	seed := seedQuotaEnforceRevivalFixture(t, ctx, pool)
	service := quota.NewService(quota.NewPostgresStore(pool))
	now := time.Now().UTC().Truncate(time.Second)

	reserved, err := service.Reserve(ctx, quota.ReserveRequest{
		TenantID:           seed.tenantID,
		ClaimID:            seed.claimID,
		RequestFingerprint: seed.fingerprint,
		Scopes: []quota.Scope{{
			TenantID: seed.tenantID,
			Kind:     quota.ScopeUser,
			ID:       strconv.FormatInt(seed.userID, 10),
		}},
		PredictedCost:  decimal.NewFromInt(4),
		LeaseExpiresAt: now.Add(10 * time.Minute),
		At:             now,
	})
	if err != nil || !reserved.Allowed || reserved.Reservation.Status != quota.ReservationReserved {
		t.Fatalf("Reserve err/result=%v/%+v; want live reserved", err, reserved)
	}

	inner := &pgAbortBillingSettler{pool: pool}
	finalizer := &revivingPGQuotaFinalizer{pool: pool, service: service}
	settler := NewSettler(inner, finalizer)
	err = settler.Abort(ctx, seed.tenantID, seed.claimID, "upstream_error", "revival-gap", 0, nil)
	if err != nil {
		t.Fatalf("Abort: %v; want revival sentinel treated as no-op", err)
	}
	if inner.abortCalls != 1 || finalizer.releaseCalls != 1 {
		t.Fatalf("billing abort/quota release calls=%d/%d; want 1/1", inner.abortCalls, finalizer.releaseCalls)
	}
	if !errors.Is(finalizer.releaseErr, quota.ErrReleaseInvalidatedByRevival) {
		t.Fatalf("underlying Release err=%v; want ErrReleaseInvalidatedByRevival", finalizer.releaseErr)
	}

	var claimStatus string
	var attemptSeq int32
	if err := pool.QueryRow(ctx,
		`SELECT status, attempt_seq FROM billing_ledger_claims WHERE tenant_id=$1 AND id=$2`,
		seed.tenantID, seed.claimID,
	).Scan(&claimStatus, &attemptSeq); err != nil {
		t.Fatalf("read revived claim: %v", err)
	}
	if claimStatus != "reserving" || attemptSeq != 2 {
		t.Fatalf("claim status/attempt=%s/%d; want reserving/2", claimStatus, attemptSeq)
	}
	var reservationStatus quota.ReservationStatus
	if err := pool.QueryRow(ctx,
		`SELECT status FROM quota_reservations WHERE tenant_id=$1 AND id=$2`,
		seed.tenantID, reserved.Reservation.ID,
	).Scan(&reservationStatus); err != nil {
		t.Fatalf("read protected reservation: %v", err)
	}
	if reservationStatus != quota.ReservationReserved {
		t.Fatalf("reservation status=%s; want reserved", reservationStatus)
	}
	var releaseAudits, recoveryJobs int64
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM quota_audit_events
		 WHERE tenant_id=$1 AND COALESCE(payload ->> 'operation', '')='release_aborted'`,
		seed.tenantID,
	).Scan(&releaseAudits); err != nil {
		t.Fatalf("count release audits: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM quota_reconciliation_jobs WHERE tenant_id=$1 AND claim_id=$2`,
		seed.tenantID, seed.claimID,
	).Scan(&recoveryJobs); err != nil {
		t.Fatalf("count recovery jobs: %v", err)
	}
	if releaseAudits != 0 || recoveryJobs != 0 {
		t.Fatalf("release audits/recovery jobs=%d/%d; want 0/0", releaseAudits, recoveryJobs)
	}
}

type quotaEnforceRevivalSeed struct {
	tenantID    int64
	userID      int64
	apiKeyID    int64
	claimID     int64
	fingerprint string
}

func openQuotaEnforceIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 8})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedQuotaEnforceRevivalFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) quotaEnforceRevivalSeed {
	t.Helper()
	suffix := uuid.NewString()
	seed := quotaEnforceRevivalSeed{fingerprint: "quotaenforce-revival-" + suffix}
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "quotaenforce-"+suffix).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM quota_reconciliation_jobs WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM quota_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM quota_concurrency_slots WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM quota_concurrency_scope_locks WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM quota_reservations WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM quota_windows WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM quota_policies WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_keys WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, seed.tenantID)
	})
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "quotaenforce-user-"+suffix,
	).Scan(&seed.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		seed.tenantID, seed.userID, "quotaenforce-key-"+suffix,
		"$2a$10$placeholder-not-resolved-by-quotaenforce-tests", "hk_qe_"+suffix[:16],
	).Scan(&seed.apiKeyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint,
			api_key_id, user_id, logical_request_id, endpoint_family,
			requested_model, billing_policy_version, request_class,
			predicted_cost, currency_code, lease_expires_at
		 ) VALUES (
			$1, $2, $3,
			$4, $5, $6, 'chat',
			'gpt-4.1-mini', 'quotaenforce-test', 'standard',
			4, 'USD', $7
		 ) RETURNING id`,
		seed.tenantID, "idem-"+suffix, seed.fingerprint,
		seed.apiKeyID, seed.userID, "logical-"+suffix, time.Now().UTC().Add(10*time.Minute),
	).Scan(&seed.claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	return seed
}

type pgAbortBillingSettler struct {
	pool       *pgxpool.Pool
	abortCalls int
}

func (s *pgAbortBillingSettler) Settle(context.Context, billing.SettleRequest) (*billing.SettleResult, error) {
	return nil, errors.New("unexpected Settle")
}

func (s *pgAbortBillingSettler) Abort(ctx context.Context, tenantID, claimID int64, reason, _ string, _ int64, _ json.RawMessage) error {
	s.abortCalls++
	tag, err := s.pool.Exec(ctx,
		`UPDATE billing_ledger_claims
		 SET status='aborted', aborted_reason=$3, settled_at=clock_timestamp()
		 WHERE tenant_id=$1 AND id=$2 AND status='reserving'`,
		tenantID, claimID, reason,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("billing abort rows=%d; want 1", tag.RowsAffected())
	}
	return nil
}

func (s *pgAbortBillingSettler) CommitCacheHit(context.Context, billing.SettleRequest) error {
	return errors.New("unexpected CommitCacheHit")
}

func (s *pgAbortBillingSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, errors.New("unexpected Refund")
}

type revivingPGQuotaFinalizer struct {
	pool         *pgxpool.Pool
	service      *quota.Service
	releaseCalls int
	releaseErr   error
}

func (f *revivingPGQuotaFinalizer) Settle(context.Context, quota.SettleRequest) (quota.SettleResult, error) {
	return quota.SettleResult{}, errors.New("unexpected Settle")
}

func (f *revivingPGQuotaFinalizer) Release(ctx context.Context, req quota.ReleaseRequest) (quota.ReleaseResult, error) {
	f.releaseCalls++
	var attemptSeq int32
	if err := f.pool.QueryRow(ctx,
		`UPDATE billing_ledger_claims
		 SET status='reserving', aborted_reason=NULL, settled_at=NULL,
		     attempt_seq=attempt_seq+1, lease_expires_at=$3, reserved_at=clock_timestamp()
		 WHERE tenant_id=$1 AND id=$2 AND status='aborted'
		 RETURNING attempt_seq`,
		req.TenantID, req.ClaimID, time.Now().UTC().Add(10*time.Minute),
	).Scan(&attemptSeq); err != nil {
		return quota.ReleaseResult{}, err
	}
	if attemptSeq != 2 {
		return quota.ReleaseResult{}, fmt.Errorf("revived attempt_seq=%d; want 2", attemptSeq)
	}
	result, err := f.service.Release(ctx, req)
	f.releaseErr = err
	return result, err
}

func (f *revivingPGQuotaFinalizer) CommitCacheHit(context.Context, quota.CacheHitRequest) (quota.CacheHitResult, error) {
	return quota.CacheHitResult{}, errors.New("unexpected CommitCacheHit")
}
