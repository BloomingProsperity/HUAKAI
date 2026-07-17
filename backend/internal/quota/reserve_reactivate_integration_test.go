//go:build integration_pg

package quota

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// TestServiceReserve_ReactivateAcceptsUpdatedPredictedAndScopes 守住可复活预留的重放
// 身份口径与 billing 对齐:released 行携新 predicted(组价率 TTL 刷新)/新 pool_group
// scope(admin 改池绑定)同 fingerprint 重试,必须走 reactivate 放行,不得 429。
// Mutation: 把严格比对(predicted/scopes 相等)重新挡回 reactivate 之前 → 重试被
// reservation replay conflict 拒绝 → RED。
func TestServiceReserve_ReactivateAcceptsUpdatedPredictedAndScopes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 7, 5, 11, 0, 0, 0, time.UTC)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	claimID := f.seedClaim("reactivate-updated")
	fp := "fp-reactivate-" + uuid.NewString()
	scopesV1 := append(f.reserveScopes(), Scope{TenantID: f.tenantID, Kind: ScopePoolGroup, ID: "7"})

	first, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: fp,
		Scopes:             scopesV1,
		PredictedCost:      decimal.NewFromInt(4),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	})
	if err != nil || !first.Allowed {
		t.Fatalf("首次 Reserve err=%v result=%+v; want allowed", err, first)
	}
	f.setClaimTerminal(claimID, claimStatusAborted, "")
	if _, err := service.Release(ctx, ReleaseRequest{
		TenantID:      f.tenantID,
		ClaimID:       claimID,
		ReservationID: first.Reservation.ID,
		Reason:        "upstream_error",
		ReleasedAt:    now,
	}); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if attempt := f.reviveClaimForNewAttempt(claimID, now.Add(10*time.Minute)); attempt != 2 {
		t.Fatalf("复活 attempt_seq=%d; want 2", attempt)
	}

	// 同 fingerprint,新 predicted(4→6)+ 新 pool_group(7→9):模拟 admin 改价/改绑后重试。
	scopesV2 := append(f.reserveScopes(), Scope{TenantID: f.tenantID, Kind: ScopePoolGroup, ID: "9"})
	retry, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: fp,
		Scopes:             scopesV2,
		PredictedCost:      decimal.NewFromInt(6),
		LeaseExpiresAt:     now.Add(10 * time.Minute),
		At:                 now,
	})
	if err != nil {
		t.Fatalf("重试 Reserve err=%v; want reactivate allow(不得 replay conflict)", err)
	}
	if !retry.Allowed || retry.Reservation.ID != first.Reservation.ID {
		t.Fatalf("retry=%+v; want同一预留被 reactivate", retry)
	}
	if !retry.Reservation.PredictedCost.Equal(decimal.NewFromInt(6)) {
		t.Fatalf("reactivated predicted=%s; want 更新为 6", retry.Reservation.PredictedCost)
	}
	// 窗口按新值重扣:release 已把 4 归还,reactivate 应扣 6。
	values := f.windowValues(costPolicy, now)
	if !values.reserved.Equal(decimal.NewFromInt(6)) {
		t.Fatalf("cost window reserved=%s; want 6(新 predicted 重扣)", values.reserved)
	}
}

// TestServiceReserve_ReactivateStillRejectsFingerprintMismatch 守住放宽比对后仅存的
// 身份护栏:released 行携不同 fingerprint(不同请求内容)重用同 claim 必须仍然冲突拒绝。
// Mutation: 可复活分支把 fingerprint 校验也删掉 → 异内容重放被放行 → RED。
func TestServiceReserve_ReactivateStillRejectsFingerprintMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 7, 5, 11, 10, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	claimID := f.seedClaim("reactivate-fp-guard")

	first, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "fp-original-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.NewFromInt(4),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	})
	if err != nil || !first.Allowed {
		t.Fatalf("首次 Reserve err=%v result=%+v; want allowed", err, first)
	}
	f.setClaimTerminal(claimID, claimStatusAborted, "")
	if _, err := service.Release(ctx, ReleaseRequest{
		TenantID:      f.tenantID,
		ClaimID:       claimID,
		ReservationID: first.Reservation.ID,
		Reason:        "upstream_error",
		ReleasedAt:    now,
	}); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if attempt := f.reviveClaimForNewAttempt(claimID, now.Add(10*time.Minute)); attempt != 2 {
		t.Fatalf("复活 attempt_seq=%d; want 2", attempt)
	}

	retry, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "fp-mutated-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.NewFromInt(4),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	})
	if !errors.Is(err, ErrReservationReplayConflict) {
		t.Fatalf("异 fingerprint 重试 err=%v; want ErrReservationReplayConflict", err)
	}
	if retry.Allowed {
		t.Fatalf("retry=%+v; want 拒绝", retry)
	}
	status, _, _, _ := f.reservationSettlement(first.Reservation.ID)
	if status != ReservationReleased {
		t.Fatalf("reservation status=%s; want 保持 released 未被动", status)
	}
}
