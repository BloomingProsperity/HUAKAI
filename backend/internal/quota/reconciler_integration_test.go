//go:build integration_pg

package quota

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// TestReconciler_ReconcilesSettleJobMovesReservedToSettled 守住 settle 补偿必须真正重放 Settle,
// 不能只把 job 标成功。Mutation: 不调 Settle 只 Complete job 时 cost window reserved 仍为 4。
func TestReconciler_ReconcilesSettleJobMovesReservedToSettled(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 5, 29, 9, 0, 0, 0, time.UTC)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, NewService(store), now, "reconcile-settle", "4", false)
	f.requireReservationReconciliation(ctx, store, reserve.Reservation)
	job := f.enqueueReconcilerJob(ctx, store, reserve.Reservation.ClaimID, &reserve.Reservation.ID, "settle_after_billing_success", now.Add(-time.Minute))

	processed, err := reconciler.ReconcileDueJobs(ctx, f.tenantID, now, 10)
	if err != nil {
		t.Fatalf("ReconcileDueJobs: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d; want 1", processed)
	}
	status, settledCost, _, _ := f.reservationSettlement(reserve.Reservation.ID)
	if status != ReservationSettled || !settledCost.Equal(decimal.NewFromInt(4)) {
		t.Fatalf("reservation status=%s settled_cost=%s; want settled with predicted-cost proxy 4", status, settledCost)
	}
	values := f.windowValues(costPolicy, now)
	if !values.reserved.Equal(decimal.Zero) || !values.settled.Equal(decimal.NewFromInt(4)) {
		t.Fatalf("cost window=%+v; want reserved=0 settled=4", values)
	}
	if got := f.reconcilerJobState(job.ID).status; got != "succeeded" {
		t.Fatalf("job status=%s; want succeeded", got)
	}
}

// TestReconciler_ReconcilesReleaseJobFreesHold 守住 abort 补偿必须释放 quota hold 和并发槽。
// Mutation: 漏调 Release 时 request/cost reserved 或 active slot 仍大于 0。
func TestReconciler_ReconcilesReleaseJobFreesHold(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 5, 29, 9, 10, 0, 0, time.UTC)
	requestPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricConcurrency, WindowNone, 0, "1", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, NewService(store), now, "reconcile-release", "4", true)
	f.requireReservationReconciliation(ctx, store, reserve.Reservation)
	job := f.enqueueReconcilerJob(ctx, store, reserve.Reservation.ClaimID, &reserve.Reservation.ID, "release_after_abort", now.Add(-time.Minute))

	processed, err := reconciler.ReconcileDueJobs(ctx, f.tenantID, now, 10)
	if err != nil {
		t.Fatalf("ReconcileDueJobs: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d; want 1", processed)
	}
	if status := f.reservationStatus(reserve.Reservation.ID); status != ReservationReleased {
		t.Fatalf("reservation status=%s; want released", status)
	}
	for name, values := range map[string]quotaWindowValues{
		"request": f.windowValues(requestPolicy, now),
		"cost":    f.windowValues(costPolicy, now),
	} {
		if !values.reserved.Equal(decimal.Zero) || !values.settled.Equal(decimal.Zero) {
			t.Fatalf("%s window=%+v; want reserved=0 settled=0", name, values)
		}
	}
	if got := f.activeSlotCount(ScopeUser, fmt.Sprint(f.userID)); got != 0 {
		t.Fatalf("active slots=%d; want 0 after reconciliation release", got)
	}
	if got := f.reconcilerJobState(job.ID).status; got != "succeeded" {
		t.Fatalf("job status=%s; want succeeded", got)
	}
}

// TestReconciler_ReconcilesCacheHitJobZeroCost 守住 cache-hit 补偿是成功零成本结算,
// 不是 abort release。Mutation: 走 abort 会让 request settled=0 且 reservation=released。
func TestReconciler_ReconcilesCacheHitJobZeroCost(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 5, 29, 9, 20, 0, 0, time.UTC)
	requestPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, NewService(store), now, "reconcile-cache-hit", "4", false)
	f.requireReservationReconciliation(ctx, store, reserve.Reservation)
	job := f.enqueueReconcilerJob(ctx, store, reserve.Reservation.ClaimID, &reserve.Reservation.ID, "release_after_cache_hit", now.Add(-time.Minute))

	processed, err := reconciler.ReconcileDueJobs(ctx, f.tenantID, now, 10)
	if err != nil {
		t.Fatalf("ReconcileDueJobs: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d; want 1", processed)
	}
	status, settledCost, _, _ := f.reservationSettlement(reserve.Reservation.ID)
	if status != ReservationSettled || !settledCost.Equal(decimal.Zero) {
		t.Fatalf("reservation status=%s settled_cost=%s; want settled zero-cost cache hit", status, settledCost)
	}
	requestValues := f.windowValues(requestPolicy, now)
	if !requestValues.reserved.Equal(decimal.Zero) || !requestValues.settled.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("request window=%+v; want reserved=0 settled=1", requestValues)
	}
	costValues := f.windowValues(costPolicy, now)
	if !costValues.reserved.Equal(decimal.Zero) || !costValues.settled.Equal(decimal.Zero) {
		t.Fatalf("cost window=%+v; want reserved=0 settled=0", costValues)
	}
	if got := f.reconcilerJobState(job.ID).status; got != "succeeded" {
		t.Fatalf("job status=%s; want succeeded", got)
	}
}

// TestReconciler_FailedReconcileBacksOffAndIncrementsAttempt 守住失败 job 不能误标成功,
// 且必须推进 attempt_count/next_run_at。Mutation: 失败却 Complete 会让 status=succeeded。
func TestReconciler_FailedReconcileBacksOffAndIncrementsAttempt(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 5, 29, 9, 30, 0, 0, time.UTC)
	claimID := f.seedClaim("reconcile-missing-reservation")
	job := f.enqueueReconcilerJob(ctx, store, claimID, nil, "settle_after_billing_success", now.Add(-time.Minute))

	processed, err := reconciler.ReconcileDueJobs(ctx, f.tenantID, now, 10)
	if err == nil {
		t.Fatal("ReconcileDueJobs err=nil; want missing reservation failure")
	}
	if processed != 0 {
		t.Fatalf("processed=%d; want 0 on failed job", processed)
	}
	state := f.reconcilerJobState(job.ID)
	if state.status != "failed" || state.attempts != 1 {
		t.Fatalf("job state=%+v; want failed attempt_count=1", state)
	}
	if !state.nextRunAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("next_run_at=%s; want %s", state.nextRunAt, now.Add(time.Minute))
	}
	if state.lastError == "" {
		t.Fatal("last_error empty; want failure reason recorded")
	}
}

// TestReconciler_IdempotentJobForAlreadySettledReservation 守住已 settled 的 settle job
// 只能幂等完成 job, 不能重复施加窗口结算。Mutation: 重跑 settlement 会让 settled 翻倍。
func TestReconciler_IdempotentJobForAlreadySettledReservation(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	service := NewService(store)
	now := time.Date(2026, 5, 29, 9, 40, 0, 0, time.UTC)
	requestPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "reconcile-idempotent-settle", "4", false)
	if _, err := service.Settle(ctx, SettleRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		ActualCost:    decimal.NewFromInt(4),
		SettledAt:     now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("initial Settle: %v", err)
	}
	job := f.enqueueReconcilerJob(ctx, store, reserve.Reservation.ClaimID, &reserve.Reservation.ID, "settle_after_billing_success", now.Add(-time.Minute))

	processed, err := reconciler.ReconcileDueJobs(ctx, f.tenantID, now, 10)
	if err != nil {
		t.Fatalf("ReconcileDueJobs: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d; want 1", processed)
	}
	if got := f.windowValues(requestPolicy, now).settled; !got.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("request settled=%s; want 1 after idempotent replay", got)
	}
	if got := f.windowValues(costPolicy, now).settled; !got.Equal(decimal.NewFromInt(4)) {
		t.Fatalf("cost settled=%s; want 4 after idempotent replay", got)
	}
	if got := f.reconcilerJobState(job.ID).status; got != "succeeded" {
		t.Fatalf("job status=%s; want succeeded", got)
	}
}

// TestReconciler_OnlyDueJobsProcessed 守住 reconciler 必须尊重 next_run_at,
// 不能处理未来 job。Mutation: 忽略 due filter 会让 future job 也 succeeded。
func TestReconciler_OnlyDueJobsProcessed(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 5, 29, 9, 50, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	dueReserve := f.reserveForSettlement(ctx, NewService(store), now, "reconcile-due", "4", false)
	f.requireReservationReconciliation(ctx, store, dueReserve.Reservation)
	futureReserve := f.reserveForSettlement(ctx, NewService(store), now, "reconcile-future", "4", false)
	f.requireReservationReconciliation(ctx, store, futureReserve.Reservation)
	dueJob := f.enqueueReconcilerJob(ctx, store, dueReserve.Reservation.ClaimID, &dueReserve.Reservation.ID, "settle_after_billing_success", now.Add(-time.Minute))
	futureJob := f.enqueueReconcilerJob(ctx, store, futureReserve.Reservation.ClaimID, &futureReserve.Reservation.ID, "settle_after_billing_success", now.Add(time.Hour))

	processed, err := reconciler.ReconcileDueJobs(ctx, f.tenantID, now, 10)
	if err != nil {
		t.Fatalf("ReconcileDueJobs: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d; want only due job processed", processed)
	}
	if got := f.reconcilerJobState(dueJob.ID).status; got != "succeeded" {
		t.Fatalf("due job status=%s; want succeeded", got)
	}
	futureState := f.reconcilerJobState(futureJob.ID)
	if futureState.status != "queued" || futureState.attempts != 0 {
		t.Fatalf("future job state=%+v; want queued attempt_count=0", futureState)
	}
	if status := f.reservationStatus(futureReserve.Reservation.ID); status != ReservationReconciliationNeeded {
		t.Fatalf("future reservation status=%s; want still reconciliation_needed", status)
	}
}

func newQuotaReconcilerRuntime(t *testing.T) (context.Context, *quotaFixture, PGStore, *Reconciler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	reconciler := NewReconciler(NewService(store), store, ReconcilerOptions{
		MaxAttempts: 10,
		BaseBackoff: time.Minute,
		MaxBackoff:  time.Hour,
		Limit:       10,
	})
	return ctx, f, store, reconciler
}

func (f *quotaFixture) requireReservationReconciliation(ctx context.Context, store PGStore, reservation Reservation) {
	f.t.Helper()
	if err := store.MarkReservationReconciliationNeeded(ctx, reservation.TenantID, reservation.ID, reservation.ClaimID); err != nil {
		f.t.Fatalf("MarkReservationReconciliationNeeded: %v", err)
	}
}

func (f *quotaFixture) enqueueReconcilerJob(ctx context.Context, store PGStore, claimID int64, reservationID *int64, kind string, nextRunAt time.Time) ReconciliationJob {
	f.t.Helper()
	job, err := store.EnqueueReconciliationJob(ctx, ReconciliationEnqueue{
		TenantID:      f.tenantID,
		ClaimID:       claimID,
		ReservationID: reservationID,
		Kind:          kind,
		LastError:     ptrString("seed-" + uuid.NewString()),
		NextRunAt:     nextRunAt,
	})
	if err != nil {
		f.t.Fatalf("EnqueueReconciliationJob %s: %v", kind, err)
	}
	return job
}

type reconcilerJobState struct {
	status    string
	attempts  int
	nextRunAt time.Time
	lastError string
}

func (f *quotaFixture) reconcilerJobState(jobID int64) reconcilerJobState {
	f.t.Helper()
	var state reconcilerJobState
	if err := f.pool.QueryRow(f.ctx,
		`SELECT status, attempt_count, next_run_at, COALESCE(last_error, '')
		 FROM quota_reconciliation_jobs
		 WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, jobID,
	).Scan(&state.status, &state.attempts, &state.nextRunAt, &state.lastError); err != nil {
		f.t.Fatalf("read reconciliation job %d: %v", jobID, err)
	}
	state.nextRunAt = state.nextRunAt.UTC()
	return state
}

func ptrString(value string) *string {
	return &value
}
