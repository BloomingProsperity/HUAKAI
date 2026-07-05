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
