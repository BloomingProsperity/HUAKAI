//go:build integration_pg

package quota

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestReconcilerReleaseAfterAbort_RevivedReconciliationNeededDetoxesAndReactivates
// 复现旧恢复准备先把预留标毒、随后 claim 复活的交错；孤儿标记必须被旧 job 释放，
// 才能让同指纹的新 attempt 沿 released reactivate 链恢复服务。
// 变异：把 reconciliation_needed 也纳入复活拒绝分支时，job、窗口和后续 Reserve 断言都会变红。
func TestReconcilerReleaseAfterAbort_RevivedReconciliationNeededDetoxesAndReactivates(t *testing.T) {
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

	poisoned, poisonErr := service.Reserve(ctx, replayReleaseRecoveryReservation(seed, now.Add(time.Second)))
	if poisonErr == nil || poisoned.Allowed || poisoned.Reservation.Status != ReservationReconciliationNeeded {
		t.Fatalf("poisoned Reserve err/result=%v/%+v; want denied reconciliation_needed", poisonErr, poisoned)
	}

	reconciler := NewReconciler(service, store, ReconcilerOptions{Limit: 10})
	processed, err := reconciler.ReconcileDueJobs(ctx, f.tenantID, now, 10)
	if err != nil || processed != 1 {
		t.Fatalf("ReconcileDueJobs processed/err=%d/%v; want 1/nil", processed, err)
	}
	if state := f.reconcilerJobState(job.ID); state.status != "succeeded" {
		t.Fatalf("job state=%+v; want succeeded", state)
	}
	assertReleasedQuotaFixture(t, f, seed)

	reactivated, err := service.Reserve(ctx, replayReleaseRecoveryReservation(seed, now.Add(2*time.Second)))
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
