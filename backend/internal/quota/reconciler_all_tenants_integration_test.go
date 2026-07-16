//go:build integration_pg

package quota

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestReconciliationWorker_SweepsCrossTenant 证明跨租户全局 sweep(缺口② 修复核心 +
// 单租户盲区回归护栏):两个独立租户各有一个到期 release job,worker.RunOnce 走
// ReconcileAllTenants 一轮把两个租户的 job 都处理掉、两个 reservation 都释放。
//
// 变异判据(两条,任一命中即证测试判别):
//   - 把 ReconcileAllTenants 的租户遍历改成只处理第一个(break after first)→ 第二租户
//     reservation 仍卡 reconciliation_needed → 本测试红。
//   - 把 ListTenantsWithDueReconciliationJobs 换回单租户 ListDueReconciliationJobs 语义
//     (只返一个租户)→ 漏扫另一租户 → 本测试红。
//
// 这正是 P1 E2E 抓到的「reconciler 死代码 + 单租户盲区」缺口的判别性守护:老 ReconcileDueJobs
// 只扫单租户、且从未接线;本切片让 worker 跨租户真跑。
func TestReconciliationWorker_SweepsCrossTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	store := NewPostgresStore(pool)
	reconciler := NewReconciler(NewService(store), store, ReconcilerOptions{Limit: 10, TenantSweep: 50})
	worker := NewReconciliationWorker(reconciler, time.Minute)
	now := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	// 每个租户:seed 三条 enforce policy + 预扣一笔 + 标记需对账 + 入队一个到期 release job。
	setupTenant := func(tag string) (*quotaFixture, int64) {
		f := newQuotaFixture(t, ctx, pool)
		f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
		f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
		f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricConcurrency, WindowNone, 0, "1", ModeEnforce)
		reserve := f.reserveForSettlement(ctx, NewService(store), now, "sweep-"+tag, "4", true)
		f.requireReservationReconciliation(ctx, store, reserve.Reservation)
		f.setClaimTerminal(reserve.Reservation.ClaimID, claimStatusAborted, "")
		f.enqueueReconcilerJob(ctx, store, reserve.Reservation.ClaimID, &reserve.Reservation.ID, "release_after_abort", now.Add(-time.Minute))
		return f, reserve.Reservation.ID
	}
	fA, resA := setupTenant("A")
	fB, resB := setupTenant("B")

	processed, err := worker.RunOnce(ctx, now)
	if err != nil {
		t.Fatalf("worker.RunOnce: %v", err)
	}
	if processed != 2 {
		t.Fatalf("processed=%d; want 2(两租户各一个到期 job)", processed)
	}
	if s := fA.reservationStatus(resA); s != ReservationReleased {
		t.Fatalf("租户A reservation=%s; want released", s)
	}
	if s := fB.reservationStatus(resB); s != ReservationReleased {
		t.Fatalf("租户B reservation=%s; want released(单租户盲区回归:第二租户被漏扫)", s)
	}
}
