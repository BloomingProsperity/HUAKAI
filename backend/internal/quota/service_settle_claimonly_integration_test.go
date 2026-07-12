//go:build integration_pg

package quota

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestServiceSettle_ClaimOnlyResolvesReservationID 守住结算写操作必须用解析出的
// reservation.ID,而非调用方传入的 req.ReservationID。生产 quotaenforce.Settler 结算时
// 只按 ClaimID 定位(ReservationID=0),若写操作直接用 req.ReservationID=0 则
// SettleReservation 的 WHERE id=0 命中 0 行、requireAffected 抛裸 no rows,quota 窗口
// 永不结算(reserved_value 永久累积,配了 policy 的租户很快被误拒)。
// Mutation: 6 个写点任一改回 req.ReservationID → Settle 报 no rows / 窗口不结算 → RED。
func TestServiceSettle_ClaimOnlyResolvesReservationID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "settle-claim-only", "4", true)

	// 模拟 quotaenforce:只传 ClaimID,ReservationID 留 0。
	result, err := service.Settle(ctx, SettleRequest{
		TenantID:   f.tenantID,
		ClaimID:    reserve.Reservation.ClaimID,
		ActualCost: decimal.NewFromInt(2),
		SettledAt:  now,
	})
	if err != nil {
		t.Fatalf("Settle(claim-only) err=%v; 写操作误用 req.ReservationID=0 会命中 0 行报 no rows", err)
	}
	if result.Reservation.Status != ReservationSettled {
		t.Fatalf("result status=%s; want settled", result.Reservation.Status)
	}
	status, settledCost, _, _ := f.reservationSettlement(reserve.Reservation.ID)
	if status != ReservationSettled || !settledCost.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("reservation status=%s settled_cost=%s; want settled with cost 2", status, settledCost)
	}
	values := f.windowValues(costPolicy, now)
	if !values.reserved.Equal(decimal.Zero) || !values.settled.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("cost window=%+v; want reserved=0 settled=2(reserved 已转 settled)", values)
	}
}

// TestServiceRelease_ClaimOnlyResolvesReservationID 守住 release 路径同款:只传 ClaimID
// 时必须用解析出的 reservation.ID 释放窗口预留与并发槽,否则 reserved_value 永不归还。
// Mutation: release 分支写点改回 req.ReservationID → Release 报 no rows / reserved 不归还 → RED。
func TestServiceRelease_ClaimOnlyResolvesReservationID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 7, 5, 12, 10, 0, 0, time.UTC)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "release-claim-only", "4", true)
	// 预留后窗口 reserved 应为 4。
	if v := f.windowValues(costPolicy, now); !v.reserved.Equal(decimal.NewFromInt(4)) {
		t.Fatalf("reserve 后 window reserved=%s; want 4", v.reserved)
	}

	result, err := service.Release(ctx, ReleaseRequest{
		TenantID:   f.tenantID,
		ClaimID:    reserve.Reservation.ClaimID,
		Reason:     "upstream_error",
		ReleasedAt: now,
	})
	if err != nil {
		t.Fatalf("Release(claim-only) err=%v; 写操作误用 req.ReservationID=0 会命中 0 行报 no rows", err)
	}
	if result.Reservation.Status != ReservationReleased {
		t.Fatalf("result status=%s; want released", result.Reservation.Status)
	}
	status, _, _, _ := f.reservationSettlement(reserve.Reservation.ID)
	if status != ReservationReleased {
		t.Fatalf("reservation status=%s; want released", status)
	}
	values := f.windowValues(costPolicy, now)
	if !values.reserved.Equal(decimal.Zero) || !values.settled.Equal(decimal.Zero) {
		t.Fatalf("cost window=%+v; want reserved=0 settled=0(预留已释放归还)", values)
	}
}

// TestServiceCommitCacheHit_ClaimOnlyResolvesReservationID 守住 cache-hit 路径同样必须
// 用解析出的 reservation.ID 做窗口结算、并发槽释放与 reservation 终态写入。
// Mutation: cache-hit 分支任一写点改回 req.ReservationID → CommitCacheHit 报 no rows
// 或窗口/槽/审计字段不落地 → RED。
func TestServiceCommitCacheHit_ClaimOnlyResolvesReservationID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 7, 5, 12, 20, 0, 0, time.UTC)
	requestPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricConcurrency, WindowNone, 0, "1", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "cache-hit-claim-only", "4", true)
	if got := f.activeSlotCount(ScopeUser, fmt.Sprint(f.userID)); got != 1 {
		t.Fatalf("cache-hit 前 active slots=%d; want 1", got)
	}

	const cacheKey = "claim-only-cache-key"
	const cacheSource = "response_cache"
	result, err := service.CommitCacheHit(ctx, CacheHitRequest{
		TenantID:    f.tenantID,
		ClaimID:     reserve.Reservation.ClaimID,
		CommittedAt: now,
		CacheKey:    cacheKey,
		CacheSource: cacheSource,
	})
	if err != nil {
		t.Fatalf("CommitCacheHit(claim-only) err=%v; 写操作误用 req.ReservationID=0 会命中 0 行报 no rows", err)
	}
	if result.Reservation.Status != ReservationSettled {
		t.Fatalf("result status=%s; want settled cache hit", result.Reservation.Status)
	}
	status, settledCost, settledUnits, overageUnits := f.reservationSettlement(reserve.Reservation.ID)
	if status != ReservationSettled || !settledCost.Equal(decimal.Zero) || !settledUnits.Equal(decimal.NewFromInt(1)) || !overageUnits.Equal(decimal.Zero) {
		t.Fatalf("reservation status=%s settled_cost=%s settled_units=%s overage_units=%s; want settled zero-cost cache hit", status, settledCost, settledUnits, overageUnits)
	}
	requestValues := f.windowValues(requestPolicy, now)
	if !requestValues.reserved.Equal(decimal.Zero) || !requestValues.settled.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("request window=%+v; want reserved=0 settled=1", requestValues)
	}
	costValues := f.windowValues(costPolicy, now)
	if !costValues.reserved.Equal(decimal.Zero) || !costValues.settled.Equal(decimal.Zero) || !costValues.overage.Equal(decimal.Zero) {
		t.Fatalf("cost window=%+v; want reserved=0 settled=0 overage=0", costValues)
	}
	if got := f.activeSlotCount(ScopeUser, fmt.Sprint(f.userID)); got != 0 {
		t.Fatalf("cache-hit 后 active slots=%d; want 0", got)
	}
	auditReserved, auditSettled := f.finalizationAuditAmounts("cache_hit", MetricRequests)
	if !auditReserved.Equal(decimal.NewFromInt(1)) || !auditSettled.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("cache_hit requests audit reserved=%s settled=%s; want request units 1/1", auditReserved, auditSettled)
	}
	if got := f.finalizationAuditPayloadField("cache_hit", "cache_key"); got != cacheKey {
		t.Fatalf("cache_hit payload cache_key=%q; want %q", got, cacheKey)
	}
	if got := f.finalizationAuditPayloadField("cache_hit", "cache_source"); got != cacheSource {
		t.Fatalf("cache_hit payload cache_source=%q; want %q", got, cacheSource)
	}
}

func (f *quotaFixture) finalizationAuditPayloadField(operation string, field string) string {
	f.t.Helper()
	var value string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COALESCE(payload ->> $3, '')
		 FROM quota_audit_events
		 WHERE tenant_id=$1
		   AND payload ->> 'operation' = $2
		 ORDER BY id DESC
		 LIMIT 1`,
		f.tenantID, operation, field,
	).Scan(&value); err != nil {
		f.t.Fatalf("read finalization audit payload field %s/%s: %v", operation, field, err)
	}
	return value
}
