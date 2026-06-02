//go:build integration_pg

package quota

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// TestServiceSettle_OverageIsBudgetOverrunNotEstimateMiss 守住 Owner 决策:
// overage 是窗口预算超支的边际增量, 不是 actual-predicted 的估算误差。
// Mutation: 把公式换成 max(0, actual-predicted), A 会得 3 而不是 2, B 会得 2 而不是 0。
func TestServiceSettle_OverageIsBudgetOverrunNotEstimateMiss(t *testing.T) {
	t.Run("over_limit_margin", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		pool := openQuotaIntegrationPool(t, ctx)
		f := newQuotaFixture(t, ctx, pool)
		service := NewService(NewPostgresStore(pool))

		now := time.Date(2026, 5, 28, 16, 0, 0, 0, time.UTC)
		costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "10", ModeEnforce)
		f.seedWindow(costPolicy, now, "0", "5", 0)
		reserve := f.reserveForSettlement(ctx, service, now, "overage-a", "4", false)

		result, err := service.Settle(ctx, SettleRequest{
			TenantID:      f.tenantID,
			ClaimID:       reserve.Reservation.ClaimID,
			ReservationID: reserve.Reservation.ID,
			ActualCost:    decimal.RequireFromString("7"),
			SettledAt:     now.Add(time.Minute),
		})
		if err != nil {
			t.Fatalf("Settle: %v", err)
		}
		if !result.OverageValue.Equal(decimal.NewFromInt(2)) {
			t.Fatalf("overage=%s; want budget overrun margin 2", result.OverageValue)
		}
		values := f.windowValues(costPolicy, now)
		if !values.reserved.Equal(decimal.Zero) || !values.settled.Equal(decimal.NewFromInt(12)) || !values.overage.Equal(decimal.NewFromInt(2)) {
			t.Fatalf("cost window=%+v; want reserved=0 settled=12 overage=2", values)
		}
	})

	t.Run("estimate_miss_but_under_limit", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		pool := openQuotaIntegrationPool(t, ctx)
		f := newQuotaFixture(t, ctx, pool)
		service := NewService(NewPostgresStore(pool))

		now := time.Date(2026, 5, 28, 16, 10, 0, 0, time.UTC)
		costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "10", ModeEnforce)
		f.seedWindow(costPolicy, now, "0", "0", 0)
		reserve := f.reserveForSettlement(ctx, service, now, "overage-b", "4", false)

		result, err := service.Settle(ctx, SettleRequest{
			TenantID:      f.tenantID,
			ClaimID:       reserve.Reservation.ClaimID,
			ReservationID: reserve.Reservation.ID,
			ActualCost:    decimal.RequireFromString("6"),
			SettledAt:     now.Add(time.Minute),
		})
		if err != nil {
			t.Fatalf("Settle: %v", err)
		}
		if !result.OverageValue.Equal(decimal.Zero) {
			t.Fatalf("overage=%s; want 0 while committedAfter stays under limit", result.OverageValue)
		}
		values := f.windowValues(costPolicy, now)
		if !values.reserved.Equal(decimal.Zero) || !values.settled.Equal(decimal.NewFromInt(6)) || !values.overage.Equal(decimal.Zero) {
			t.Fatalf("cost window=%+v; want reserved=0 settled=6 overage=0", values)
		}
	})
}

// TestServiceSettle_MovesReservedToSettledActualNotPredicted 守住 settle 必须释放 predicted hold,
// 并把 actual 写入 settled。Mutation: 用 predicted 写 settled 时 cost settled 会是 4 而不是 6。
func TestServiceSettle_MovesReservedToSettledActualNotPredicted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 16, 20, 0, 0, time.UTC)
	requestPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "actual-not-predicted", "4", false)

	if _, err := service.Settle(ctx, SettleRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		ActualCost:    decimal.RequireFromString("6"),
		SettledAt:     now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	requestValues := f.windowValues(requestPolicy, now)
	if !requestValues.reserved.Equal(decimal.Zero) || !requestValues.settled.Equal(decimal.NewFromInt(1)) || requestValues.requestCount != 1 {
		t.Fatalf("request window=%+v; want reserved=0 settled=1 request_count=1", requestValues)
	}
	costValues := f.windowValues(costPolicy, now)
	if !costValues.reserved.Equal(decimal.Zero) || !costValues.settled.Equal(decimal.NewFromInt(6)) {
		t.Fatalf("cost window=%+v; want reserved=0 settled=6 actual cost", costValues)
	}
	status, settledCost, settledUnits, overageUnits := f.reservationSettlement(reserve.Reservation.ID)
	if status != ReservationSettled || !settledCost.Equal(decimal.NewFromInt(6)) || !settledUnits.Equal(decimal.NewFromInt(1)) || !overageUnits.Equal(decimal.Zero) {
		t.Fatalf("reservation status=%s settled_cost=%s settled_units=%s overage=%s; want settled/6/1/0", status, settledCost, settledUnits, overageUnits)
	}
}

// TestServiceSettle_CrossWindowFinalizationReleasesReservedWindow 守住 settle
// 必须命中 reserve 时写入 hold 的原窗口。Mutation: 用 SettledAt 重算窗口会把 settled 写到 W1,
// 而 W0 的 reserved_value 仍然滞留。
func TestServiceSettle_CrossWindowFinalizationReleasesReservedWindow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	reservedAt := time.Date(2026, 5, 28, 10, 0, 30, 0, time.UTC)
	settledAt := time.Date(2026, 5, 28, 10, 1, 30, 0, time.UTC)
	requestPolicy := f.seedPolicyWithMode(reservedAt, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 60, "100", ModeEnforce)
	costPolicy := f.seedPolicyWithMode(reservedAt, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 60, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, reservedAt, "cross-window-finalization", "4", false)

	if _, err := service.Settle(ctx, SettleRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		ActualCost:    decimal.RequireFromString("6"),
		SettledAt:     settledAt,
	}); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	requestW0 := f.windowValues(requestPolicy, reservedAt)
	if !requestW0.reserved.Equal(decimal.Zero) || !requestW0.settled.Equal(decimal.NewFromInt(1)) || requestW0.requestCount != 1 {
		t.Fatalf("request W0 window=%+v; want reserved=0 settled=1 request_count=1", requestW0)
	}
	costW0 := f.windowValues(costPolicy, reservedAt)
	if !costW0.reserved.Equal(decimal.Zero) || !costW0.settled.Equal(decimal.NewFromInt(6)) || !costW0.overage.Equal(decimal.Zero) {
		t.Fatalf("cost W0 window=%+v; want reserved=0 settled=6 overage=0", costW0)
	}

	for name, values := range map[string]quotaWindowValues{
		"request W1": f.windowValuesOrZero(requestPolicy, settledAt),
		"cost W1":    f.windowValuesOrZero(costPolicy, settledAt),
	} {
		if !values.reserved.Equal(decimal.Zero) || !values.settled.Equal(decimal.Zero) || !values.overage.Equal(decimal.Zero) || values.requestCount != 0 {
			t.Fatalf("%s window=%+v; want no quota movement in finalization-time window", name, values)
		}
	}
	if status := f.reservationStatus(reserve.Reservation.ID); status != ReservationSettled {
		t.Fatalf("reservation status=%s; want settled", status)
	}
}

// TestServiceSettle_IdempotentSameClaim 守住二次 settle 不重复写窗口/槽/audit。
// Mutation: 已 settled 仍重跑 ApplyWindowSettlement 时 request/cost settled 会翻倍。
func TestServiceSettle_IdempotentSameClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 16, 30, 0, 0, time.UTC)
	requestPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "settle-idempotent", "4", false)
	req := SettleRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		ActualCost:    decimal.RequireFromString("6"),
		SettledAt:     now.Add(time.Minute),
	}

	first, err := service.Settle(ctx, req)
	if err != nil {
		t.Fatalf("first Settle: %v", err)
	}
	second, err := service.Settle(ctx, req)
	if err != nil {
		t.Fatalf("second Settle: %v", err)
	}
	if first.IdempotencyHit || !second.IdempotencyHit {
		t.Fatalf("idempotency first=%v second=%v; want false/true", first.IdempotencyHit, second.IdempotencyHit)
	}
	if got := f.windowValues(requestPolicy, now).settled; !got.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("request settled=%s; want 1 after duplicate Settle", got)
	}
	if got := f.windowValues(costPolicy, now).settled; !got.Equal(decimal.NewFromInt(6)) {
		t.Fatalf("cost settled=%s; want 6 after duplicate Settle", got)
	}
	if got := f.auditSemanticCount("settle_committed"); got != 1 {
		t.Fatalf("settle_committed audit count=%d; want 1", got)
	}
}

// TestServiceRelease_AbortReleasesWindowsAndSlotsWithoutCost 守住 abort 只能释放 hold 和槽,
// 不能扣成本或写 overage。Mutation: 漏 ReleaseConcurrencySlots / 把 release 当 settle / 只释放 cost 不释放 requests 会变红。
func TestServiceRelease_AbortReleasesWindowsAndSlotsWithoutCost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 16, 40, 0, 0, time.UTC)
	requestPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricConcurrency, WindowNone, 0, "1", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "release-abort", "4", true)
	if got := f.activeSlotCount(ScopeUser, fmt.Sprint(f.userID)); got != 1 {
		t.Fatalf("active slots before release=%d; want 1", got)
	}

	result, err := service.Release(ctx, ReleaseRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		Reason:        "abort",
		ReleasedAt:    now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if result.Reservation.Status != ReservationReleased {
		t.Fatalf("result reservation status=%s; want released", result.Reservation.Status)
	}
	if got := f.activeSlotCount(ScopeUser, fmt.Sprint(f.userID)); got != 0 {
		t.Fatalf("active slots after release=%d; want 0", got)
	}
	for name, values := range map[string]quotaWindowValues{
		"request": f.windowValues(requestPolicy, now),
		"cost":    f.windowValues(costPolicy, now),
	} {
		if !values.reserved.Equal(decimal.Zero) || !values.settled.Equal(decimal.Zero) || !values.overage.Equal(decimal.Zero) {
			t.Fatalf("%s window=%+v; want all quota values released to 0", name, values)
		}
	}
	if status := f.reservationStatus(reserve.Reservation.ID); status != ReservationReleased {
		t.Fatalf("reservation status=%s; want released", status)
	}
	auditReserved, auditSettled := f.finalizationAuditAmounts("release_aborted", MetricRequests)
	if !auditReserved.Equal(decimal.NewFromInt(1)) || !auditSettled.Equal(decimal.Zero) {
		t.Fatalf("release requests audit reserved=%s settled=%s; want request units 1/0", auditReserved, auditSettled)
	}
}

// TestServiceRelease_IdempotentAfterReleased 守住二次 release 不把 reserved 扣负,
// 也不写第二条关键 audit。Mutation: 不查 released 状态直接重复 ApplyWindowSettlement 会变红。
func TestServiceRelease_IdempotentAfterReleased(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 16, 50, 0, 0, time.UTC)
	requestPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "release-idempotent", "4", false)
	req := ReleaseRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		Reason:        "caller_cancelled",
		ReleasedAt:    now.Add(time.Minute),
	}

	first, err := service.Release(ctx, req)
	if err != nil {
		t.Fatalf("first Release: %v", err)
	}
	second, err := service.Release(ctx, req)
	if err != nil {
		t.Fatalf("second Release: %v", err)
	}
	if first.IdempotencyHit || !second.IdempotencyHit {
		t.Fatalf("idempotency first=%v second=%v; want false/true", first.IdempotencyHit, second.IdempotencyHit)
	}
	if got := f.windowValues(requestPolicy, now).reserved; !got.Equal(decimal.Zero) {
		t.Fatalf("request reserved=%s; want 0 after duplicate Release", got)
	}
	if got := f.windowValues(costPolicy, now).reserved; !got.Equal(decimal.Zero) {
		t.Fatalf("cost reserved=%s; want 0 after duplicate Release", got)
	}
	if got := f.auditSemanticCount("release_aborted"); got != 1 {
		t.Fatalf("release_aborted audit count=%d; want 1", got)
	}
}

// TestServiceCommitCacheHit_CountsRequestZeroCost 守住 cache-hit 是成功路径:
// request 计入 settled, cost 为 0, reservation 为 settled。Mutation: 走 Release 分支会让 request settled=0 且 status=released。
func TestServiceCommitCacheHit_CountsRequestZeroCost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 17, 0, 0, 0, time.UTC)
	requestPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "cache-hit", "4", false)

	result, err := service.CommitCacheHit(ctx, CacheHitRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		CommittedAt:   now.Add(time.Minute),
		CacheKey:      "cache-key-" + uuid.NewString(),
		CacheSource:   "response_cache",
	})
	if err != nil {
		t.Fatalf("CommitCacheHit: %v", err)
	}
	if result.Reservation.Status != ReservationSettled {
		t.Fatalf("result=%+v; want settled cache hit", result)
	}
	requestValues := f.windowValues(requestPolicy, now)
	costValues := f.windowValues(costPolicy, now)
	if !requestValues.reserved.Equal(decimal.Zero) || !requestValues.settled.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("request window=%+v; want reserved=0 settled=1", requestValues)
	}
	if !costValues.reserved.Equal(decimal.Zero) || !costValues.settled.Equal(decimal.Zero) || !costValues.overage.Equal(decimal.Zero) {
		t.Fatalf("cost window=%+v; want reserved=0 settled=0 overage=0", costValues)
	}
	if got := f.auditSemanticCount("cache_hit"); got != 1 {
		t.Fatalf("cache_hit audit count=%d; want 1", got)
	}
	auditReserved, auditSettled := f.finalizationAuditAmounts("cache_hit", MetricRequests)
	if !auditReserved.Equal(decimal.NewFromInt(1)) || !auditSettled.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("cache_hit requests audit reserved=%s settled=%s; want request units 1/1", auditReserved, auditSettled)
	}
}

// TestServiceCommitCacheHit_Idempotent 守住重复 cache-hit 不重复 request settled。
// Mutation: 已 settled 分支重跑 settlement 时 request settled 会变 2。
func TestServiceCommitCacheHit_Idempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 17, 10, 0, 0, time.UTC)
	requestPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "cache-hit-idempotent", "4", false)
	req := CacheHitRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		CommittedAt:   now.Add(time.Minute),
		CacheKey:      "idem-cache",
		CacheSource:   "response_cache",
	}

	first, err := service.CommitCacheHit(ctx, req)
	if err != nil {
		t.Fatalf("first CommitCacheHit: %v", err)
	}
	second, err := service.CommitCacheHit(ctx, req)
	if err != nil {
		t.Fatalf("second CommitCacheHit: %v", err)
	}
	if first.IdempotencyHit || !second.IdempotencyHit {
		t.Fatalf("idempotency first=%v second=%v; want false/true", first.IdempotencyHit, second.IdempotencyHit)
	}
	if got := f.windowValues(requestPolicy, now).settled; !got.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("request settled=%s; want 1 after duplicate cache hit", got)
	}
	if got := f.auditSemanticCount("cache_hit"); got != 1 {
		t.Fatalf("cache_hit audit count=%d; want 1", got)
	}
}

// TestServiceSettle_UsesReservationScopesAtSettlement 守住 settle 必须用 reservation.Scopes
// 重解析策略, 而不是从 settle request 读取空 scope 或默认 tenant-only。Mutation: 改成默认 global 会让 user/api_key 窗口仍被占用。
func TestServiceSettle_UsesReservationScopesAtSettlement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 17, 20, 0, 0, time.UTC)
	userPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	keyPolicy := f.seedPolicyWithMode(now, ScopeAPIKey, fmt.Sprint(f.apiKeyID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "reservation-scopes", "4", false)

	if _, err := service.Settle(ctx, SettleRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		ActualCost:    decimal.RequireFromString("2"),
		SettledAt:     now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	for name, policyID := range map[string]int64{"user": userPolicy, "api_key": keyPolicy} {
		values := f.windowValues(policyID, now)
		if !values.reserved.Equal(decimal.Zero) || !values.settled.Equal(decimal.NewFromInt(2)) {
			t.Fatalf("%s policy window=%+v; want reservation scope released and settled actual=2", name, values)
		}
	}
}

// TestServiceSettle_FailureQueuesReconciliationOutsideFailedTx 守住主 quota tx 失败后,
// reconciliation 必须在失败 tx 之外入队。Mutation: 失败直接返回会没有 queued job, reservation 也不会进入补偿状态。
func TestServiceSettle_FailureQueuesReconciliationOutsideFailedTx(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 17, 30, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "settle-reconcile", "4", false)
	f.installAuditFailureTrigger("forced quota audit failure b2b")

	result, err := service.Settle(ctx, SettleRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		ActualCost:    decimal.RequireFromString("6"),
		SettledAt:     now.Add(time.Minute),
	})
	if err == nil {
		t.Fatal("Settle err=nil; want forced primary tx failure")
	}
	if !result.ReconciliationQueued {
		t.Fatalf("ReconciliationQueued=false; want true after enqueue")
	}
	if status := f.reservationStatus(reserve.Reservation.ID); status != ReservationReconciliationNeeded {
		t.Fatalf("reservation status=%s; want reconciliation_needed after failed tx", status)
	}
	if got := f.reconciliationJobCount("quota_settle_failed"); got != 1 {
		t.Fatalf("quota_settle_failed job count=%d; want 1", got)
	}
}

// TestServiceSettle_ReconciliationEnqueueFailureIsNotSwallowed 守住主 tx 和补偿入队都失败时,
// 调用方必须看见组合错误且 ReconciliationQueued=false。Mutation: 吞掉 enqueue 失败会让本测试误判补偿已安排。
func TestServiceSettle_ReconciliationEnqueueFailureIsNotSwallowed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 17, 40, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "settle-reconcile-fails", "4", false)
	f.installAuditFailureTrigger("forced quota audit failure b2b")
	f.installReconciliationFailureTrigger("forced quota reconciliation enqueue failure b2b")

	result, err := service.Settle(ctx, SettleRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		ActualCost:    decimal.RequireFromString("6"),
		SettledAt:     now.Add(time.Minute),
	})
	if err == nil {
		t.Fatal("Settle err=nil; want primary + enqueue failures")
	}
	if result.ReconciliationQueued {
		t.Fatalf("ReconciliationQueued=true; want false when enqueue fails")
	}
	errText := err.Error()
	if !strings.Contains(errText, "forced quota audit failure b2b") || !strings.Contains(errText, "forced quota reconciliation enqueue failure b2b") {
		t.Fatalf("err=%q; want both primary and enqueue failures", errText)
	}
}

type quotaWindowValues struct {
	reserved     decimal.Decimal
	settled      decimal.Decimal
	overage      decimal.Decimal
	requestCount int64
}

func (f *quotaFixture) reserveForSettlement(ctx context.Context, service *Service, at time.Time, label string, predicted string, needSlot bool) ReserveResult {
	f.t.Helper()
	claimID := f.seedClaim(label)
	result, err := service.Reserve(ctx, ReserveRequest{
		TenantID:            f.tenantID,
		ClaimID:             claimID,
		RequestFingerprint:  label + "-" + uuid.NewString(),
		Scopes:              f.reserveScopes(),
		PredictedCost:       decimal.RequireFromString(predicted),
		NeedConcurrencySlot: needSlot,
		LeaseExpiresAt:      at.Add(5 * time.Minute),
		At:                  at,
	})
	if err != nil {
		f.t.Fatalf("Reserve %s: %v", label, err)
	}
	if !result.Allowed || result.Reservation.ID == 0 {
		f.t.Fatalf("Reserve %s result=%+v; want allowed reservation", label, result)
	}
	return result
}

func (f *quotaFixture) windowValues(policyID int64, at time.Time) quotaWindowValues {
	f.t.Helper()
	window := f.policyWindow(policyID, at)
	var rawReserved, rawSettled, rawOverage string
	var requestCount int64
	if err := f.pool.QueryRow(f.ctx,
		`SELECT reserved_value::text, settled_value::text, overage_value::text, request_count
		 FROM quota_windows
		 WHERE tenant_id=$1 AND policy_id=$2 AND window_start=$3`,
		f.tenantID, policyID, window.Start,
	).Scan(&rawReserved, &rawSettled, &rawOverage, &requestCount); err != nil {
		f.t.Fatalf("read window values: %v", err)
	}
	return quotaWindowValues{
		reserved:     mustDecimal(f.t, rawReserved),
		settled:      mustDecimal(f.t, rawSettled),
		overage:      mustDecimal(f.t, rawOverage),
		requestCount: requestCount,
	}
}

func (f *quotaFixture) windowValuesOrZero(policyID int64, at time.Time) quotaWindowValues {
	f.t.Helper()
	window := f.policyWindow(policyID, at)
	var rawReserved, rawSettled, rawOverage string
	var requestCount int64
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COALESCE(SUM(reserved_value), 0)::text,
		        COALESCE(SUM(settled_value), 0)::text,
		        COALESCE(SUM(overage_value), 0)::text,
		        COALESCE(SUM(request_count), 0)::bigint
		 FROM quota_windows
		 WHERE tenant_id=$1 AND policy_id=$2 AND window_start=$3`,
		f.tenantID, policyID, window.Start,
	).Scan(&rawReserved, &rawSettled, &rawOverage, &requestCount); err != nil {
		f.t.Fatalf("read optional window values: %v", err)
	}
	return quotaWindowValues{
		reserved:     mustDecimal(f.t, rawReserved),
		settled:      mustDecimal(f.t, rawSettled),
		overage:      mustDecimal(f.t, rawOverage),
		requestCount: requestCount,
	}
}

func (f *quotaFixture) reservationSettlement(reservationID int64) (ReservationStatus, decimal.Decimal, decimal.Decimal, decimal.Decimal) {
	f.t.Helper()
	var status string
	var rawSettledCost, rawSettledUnits, rawOverageUnits string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT status, settled_cost::text, settled_units::text, overage_units::text
		 FROM quota_reservations
		 WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, reservationID,
	).Scan(&status, &rawSettledCost, &rawSettledUnits, &rawOverageUnits); err != nil {
		f.t.Fatalf("read reservation settlement: %v", err)
	}
	return ReservationStatus(status), mustDecimal(f.t, rawSettledCost), mustDecimal(f.t, rawSettledUnits), mustDecimal(f.t, rawOverageUnits)
}

func (f *quotaFixture) auditSemanticCount(operation string) int64 {
	f.t.Helper()
	var count int64
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*)
		 FROM quota_audit_events
		 WHERE tenant_id=$1
		   AND (event_type=$2 OR payload ->> 'operation' = $2)`,
		f.tenantID, operation,
	).Scan(&count); err != nil {
		f.t.Fatalf("count audit operation %s: %v", operation, err)
	}
	return count
}

func (f *quotaFixture) finalizationAuditAmounts(operation string, metric Metric) (decimal.Decimal, decimal.Decimal) {
	f.t.Helper()
	var rawReserved, rawSettled string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT amount_reserved::text, amount_settled::text
		 FROM quota_audit_events
		 WHERE tenant_id=$1
		   AND metric=$2
		   AND payload ->> 'operation' = $3
		 ORDER BY id DESC
		 LIMIT 1`,
		f.tenantID, string(metric), operation,
	).Scan(&rawReserved, &rawSettled); err != nil {
		f.t.Fatalf("read finalization audit amounts %s/%s: %v", operation, metric, err)
	}
	return mustDecimal(f.t, rawReserved), mustDecimal(f.t, rawSettled)
}

func (f *quotaFixture) reconciliationJobCount(kind string) int64 {
	f.t.Helper()
	var count int64
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*)
		 FROM quota_reconciliation_jobs
		 WHERE tenant_id=$1
		   AND status='queued'
		   AND (job_kind=$2 OR COALESCE(last_error, '') LIKE $2 || ':%')`,
		f.tenantID, kind,
	).Scan(&count); err != nil {
		f.t.Fatalf("count reconciliation job %s: %v", kind, err)
	}
	return count
}

func (f *quotaFixture) installAuditFailureTrigger(message string) {
	f.t.Helper()
	name := f.testSQLIdentifier("quota_test_fail_audit")
	f.installFailureTrigger("quota_audit_events", name, message, "COALESCE(NEW.payload ->> 'operation', '') <> ''")
}

func (f *quotaFixture) installReconciliationFailureTrigger(message string) {
	f.t.Helper()
	name := f.testSQLIdentifier("quota_test_fail_reconciliation")
	f.installFailureTrigger("quota_reconciliation_jobs", name, message, "COALESCE(NEW.last_error, '') LIKE 'quota_%_failed:%'")
}

func (f *quotaFixture) installFailureTrigger(table string, name string, message string, condition string) {
	f.t.Helper()
	functionName := name + "_fn"
	quotedFunction := pgQuoteIdentifier(functionName)
	quotedTrigger := pgQuoteIdentifier(name + "_trg")
	quotedTable := pgQuoteIdentifier(table)
	escapedMessage := strings.ReplaceAll(message, "'", "''")
	functionSQL := fmt.Sprintf(`
CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.tenant_id = %d AND (%s) THEN
		RAISE EXCEPTION '%s';
	END IF;
	RETURN NEW;
END
$$;`,
		quotedFunction, f.tenantID, condition, escapedMessage,
	)
	if _, err := f.pool.Exec(f.ctx, functionSQL); err != nil {
		f.t.Fatalf("install trigger function %s: %v", name, err)
	}
	triggerSQL := fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON %s
FOR EACH ROW EXECUTE FUNCTION %s();`, quotedTrigger, quotedTable, quotedFunction)
	if _, err := f.pool.Exec(f.ctx, triggerSQL); err != nil {
		f.t.Fatalf("install trigger %s: %v", name, err)
	}
	f.t.Cleanup(func() {
		ctx := context.Background()
		_, _ = f.pool.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, quotedTrigger, quotedTable))
		_, _ = f.pool.Exec(ctx, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, quotedFunction))
	})
}

func (f *quotaFixture) testSQLIdentifier(prefix string) string {
	f.t.Helper()
	return prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
}

func pgQuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func mustDecimal(t *testing.T, raw string) decimal.Decimal {
	t.Helper()
	value, err := decimal.NewFromString(raw)
	if err != nil {
		t.Fatalf("parse decimal %q: %v", raw, err)
	}
	return value
}
