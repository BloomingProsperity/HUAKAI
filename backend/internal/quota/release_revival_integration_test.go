//go:build integration_pg

package quota

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestReconcilerReleaseAfterAbort_RevivedReconciliationNeededDefersUntilClaimTerminal
// 复现旧预留标毒后 claim 复活，且新 attempt 的 Reserve 冲突耗尽并 fail-open 运行的交错；
// 旧 job 必须先退避，等 claim 终结后再解毒，避免提前释放其结算依据。
// 变异：把推迟改成立即释放或 invalidated 终停时，首轮 job 状态和最终收敛断言都会变红。
func TestReconcilerReleaseAfterAbort_RevivedReconciliationNeededDefersUntilClaimTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	service := NewService(store)
	now := time.Now().UTC().Truncate(time.Second)
	seed := seedReleaseRecoveryFixture(t, ctx, f, service, "release-revival-detox")
	f.setClaimTerminal(seed.reserve.Reservation.ClaimID, claimStatusAborted, "")

	preparedID, err := store.PrepareReleaseRecovery(ctx, f.tenantID, seed.reserve.Reservation.ClaimID, seed.reserve.Reservation.ID)
	if err != nil || preparedID != seed.reserve.Reservation.ID {
		t.Fatalf("PrepareReleaseRecovery id/err=%d/%v; want %d/nil", preparedID, err, seed.reserve.Reservation.ID)
	}
	if status := f.reservationStatus(seed.reserve.Reservation.ID); status != ReservationReconciliationNeeded {
		t.Fatalf("prepared reservation status=%s; want reconciliation_needed", status)
	}
	job := f.enqueueReconcilerJob(ctx, store, seed.reserve.Reservation.ClaimID, &preparedID, reconciliationKindReleaseAfterAbort, now.Add(-time.Minute))
	if attempt := f.reviveClaimForNewAttempt(seed.reserve.Reservation.ClaimID, now.Add(10*time.Minute)); attempt != 2 {
		t.Fatalf("revived attempt_seq=%d; want 2", attempt)
	}

	originalBeginTx := store.beginTx
	reserveAttempts := 0
	store.beginTx = func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
		reserveAttempts++
		return nil, &pgconn.PgError{Code: "40001", Message: "forced fail-open reserve exhaustion"}
	}
	failedOpen, reserveErr := service.Reserve(ctx, replayReleaseRecoveryReservation(seed, now.Add(time.Second)))
	store.beginTx = originalBeginTx
	if !IsRetryable(reserveErr) || IsDenied(reserveErr) || failedOpen.Allowed || reserveAttempts != reserveTxRetryAttempts {
		t.Fatalf("fail-open Reserve attempts/err/result=%d/%v/%+v; want %d/retryable/not allowed",
			reserveAttempts, reserveErr, failedOpen, reserveTxRetryAttempts)
	}

	reconciler := NewReconciler(service, store, ReconcilerOptions{
		BaseBackoff: time.Minute,
		MaxBackoff:  time.Hour,
		Limit:       10,
	})
	processed, err := reconciler.ReconcileDueJobs(ctx, f.tenantID, now, 10)
	if !errors.Is(err, ErrReleaseDeferredForRevival) || !IsRetryable(err) || processed != 0 {
		t.Fatalf("first ReconcileDueJobs processed/err=%d/%v; want 0/Deferred retryable", processed, err)
	}
	deferred := f.reconcilerJobState(job.ID)
	if deferred.status != "queued" || deferred.attempts != 1 || !deferred.nextRunAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("deferred job state=%+v; want queued attempt 1 with one-minute backoff", deferred)
	}
	if !strings.Contains(deferred.lastError, "release deferred for billing claim revival") {
		t.Fatalf("deferred last_error=%q; want Deferred reason", deferred.lastError)
	}
	assertHeldQuotaFixture(t, f, seed)
	if got := f.auditSemanticCount("release_aborted"); got != 0 {
		t.Fatalf("release audit count=%d; want 0 while revival is running", got)
	}
	if got := releaseJobCountForClaim(t, f, seed.reserve.Reservation.ClaimID); got != 1 {
		t.Fatalf("release job count=%d; want original job only", got)
	}

	f.setClaimTerminal(seed.reserve.Reservation.ClaimID, claimStatusCommitted, seed.reserve.Reservation.PredictedCost.String())
	processed, err = reconciler.ReconcileDueJobs(ctx, f.tenantID, deferred.nextRunAt, 10)
	if err != nil || processed != 1 {
		t.Fatalf("terminal ReconcileDueJobs processed/err=%d/%v; want 1/nil", processed, err)
	}
	completed := f.reconcilerJobState(job.ID)
	if completed.status != "succeeded" || completed.attempts != 2 {
		t.Fatalf("completed job state=%+v; want succeeded attempt 2", completed)
	}
	assertReleasedQuotaFixture(t, f, seed)

	reactivated, err := service.Reserve(ctx, replayReleaseRecoveryReservation(seed, deferred.nextRunAt.Add(time.Second)))
	if err != nil || !reactivated.Allowed || !reactivated.IdempotencyHit {
		t.Fatalf("reactivate Reserve err/result=%v/%+v; want reused allow", err, reactivated)
	}
	if reactivated.Reservation.ID != seed.reserve.Reservation.ID || reactivated.Reservation.Status != ReservationReserved {
		t.Fatalf("reactivated reservation=%+v; want same id %d in reserved", reactivated.Reservation, seed.reserve.Reservation.ID)
	}
	assertHeldQuotaFixture(t, f, seed)
}

// TestReconcilerReleaseAfterAbort_RevivedReservedProtectsLiveReservation
// 固定旧 release_after_abort job 与新 attempt 的 REUSED 活预留相撞时，job 必须终态失败，
// 且不得释放窗口、并发槽或再造补偿 job。
// 变异：删除 reserved 分支的复活守卫时，reservation 会变 released、窗口归零且 job 被误标成功。
func TestReconcilerReleaseAfterAbort_RevivedReservedProtectsLiveReservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	service := NewService(store)
	now := time.Now().UTC().Truncate(time.Second)
	seed := seedReleaseRecoveryFixture(t, ctx, f, service, "release-revival-live")
	f.setClaimTerminal(seed.reserve.Reservation.ClaimID, claimStatusAborted, "")
	job := f.enqueueReconcilerJob(ctx, store, seed.reserve.Reservation.ClaimID, &seed.reserve.Reservation.ID, reconciliationKindReleaseAfterAbort, now.Add(-time.Minute))
	if attempt := f.reviveClaimForNewAttempt(seed.reserve.Reservation.ClaimID, now.Add(10*time.Minute)); attempt != 2 {
		t.Fatalf("revived attempt_seq=%d; want 2", attempt)
	}

	reused, err := service.Reserve(ctx, replayReleaseRecoveryReservation(seed, now.Add(time.Second)))
	if err != nil || !reused.Allowed || !reused.IdempotencyHit || reused.Reservation.Status != ReservationReserved {
		t.Fatalf("REUSED Reserve err/result=%v/%+v; want live reserved reuse", err, reused)
	}

	reconciler := NewReconciler(service, store, ReconcilerOptions{Limit: 10})
	processed, err := reconciler.ReconcileDueJobs(ctx, f.tenantID, now, 10)
	if err == nil || !strings.Contains(err.Error(), "recovery invalidated") || processed != 0 {
		t.Fatalf("ReconcileDueJobs processed/err=%d/%v; want 0/recovery invalidated", processed, err)
	}
	state := f.reconcilerJobState(job.ID)
	if state.status != "failed" || state.attempts != 1 || !state.nextRunAt.Equal(now.Add(terminalReconciliationDelay)) {
		t.Fatalf("job state=%+v; want terminal failed attempt 1", state)
	}
	if !strings.Contains(state.lastError, ErrReleaseInvalidatedByRevival.Error()) || !strings.Contains(state.lastError, "reserving") {
		t.Fatalf("job last_error=%q; want sentinel and revived claim state", state.lastError)
	}
	if status := f.reservationStatus(seed.reserve.Reservation.ID); status != ReservationReserved {
		t.Fatalf("reservation status=%s; want live reserved unchanged", status)
	}
	assertHeldQuotaFixture(t, f, seed)
	if got := f.auditSemanticCount("release_aborted"); got != 0 {
		t.Fatalf("release audit count=%d; want 0", got)
	}
	if got := releaseJobCountForClaim(t, f, seed.reserve.Reservation.ClaimID); got != 1 {
		t.Fatalf("release job count=%d; want original job only", got)
	}
}

func replayReleaseRecoveryReservation(seed releaseRecoveryFixture, at time.Time) ReserveRequest {
	return ReserveRequest{
		TenantID:            seed.reserve.Reservation.TenantID,
		ClaimID:             seed.reserve.Reservation.ClaimID,
		RequestFingerprint:  seed.reserve.Reservation.RequestFingerprint,
		Scopes:              seed.reserve.Reservation.Scopes,
		PredictedCost:       seed.reserve.Reservation.PredictedCost,
		NeedConcurrencySlot: true,
		LeaseExpiresAt:      at.Add(10 * time.Minute),
		At:                  at,
	}
}
