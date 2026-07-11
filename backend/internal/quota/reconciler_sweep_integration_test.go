//go:build integration_pg

package quota

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// expireReservationLease 把预留的 lease 拨到过去,模拟「进程死在 billing 终态与
// quota 补偿之间、job 从未入队」后时间流逝的孤儿态。
func (f *quotaFixture) expireReservationLease(reservationID int64, at time.Time) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE quota_reservations SET lease_expires_at=$3 WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, reservationID, at,
	); err != nil {
		f.t.Fatalf("expire reservation lease: %v", err)
	}
}

// setClaimTerminal 把 billing claim 拨到终态;actualCost 为空串时保持 NULL。
func (f *quotaFixture) setClaimTerminal(claimID int64, status string, actualCost string) {
	f.t.Helper()
	var err error
	if actualCost == "" {
		_, err = f.pool.Exec(f.ctx,
			`UPDATE billing_ledger_claims SET status=$3 WHERE tenant_id=$1 AND id=$2`,
			f.tenantID, claimID, status)
	} else {
		_, err = f.pool.Exec(f.ctx,
			`UPDATE billing_ledger_claims SET status=$3, actual_cost=$4::numeric WHERE tenant_id=$1 AND id=$2`,
			f.tenantID, claimID, status, actualCost)
	}
	if err != nil {
		f.t.Fatalf("set claim %d terminal %s: %v", claimID, status, err)
	}
}

func (f *quotaFixture) seedPendingPostDeliverySettlement(claimID int64) {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO usage_record_dlq (
			tenant_id, claim_id, payload, failure_reason, event_kind, lane, status,
			next_retry_at, lease_ttl, replica_status, replica_target, idempotency_key,
			source_table, source_id
		 ) VALUES (
			$1, $2, '{}', 'settlement_failed', 'post_delivery_settlement', 'HIGH', 'pending',
			NOW(), interval '30 seconds', 'none', 'primary', $3,
			'billing_ledger_claims', $2
		 ) RETURNING id`,
		f.tenantID, claimID, fmt.Sprintf("quota-protect:%d:%d", f.tenantID, claimID),
	).Scan(&id); err != nil {
		f.t.Fatalf("seed pending post-delivery settlement: %v", err)
	}
	f.t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM usage_record_dlq WHERE id=$1`, id)
	})
}

// TestReconcilerSweep_CommittedClaimSettlesWithClaimActualCost 守住清扫器对 committed claim
// 必须用 claim 的 actual_cost(权威金额)结算,而非 predicted_cost 保守代理。
// Mutation: ①清扫器不跑/查询漏取该行 → 预留仍 reserved、窗口 reserved 仍 4 → RED;
// ②把 actual 换成 predicted → settled_cost=4≠2 → RED。
func TestReconcilerSweep_CommittedClaimSettlesWithClaimActualCost(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, NewService(store), now, "sweep-committed", "4", false)
	f.expireReservationLease(reserve.Reservation.ID, now.Add(-time.Minute))
	f.setClaimTerminal(reserve.Reservation.ClaimID, "committed", "2")

	processed, err := reconciler.SweepStaleReservations(ctx, now, 10)
	if err != nil {
		t.Fatalf("SweepStaleReservations: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d; want 1", processed)
	}
	status, settledCost, _, _ := f.reservationSettlement(reserve.Reservation.ID)
	if status != ReservationSettled || !settledCost.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("reservation status=%s settled_cost=%s; want settled with claim actual 2", status, settledCost)
	}
	values := f.windowValues(costPolicy, now)
	if !values.reserved.Equal(decimal.Zero) || !values.settled.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("cost window=%+v; want reserved=0 settled=2", values)
	}
}

// TestReconcilerSweep_CommittedClaimNullActualFallsBackToPredicted 守住 claim actual_cost
// 为 NULL 时退回 predicted_cost,不得按 0 结算(漏计成本窗)。
// Mutation: 把 NULL 兜底删掉(恒用 ClaimActualCost)→ settled_cost=0 → RED。
func TestReconcilerSweep_CommittedClaimNullActualFallsBackToPredicted(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 7, 5, 9, 10, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, NewService(store), now, "sweep-null-actual", "4", false)
	f.expireReservationLease(reserve.Reservation.ID, now.Add(-time.Minute))
	f.setClaimTerminal(reserve.Reservation.ClaimID, "committed", "")

	processed, err := reconciler.SweepStaleReservations(ctx, now, 10)
	if err != nil {
		t.Fatalf("SweepStaleReservations: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d; want 1", processed)
	}
	status, settledCost, _, _ := f.reservationSettlement(reserve.Reservation.ID)
	if status != ReservationSettled || !settledCost.Equal(decimal.NewFromInt(4)) {
		t.Fatalf("reservation status=%s settled_cost=%s; want settled with predicted fallback 4", status, settledCost)
	}
}

// TestReconcilerSweep_AbortedClaimReleasesReservation 守住 aborted claim 的孤儿预留必须
// Release(窗口 headroom 归还),不得 Settle(把没发生的花费计入成本窗)。
// Mutation: aborted 分支也走 Settle → settled≠0 → RED;分支被删 → 仍 reserved → RED。
func TestReconcilerSweep_AbortedClaimReleasesReservation(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 7, 5, 9, 20, 0, 0, time.UTC)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, NewService(store), now, "sweep-aborted", "4", false)
	f.expireReservationLease(reserve.Reservation.ID, now.Add(-time.Minute))
	f.setClaimTerminal(reserve.Reservation.ClaimID, "aborted", "")

	processed, err := reconciler.SweepStaleReservations(ctx, now, 10)
	if err != nil {
		t.Fatalf("SweepStaleReservations: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d; want 1", processed)
	}
	status, settledCost, _, _ := f.reservationSettlement(reserve.Reservation.ID)
	if status != ReservationReleased || !settledCost.Equal(decimal.Zero) {
		t.Fatalf("reservation status=%s settled_cost=%s; want released with zero settle", status, settledCost)
	}
	values := f.windowValues(costPolicy, now)
	if !values.reserved.Equal(decimal.Zero) || !values.settled.Equal(decimal.Zero) {
		t.Fatalf("cost window=%+v; want reserved=0 settled=0 after release", values)
	}
}

// TestReconcilerSweep_SkipsLiveLeaseAndReservingClaim 守住清扫器的两条不动线:
// ①lease 未过期的预留不碰(在途请求);②claim 仍 reserving 的不碰(billing lease
// sweeper 先终结,乱动会跟真实结算竞争语义)。
// Mutation: 查询去掉 lease 过滤或 claim 终态过滤 → 有行被处理 → RED。
func TestReconcilerSweep_SkipsLiveLeaseAndReservingClaim(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 7, 5, 9, 30, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	service := NewService(store)

	// live lease + committed claim: lease 还有 5 分钟,不许碰。
	liveLease := f.reserveForSettlement(ctx, service, now, "sweep-live-lease", "4", false)
	f.setClaimTerminal(liveLease.Reservation.ClaimID, "committed", "3")

	// 过期 lease + reserving claim: billing 未终态,不许碰。
	reservingClaim := f.reserveForSettlement(ctx, service, now, "sweep-reserving", "4", false)
	f.expireReservationLease(reservingClaim.Reservation.ID, now.Add(-time.Minute))

	processed, err := reconciler.SweepStaleReservations(ctx, now, 10)
	if err != nil {
		t.Fatalf("SweepStaleReservations: %v", err)
	}
	if processed != 0 {
		t.Fatalf("processed=%d; want 0 (nothing eligible)", processed)
	}
	for label, id := range map[string]int64{
		"live-lease": liveLease.Reservation.ID,
		"reserving":  reservingClaim.Reservation.ID,
	} {
		status, _, _, _ := f.reservationSettlement(id)
		if status != ReservationReserved {
			t.Fatalf("%s reservation status=%s; want untouched reserved", label, status)
		}
	}
}

func TestReconcilerSweep_SkipsUnresolvedPostDeliverySettlement(t *testing.T) {
	// 变异检查:移除 quota reservation 或 concurrency slot 的恢复行排除条件会释放已交付资源。
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 7, 5, 9, 32, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricConcurrency, WindowNone, 0, "1", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, NewService(store), now, "sweep-delivered-unsettled", "4", true)
	f.expireReservationLease(reserve.Reservation.ID, now.Add(-time.Minute))
	f.setClaimTerminal(reserve.Reservation.ClaimID, "aborted", "")
	if _, err := f.pool.Exec(ctx,
		`UPDATE quota_concurrency_slots
		 SET lease_expires_at=$3
		 WHERE tenant_id=$1 AND reservation_id=$2`,
		f.tenantID, reserve.Reservation.ID, now.Add(-time.Minute),
	); err != nil {
		t.Fatalf("expire concurrency slot: %v", err)
	}
	f.seedPendingPostDeliverySettlement(reserve.Reservation.ClaimID)

	if err := store.ExpireConcurrencySlots(ctx, f.tenantID, now); err != nil {
		t.Fatalf("ExpireConcurrencySlots: %v", err)
	}
	if got := f.activeSlotCount(ScopeUser, fmt.Sprint(f.userID)); got != 1 {
		t.Fatalf("active slots=%d want 1 while settlement unresolved", got)
	}
	processed, err := reconciler.SweepStaleReservations(ctx, now, 10)
	if err != nil {
		t.Fatalf("SweepStaleReservations: %v", err)
	}
	if processed != 0 {
		t.Fatalf("processed=%d want 0 while settlement unresolved", processed)
	}
	if status := f.reservationStatus(reserve.Reservation.ID); status != ReservationReserved {
		t.Fatalf("reservation status=%s want reserved", status)
	}
}

// TestReconcilerSweep_IneligibleRowsDoNotConsumeLimit 守住 SQL 侧必须过滤 claim 终态:
// 即使 Go 侧 switch 会跳过 reserving 行,让它进结果集也会吃掉 LIMIT 预算,饿死排在
// 后面的合格行(lease 更旧的 reserving 行永远排前面 → 合格行永远轮不上)。
// Mutation: 查询去掉 blc.status 终态过滤 → limit=1 只捞到 reserving 行 → processed=0 → RED。
func TestReconcilerSweep_IneligibleRowsDoNotConsumeLimit(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 7, 5, 9, 35, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	service := NewService(store)

	// reserving 行 lease 更旧(排序在前);committed 行随后。
	reserving := f.reserveForSettlement(ctx, service, now, "sweep-starve-reserving", "4", false)
	f.expireReservationLease(reserving.Reservation.ID, now.Add(-2*time.Minute))
	committed := f.reserveForSettlement(ctx, service, now, "sweep-starve-committed", "4", false)
	f.expireReservationLease(committed.Reservation.ID, now.Add(-time.Minute))
	f.setClaimTerminal(committed.Reservation.ClaimID, "committed", "2")

	processed, err := reconciler.SweepStaleReservations(ctx, now, 1)
	if err != nil {
		t.Fatalf("SweepStaleReservations: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d; want 1 (limit 预算不得被不合格行吃掉)", processed)
	}
	status, settledCost, _, _ := f.reservationSettlement(committed.Reservation.ID)
	if status != ReservationSettled || !settledCost.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("committed reservation status=%s cost=%s; want settled 2", status, settledCost)
	}
}

// seedFailedReconciliationJob 直插一条 failed 态补偿 job,模拟「重放已失败、正在退避」的行。
func (f *quotaFixture) seedFailedReconciliationJob(claimID int64, kind string, at time.Time) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO quota_reconciliation_jobs (tenant_id, claim_id, job_kind, status, attempt_count, next_run_at, created_at, updated_at)
		 VALUES ($1, $2, $3, 'failed', 3, $4, $4, $4)`,
		f.tenantID, claimID, kind, at,
	); err != nil {
		f.t.Fatalf("seed failed reconciliation job: %v", err)
	}
}

// TestReconcilerSweep_SkipsRowsWithJobHistory 守住清扫段不得碰有补偿 job 史的行:
// job 段自带退避与终态停靠,清扫段每轮重试会击穿它(每分钟 re-enqueue 一条新 job,
// attempt_count 永远打不到上限,job 表无界增长)。
// Mutation: 查询去掉 NOT EXISTS 反连接 → failed job 的行被清扫处理 → RED。
func TestReconcilerSweep_SkipsRowsWithJobHistory(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 7, 5, 9, 45, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, NewService(store), now, "sweep-job-history", "4", false)
	f.expireReservationLease(reserve.Reservation.ID, now.Add(-time.Minute))
	f.setClaimTerminal(reserve.Reservation.ClaimID, "committed", "2")
	f.seedFailedReconciliationJob(reserve.Reservation.ClaimID, "settle_after_billing_success", now.Add(time.Hour))

	processed, err := reconciler.SweepStaleReservations(ctx, now, 10)
	if err != nil {
		t.Fatalf("SweepStaleReservations: %v", err)
	}
	if processed != 0 {
		t.Fatalf("processed=%d; want 0 (有 job 史的行归 job 段)", processed)
	}
	status, _, _, _ := f.reservationSettlement(reserve.Reservation.ID)
	if status != ReservationReserved {
		t.Fatalf("reservation status=%s; want untouched reserved", status)
	}
}

// TestReconcilerSweep_RowRecheckSkipsRevivedClaim 守住每行动作前的现状复核:
// list 快照后 claim 被复活(aborted→reserving)或预留 lease 被 reactivate 续期时,
// 必须跳过,不得按 stale 快照 Release 在途预留(并发槽/headroom 会被中途归还)。
// Mutation: sweepStaleRow 删掉复核直接按快照动作 → 复活行被 Release → RED。
func TestReconcilerSweep_RowRecheckSkipsRevivedClaim(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 7, 5, 9, 50, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	service := NewService(store)

	// 子场景1: 快照声称 aborted,现状已复活为 reserving → 跳过。
	revived := f.reserveForSettlement(ctx, service, now, "sweep-revived", "4", false)
	f.expireReservationLease(revived.Reservation.ID, now.Add(-time.Minute))
	staleRow := StaleReservation{
		TenantID:      f.tenantID,
		ReservationID: revived.Reservation.ID,
		ClaimID:       revived.Reservation.ClaimID,
		PredictedCost: decimal.NewFromInt(4),
		ClaimStatus:   "aborted", // stale 快照
	}
	acted, err := reconciler.sweepStaleRow(ctx, now, staleRow)
	if err != nil {
		t.Fatalf("sweepStaleRow revived: %v", err)
	}
	if acted {
		t.Fatal("acted=true; claim 现状 reserving 必须跳过")
	}
	status, _, _, _ := f.reservationSettlement(revived.Reservation.ID)
	if status != ReservationReserved {
		t.Fatalf("reservation status=%s; want untouched reserved", status)
	}

	// 子场景2: 快照后 lease 被续期(reactivate 语义)→ 跳过。
	renewed := f.reserveForSettlement(ctx, service, now, "sweep-renewed", "4", false)
	f.setClaimTerminal(renewed.Reservation.ClaimID, "aborted", "")
	f.expireReservationLease(renewed.Reservation.ID, now.Add(30*time.Minute)) // 现状 lease 在未来
	renewedRow := StaleReservation{
		TenantID:      f.tenantID,
		ReservationID: renewed.Reservation.ID,
		ClaimID:       renewed.Reservation.ClaimID,
		PredictedCost: decimal.NewFromInt(4),
		ClaimStatus:   "aborted",
	}
	acted, err = reconciler.sweepStaleRow(ctx, now, renewedRow)
	if err != nil {
		t.Fatalf("sweepStaleRow renewed: %v", err)
	}
	if acted {
		t.Fatal("acted=true; lease 已续期(在途)必须跳过")
	}
	status, _, _, _ = f.reservationSettlement(renewed.Reservation.ID)
	if status != ReservationReserved {
		t.Fatalf("renewed reservation status=%s; want untouched reserved", status)
	}
}

// TestReconciler_JobReplayUsesClaimActualCost 守住 job 重放段与清扫段同一金额口径:
// claim 已写实结额时必须用它,不得恒用 predicted(同类孤儿两段结出不同金额)。
// Mutation: replayJob settle 分支回退恒用 predicted → settled_cost=4≠2 → RED。
func TestReconciler_JobReplayUsesClaimActualCost(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 7, 5, 9, 55, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, NewService(store), now, "job-actual-cost", "4", false)
	f.requireReservationReconciliation(ctx, store, reserve.Reservation)
	f.setClaimTerminal(reserve.Reservation.ClaimID, "committed", "2")
	f.enqueueReconcilerJob(ctx, store, reserve.Reservation.ClaimID, &reserve.Reservation.ID, "settle_after_billing_success", now.Add(-time.Minute))

	processed, err := reconciler.ReconcileDueJobs(ctx, f.tenantID, now, 10)
	if err != nil {
		t.Fatalf("ReconcileDueJobs: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d; want 1", processed)
	}
	status, settledCost, _, _ := f.reservationSettlement(reserve.Reservation.ID)
	if status != ReservationSettled || !settledCost.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("reservation status=%s settled_cost=%s; want settled with claim actual 2", status, settledCost)
	}
}

// TestReconciliationWorker_RunOnceIncludesStaleSweep 守住 worker 单轮必须带清扫段:
// job 表为空(崩溃窗口下本来就没有 job)时,孤儿预留仍要在一轮内被补偿。
// Mutation: RunOnce 去掉 SweepStaleReservations 调用 → processed=0、预留仍 reserved → RED。
func TestReconciliationWorker_RunOnceIncludesStaleSweep(t *testing.T) {
	ctx, f, store, reconciler := newQuotaReconcilerRuntime(t)
	now := time.Date(2026, 7, 5, 9, 40, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, NewService(store), now, "sweep-worker", "4", false)
	f.expireReservationLease(reserve.Reservation.ID, now.Add(-time.Minute))
	f.setClaimTerminal(reserve.Reservation.ClaimID, "committed", "2")

	worker := NewReconciliationWorker(reconciler, 0)
	processed, err := worker.RunOnce(ctx, now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d; want 1 from sweep segment", processed)
	}
	status, settledCost, _, _ := f.reservationSettlement(reserve.Reservation.ID)
	if status != ReservationSettled || !settledCost.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("reservation status=%s settled_cost=%s; want settled 2 via worker round", status, settledCost)
	}
}
