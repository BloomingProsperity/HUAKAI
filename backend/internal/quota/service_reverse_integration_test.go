//go:build integration_pg

package quota

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestServiceReverseCost_DecrementsSettledByRefund 钉死 ③ 修复主路径:已结算 claim 的成本被
// ReverseCost 从它命中的成本窗 settled_value 负向冲减(对齐"退款只退钱包、配额计数也要同步退")。
// 判别(§14):若 reverseCostSettlementWindows 把 SettledAddValue 写成正向(不取 Neg)或不下发冲减,
// settled 不会从 8 降到 5,下面断言转红。
func TestServiceReverseCost_DecrementsSettledByRefund(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "rev-a", "8", false)
	if _, err := service.Settle(ctx, SettleRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		ActualCost:    decimal.RequireFromString("8"),
		SettledAt:     now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if v := f.windowValues(costPolicy, now); !v.settled.Equal(decimal.NewFromInt(8)) {
		t.Fatalf("结算后 settled=%s want 8", v.settled)
	}

	// 退款冲减 3:settled 8 → 5。
	res, err := service.ReverseCost(ctx, ReverseCostRequest{
		TenantID: f.tenantID,
		ClaimID:  reserve.Reservation.ClaimID,
		Amount:   decimal.RequireFromString("3"),
	})
	if err != nil {
		t.Fatalf("ReverseCost: %v", err)
	}
	if res.Skipped || !res.ReversedValue.Equal(decimal.NewFromInt(3)) {
		t.Fatalf("ReverseCost result=%+v want reversed=3 且不跳过", res)
	}
	if v := f.windowValues(costPolicy, now); !v.settled.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("冲减后 settled=%s want 5(8-3)", v.settled)
	}
}

// TestServiceReverseCost_ClampsAtZero 钉死钳制:冲减额超过当前 settled 时只冲到 0、绝不为负。
// 判别(§14):若去掉 dec>SettledValue 的钳制,负向写会把 settled 推到 -6,被 DB CHECK
// settled_value>=0 拒绝、ReverseCost 返回错误,下面"settled=0 且无错误"的断言转红。
func TestServiceReverseCost_ClampsAtZero(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 6, 27, 13, 0, 0, 0, time.UTC)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, "rev-clamp", "4", false)
	if _, err := service.Settle(ctx, SettleRequest{
		TenantID:      f.tenantID,
		ClaimID:       reserve.Reservation.ClaimID,
		ReservationID: reserve.Reservation.ID,
		ActualCost:    decimal.RequireFromString("4"),
		SettledAt:     now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	res, err := service.ReverseCost(ctx, ReverseCostRequest{
		TenantID: f.tenantID,
		ClaimID:  reserve.Reservation.ClaimID,
		Amount:   decimal.RequireFromString("10"), // 远超 settled=4
	})
	if err != nil {
		t.Fatalf("ReverseCost(超额): %v", err)
	}
	if !res.ReversedValue.Equal(decimal.NewFromInt(4)) {
		t.Fatalf("reversed=%s want 4(钳制到 settled)", res.ReversedValue)
	}
	if v := f.windowValues(costPolicy, now); !v.settled.Equal(decimal.Zero) {
		t.Fatalf("冲减后 settled=%s want 0(钳制,不为负)", v.settled)
	}
}

// TestServiceReverseCost_SkipsUnsettledAndMissing 钉死跳过分支:未结算预留 / 不存在的 claim →
// 返回 Skipped、不报错、不动账(避免对没有 settled_value 的预留误冲或把找不到当失败)。
func TestServiceReverseCost_SkipsUnsettledAndMissing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 6, 27, 14, 0, 0, 0, time.UTC)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)

	// (a) 已预留但未结算 → 跳过、settled 仍为 0。
	reserve := f.reserveForSettlement(ctx, service, now, "rev-unsettled", "5", false)
	res, err := service.ReverseCost(ctx, ReverseCostRequest{
		TenantID: f.tenantID, ClaimID: reserve.Reservation.ClaimID, Amount: decimal.RequireFromString("2"),
	})
	if err != nil {
		t.Fatalf("ReverseCost(未结算): %v", err)
	}
	if !res.Skipped {
		t.Fatalf("未结算预留应 Skipped,实际 %+v", res)
	}
	if v := f.windowValuesOrZero(costPolicy, now); !v.settled.Equal(decimal.Zero) {
		t.Fatalf("未结算冲减后 settled=%s want 0(不动账)", v.settled)
	}

	// (b) 不存在的 claim → 跳过、不报错。
	res2, err := service.ReverseCost(ctx, ReverseCostRequest{
		TenantID: f.tenantID, ClaimID: 999_000_111, Amount: decimal.RequireFromString("1"),
	})
	if err != nil {
		t.Fatalf("ReverseCost(不存在 claim)应无错误,实际 %v", err)
	}
	if !res2.Skipped {
		t.Fatalf("不存在 claim 应 Skipped,实际 %+v", res2)
	}
}
