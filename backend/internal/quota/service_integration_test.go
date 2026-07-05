//go:build integration_pg

package quota

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// ReserveCostCapCountsReservedPlusSettled 守住 cost 判定必须同时计算
// reserved_value + settled_value。Mutation: 只算 settled 时 5+4=9 会误放行。
func TestServiceReserve_CostCapCountsReservedPlusSettled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 10, 15, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeAPIKey, fmt.Sprint(f.apiKeyID), MetricCostUSD, WindowFixed, 3600, "10", ModeEnforce)
	f.seedWindow(policyID, now, "2", "5", 0)
	claimID := f.seedClaim("reserve-cost-cap")

	result, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "reserve-cost-cap-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("4"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	})
	if !IsDenied(err) {
		t.Fatalf("Reserve err=%v; want denied", err)
	}
	if result.Decision.Kind != DecisionDeny || result.Decision.Metric != MetricCostUSD {
		t.Fatalf("decision=%+v; want cost deny", result.Decision)
	}
	if got := f.reservationCount(claimID); got != 0 {
		t.Fatalf("reservation count=%d; want 0 on deny", got)
	}
	if got := f.auditCount("reserve_denied"); got != 1 {
		t.Fatalf("reserve_denied audit count=%d; want 1", got)
	}
	if got := f.latestAuditPayloadField("reserve_denied", "current"); got != "7" {
		t.Fatalf("reserve_denied payload current=%q; want reserved+settled current 7", got)
	}
	if got := f.latestAuditPayloadField("reserve_denied", "amount"); got != "4" {
		t.Fatalf("reserve_denied payload amount=%q; want predicted cost 4", got)
	}
	if got := f.latestAuditPayloadField("reserve_denied", "limit"); got != "10" {
		t.Fatalf("reserve_denied payload limit=%q; want cost cap 10", got)
	}
}

// ReserveObserveDoesNotDeny 守住 observe 超限只审计不阻断。
// Mutation: 把 observe 当 enforce 会返回 denied。
func TestServiceReserve_ObserveDoesNotDeny(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 11, 0, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricCostUSD, WindowFixed, 3600, "10", ModeObserve)
	f.seedWindow(policyID, now, "9", "0", 0)
	claimID := f.seedClaim("reserve-observe")

	result, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "reserve-observe-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("4"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	})
	if err != nil {
		t.Fatalf("Reserve observe: %v", err)
	}
	if result.Decision.Kind != DecisionAllow || result.Reservation.ID == 0 {
		t.Fatalf("result=%+v; want allow with reservation", result)
	}
	if got := f.auditCount("observe_exceeded"); got != 1 {
		t.Fatalf("observe_exceeded audit count=%d; want 1", got)
	}
	if got := f.auditCount("reserve_allowed"); got != 1 {
		t.Fatalf("reserve_allowed audit count=%d; want 1", got)
	}
}

// ReserveNoPolicyAllows 守住「配额强制默认开」的安全前提:没有任何策略配置时,
// Reserve 必须放行(并按并发槽建预留),否则 default-on 会拦截未配置策略的部署。
// Mutation: 让无策略路径返回 deny / 不建预留 -> RED。
func TestServiceReserve_NoPolicyAllows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 6, 14, 9, 30, 0, 0, time.UTC)
	// 有意不为任何 scope 配置策略。
	claimID := f.seedClaim("reserve-no-policy")

	result, err := service.Reserve(ctx, ReserveRequest{
		TenantID:            f.tenantID,
		ClaimID:             claimID,
		RequestFingerprint:  "reserve-no-policy-" + uuid.NewString(),
		Scopes:              f.reserveScopes(),
		PredictedCost:       decimal.RequireFromString("999999"),
		ReservedTokens:      1_000_000,
		NeedConcurrencySlot: true,
		LeaseExpiresAt:      now.Add(5 * time.Minute),
		At:                  now,
	})
	if err != nil {
		t.Fatalf("Reserve no-policy: %v", err)
	}
	if result.Decision.Kind != DecisionAllow {
		t.Fatalf("decision=%+v; want allow when no policy configured", result.Decision)
	}
	if IsDenied(err) {
		t.Fatal("no-policy reserve must never be denied (default-on safety)")
	}
	if got := f.auditCount("reserve_denied"); got != 0 {
		t.Fatalf("reserve_denied audit count=%d; want 0 with no policy", got)
	}
}

// ReserveStrictestScopeWins 守住多 scope 同时生效时任一 enforce 超限即整体拒绝。
func TestServiceReserve_StrictestScopeWins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	userPolicy := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "1", ModeEnforce)
	apiKeyPolicy := f.seedPolicyWithMode(now, ScopeAPIKey, fmt.Sprint(f.apiKeyID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	f.seedWindow(userPolicy, now, "1", "0", 0)
	f.seedWindow(apiKeyPolicy, now, "0", "0", 0)
	claimID := f.seedClaim("reserve-strictest")

	result, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "reserve-strictest-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	})
	if !IsDenied(err) {
		t.Fatalf("Reserve err=%v; want denied", err)
	}
	if result.Decision.Scope.Kind != ScopeUser || result.Decision.Metric != MetricRequests {
		t.Fatalf("decision=%+v; want user requests deny", result.Decision)
	}
	if got := f.reservationCount(claimID); got != 0 {
		t.Fatalf("reservation count=%d; want 0 on strictest deny", got)
	}
}

// ReserveDeniedWritesAuditAndRetryAfter 守住窗口化 enforce 拒绝时必须写 retry_after。
func TestServiceReserve_DeniedWritesAuditAndRetryAfter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 13, 15, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "1", ModeEnforce)
	f.seedWindow(policyID, now, "1", "0", 0)
	claimID := f.seedClaim("reserve-retry-after")

	_, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "reserve-retry-after-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	})
	if !IsDenied(err) {
		t.Fatalf("Reserve err=%v; want denied", err)
	}
	if retryAfter := f.latestRetryAfter("reserve_denied"); retryAfter <= 0 {
		t.Fatalf("retry_after=%d; want >0", retryAfter)
	}
}

// RequestsCapUsesReservedAndSettledEvenWhenRequestCountIsZero 守住 requests
// 准入必须用 Model B: reserved_value + settled_value, request_count 只是镜像。
// Mutation: 若 assess 改读 request_count=0, 本应 2+1+1>3 的请求会误放行。
func TestServiceReserve_RequestsCapUsesReservedAndSettledEvenWhenRequestCountIsZero(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 13, 25, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "3", ModeEnforce)
	f.seedWindow(policyID, now, "2", "1", 0)
	claimID := f.seedClaim("reserve-requests-model-b-deny")

	result, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "reserve-requests-model-b-deny-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	})
	if !IsDenied(err) {
		t.Fatalf("Reserve err=%v result=%+v; want denied at reserved+settled+1 > limit", err, result)
	}
	if result.Decision.Code != decisionCodeLimitExceeded || result.Decision.Metric != MetricRequests {
		t.Fatalf("decision=%+v; want requests limit_exceeded", result.Decision)
	}
	if got := f.reservationCount(claimID); got != 0 {
		t.Fatalf("reservation count=%d; want 0 on Model B deny", got)
	}
	if got := f.latestAuditPayloadField("reserve_denied", "current"); got != "3" {
		t.Fatalf("reserve_denied current=%q; want reserved+settled current 3", got)
	}
	if got := f.latestAuditPayloadField("reserve_denied", "request_count"); got != "0" {
		t.Fatalf("reserve_denied request_count=%q; want mirror value 0", got)
	}
}

// RequestsCapAfterSettlement 守住 requests 预置用量来自 reserved_value,
// request_count 只作为观测镜像。Mutation: 若回读 request_count=0, 第二个请求会误放行。
func TestServiceReserve_RequestsCapAfterSettlement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 13, 30, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "2", ModeEnforce)
	f.seedWindow(policyID, now, "1", "0", 0)
	claimID := f.seedClaim("reserve-requests-second")

	result, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "reserve-requests-second-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	})
	if err != nil {
		t.Fatalf("second Reserve err=%v; want allow at reserved+settled 2/2", err)
	}
	if !result.Allowed || result.Reservation.ID == 0 {
		t.Fatalf("second result=%+v; want allowed reservation", result)
	}
	if got := f.windowReservedValue(policyID, now); !got.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("reserved_value after second reserve=%s; want 2", got)
	}
	if got := f.windowRequestCount(policyID, now); got != 1 {
		t.Fatalf("request_count after second reserve=%d; want mirror 1", got)
	}

	thirdClaimID := f.seedClaim("reserve-requests-third")
	third, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            thirdClaimID,
		RequestFingerprint: "reserve-requests-third-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	})
	if !IsDenied(err) {
		t.Fatalf("third Reserve err=%v result=%+v; want denied at reserved+settled 3/2", err, third)
	}
	if got := f.windowReservedValue(policyID, now); !got.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("reserved_value after denied third=%s; want still 2", got)
	}
	if got := f.windowRequestCount(policyID, now); got != 1 {
		t.Fatalf("request_count after denied third=%d; want still 1", got)
	}
}

// RequestsCapAtomicGuardPreventsConcurrentBypass 守住 requests reserve 必须用
// reserved_value + settled_value + 1 <= limit 的单条 UPDATE 原子守护增量。
// Mutation: 把 delta 置 0 或改成 request_count 判定, 并发下会超发。
func TestPostgresStore_RequestsCapAtomicGuardPreventsConcurrentBypass(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)

	now := time.Date(2026, 5, 28, 13, 45, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "3", ModeEnforce)
	windowID := f.seedWindow(policyID, now, "1", "0", 0)

	const workers = 8
	errs := make([]error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, errs[i] = store.IncrementWindowReserved(ctx, WindowReserve{
				TenantID:          f.tenantID,
				WindowID:          windowID,
				ReserveDelta:      decimal.NewFromInt(1),
				RequestCountDelta: 1,
				LimitValue:        decimal.NewFromInt(3),
			})
		}()
	}
	wg.Wait()

	var allowed int
	var denied int
	for i, err := range errs {
		switch {
		case err == nil:
			allowed++
		case errors.Is(err, pgx.ErrNoRows):
			denied++
		default:
			t.Fatalf("IncrementWindowReserved[%d] err=%v; want allow or atomic deny", i, err)
		}
	}
	if allowed != 2 || denied != workers-2 {
		t.Fatalf("allowed=%d denied=%d; want exact remaining capacity allowed=2 denied=%d", allowed, denied, workers-2)
	}
	if got := f.windowReservedValue(policyID, now); !got.Equal(decimal.NewFromInt(3)) {
		t.Fatalf("reserved_value after concurrent increments=%s; want capped at limit 3", got)
	}
	if got := f.windowRequestCount(policyID, now); got != 2 {
		t.Fatalf("request_count after concurrent increments=%d; want mirror of 2 allowed reserves", got)
	}
}

// ReserveIdempotentSameClaim 守住 (tenant_id, claim_id) 幂等, 重试不得双增窗口。
func TestServiceReserve_IdempotentSameClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 14, 0, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "10", ModeEnforce)
	claimID := f.seedClaim("reserve-idempotent")
	req := ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "reserve-idempotent-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	}

	first, err := service.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	second, err := service.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("second Reserve: %v", err)
	}
	if first.Reservation.ID == 0 || second.Reservation.ID != first.Reservation.ID {
		t.Fatalf("reservation ids first=%d second=%d; want same DB reservation", first.Reservation.ID, second.Reservation.ID)
	}
	if got := f.reservationCount(claimID); got != 1 {
		t.Fatalf("reservation count=%d; want 1", got)
	}
	if got := f.windowRequestCount(policyID, now); got != 1 {
		t.Fatalf("window request_count=%d; want 1 after duplicate Reserve", got)
	}
	if got := f.auditCount("reserve_allowed"); got != 1 {
		t.Fatalf("reserve_allowed audit count=%d; want only first Reserve to audit allow", got)
	}
}

// RetryAfterReservedIsIdempotent 守住 active reserved claim 的重试只复用同一
// reservation, 不重复占窗口。Mutation: 幂等分支重走 reserve 会把 request_count 加 2。
func TestServiceReserve_RetryAfterReservedIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 14, 15, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "10", ModeEnforce)
	claimID := f.seedClaim("reserve-idempotent-active")
	req := ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "reserve-idempotent-active-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	}

	first, err := service.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	second, err := service.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("second Reserve: %v", err)
	}
	if !second.IdempotencyHit || second.Reservation.ID != first.Reservation.ID {
		t.Fatalf("second=%+v; want idempotent reuse of reservation %d", second, first.Reservation.ID)
	}
	if got := f.windowRequestCount(policyID, now); got != 1 {
		t.Fatalf("request_count=%d; want 1 after active reserved retry", got)
	}
}

// RetryAfterReleasedRebuildsHoldWhenCapacityAllows 守住 released claim 重试时
// 必须重新评估并重建窗口/并发槽持有。Mutation: released 直接 deny 会变红。
func TestServiceReserve_RetryAfterReleasedRebuildsHoldWhenCapacityAllows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	service := NewService(store)

	now := time.Date(2026, 5, 28, 14, 45, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "3", ModeEnforce)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricConcurrency, WindowNone, 0, "1", ModeEnforce)
	claimID := f.seedClaim("reserve-retry-released")
	req := ReserveRequest{
		TenantID:            f.tenantID,
		ClaimID:             claimID,
		RequestFingerprint:  "reserve-retry-released-" + uuid.NewString(),
		Scopes:              f.reserveScopes(),
		PredictedCost:       decimal.RequireFromString("0.01"),
		NeedConcurrencySlot: true,
		LeaseExpiresAt:      now.Add(5 * time.Minute),
		At:                  now,
	}

	first, err := service.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	if err := store.ReleaseReservation(ctx, ReservationRelease{
		TenantID:      f.tenantID,
		ReservationID: first.Reservation.ID,
		ClaimID:       claimID,
		Reason:        "test-release",
	}); err != nil {
		t.Fatalf("release reservation: %v", err)
	}
	if err := store.ReleaseConcurrencySlots(ctx, f.tenantID, first.Reservation.ID, "test-release"); err != nil {
		t.Fatalf("release concurrency slots: %v", err)
	}
	if got := f.activeSlotCount(ScopeUser, fmt.Sprint(f.userID)); got != 0 {
		t.Fatalf("active slot count after release=%d; want 0", got)
	}

	retry, err := service.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("released retry: %v", err)
	}
	if !retry.Allowed || !retry.IdempotencyHit || retry.Reservation.ID != first.Reservation.ID {
		t.Fatalf("released retry result=%+v; want idempotent allow on same reservation", retry)
	}
	if got := f.reservationStatus(first.Reservation.ID); got != ReservationReserved {
		t.Fatalf("reservation status=%s; want reserved after reactivation", got)
	}
	if got := f.windowReservedValue(policyID, now); !got.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("reserved_value after released retry=%s; want rebuilt hold total 2", got)
	}
	if got := f.activeSlotCount(ScopeUser, fmt.Sprint(f.userID)); got != 1 {
		t.Fatalf("active slot count after retry=%d; want reacquired slot", got)
	}
	if got := f.auditCount("reserve_allowed"); got != 2 {
		t.Fatalf("reserve_allowed audit count=%d; want first + reactivation", got)
	}
}

// RetryAfterReleasedDeniesWhenCapacityGone 守住 released claim 重试必须重新评估容量,
// 不能无条件复活。Mutation: 不跑 evaluatePolicies 会误 allow。
func TestServiceReserve_RetryAfterReleasedDeniesWhenCapacityGone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	service := NewService(store)

	now := time.Date(2026, 5, 28, 14, 55, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "1", ModeEnforce)
	claimID := f.seedClaim("reserve-retry-released-deny")
	req := ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "reserve-retry-released-deny-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	}

	first, err := service.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	if err := store.ReleaseReservation(ctx, ReservationRelease{
		TenantID:      f.tenantID,
		ReservationID: first.Reservation.ID,
		ClaimID:       claimID,
		Reason:        "test-release",
	}); err != nil {
		t.Fatalf("release reservation: %v", err)
	}

	retry, err := service.Reserve(ctx, req)
	if !IsDenied(err) {
		t.Fatalf("released retry err=%v result=%+v; want capacity deny", err, retry)
	}
	if retry.Decision.Code != decisionCodeLimitExceeded || retry.Decision.Metric != MetricRequests {
		t.Fatalf("decision=%+v; want requests limit_exceeded", retry.Decision)
	}
	if got := f.reservationStatus(first.Reservation.ID); got != ReservationReleased {
		t.Fatalf("reservation status=%s; want still released after deny", got)
	}
	if got := f.windowReservedValue(policyID, now); !got.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("reserved_value after denied released retry=%s; want unchanged 1", got)
	}
	if got := f.auditCount("reserve_denied"); got != 1 {
		t.Fatalf("reserve_denied audit count=%d; want capacity denial audit", got)
	}
}

// ConcurrentSameClaimReserveIdempotent 守住同一 claim 并发 reserve 的输者必须
// 重读赢家, 不把唯一键冲突冒成 fail_closed, 也不能多次增加窗口。
func TestServiceReserve_ConcurrentSameClaimReserveIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 15, 0, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	claimID := f.seedClaim("reserve-concurrent-idempotent")
	req := ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "reserve-concurrent-idempotent-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	}

	const workers = 12
	results := make([]ReserveResult, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i], errs[i] = service.Reserve(ctx, req)
		}()
	}
	wg.Wait()

	var reservationID int64
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Reserve[%d] err=%v result=%+v; want idempotent allow", i, err, results[i])
		}
		if !results[i].Allowed || results[i].Reservation.ID == 0 {
			t.Fatalf("Reserve[%d] result=%+v; want allowed reservation", i, results[i])
		}
		if reservationID == 0 {
			reservationID = results[i].Reservation.ID
		}
		if results[i].Reservation.ID != reservationID {
			t.Fatalf("Reserve[%d] reservation=%d; want single reservation %d", i, results[i].Reservation.ID, reservationID)
		}
	}
	if got := f.reservationCount(claimID); got != 1 {
		t.Fatalf("reservation count=%d; want 1", got)
	}
	if got := f.windowRequestCount(policyID, now); got != 1 {
		t.Fatalf("request_count=%d; want one increment for concurrent same claim", got)
	}
}

// ConcurrentDifferentClaimsRetrySerializableConflict 守住不同 claim 并发争同一
// quota window 时, serializable 40001/40P01 必须重跑整笔 reserve, 不能误当同
// claim 唯一键竞争去重读赢家。Mutation: 把 40001 路由到 claim reread 会让输者
// 查不到自己的 reservation, 返回 quota_fail_closed, allowed 数少于 2。
func TestServiceReserve_ConcurrentDifferentClaimsRetrySerializableConflict(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 15, 10, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "2", ModeEnforce)
	claimIDs := [2]int64{
		f.seedClaim("reserve-concurrent-different-a"),
		f.seedClaim("reserve-concurrent-different-b"),
	}
	blocker, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatalf("begin policy blocker tx: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx,
		`SELECT 1 FROM quota_policies WHERE tenant_id=$1 AND id=$2 FOR UPDATE`,
		f.tenantID, policyID,
	); err != nil {
		t.Fatalf("lock policy before concurrent reserve: %v", err)
	}
	reqs := [2]ReserveRequest{
		{
			TenantID:           f.tenantID,
			ClaimID:            claimIDs[0],
			RequestFingerprint: "reserve-concurrent-different-a-" + uuid.NewString(),
			Scopes:             f.reserveScopes(),
			PredictedCost:      decimal.RequireFromString("0.01"),
			LeaseExpiresAt:     now.Add(5 * time.Minute),
			At:                 now,
		},
		{
			TenantID:           f.tenantID,
			ClaimID:            claimIDs[1],
			RequestFingerprint: "reserve-concurrent-different-b-" + uuid.NewString(),
			Scopes:             f.reserveScopes(),
			PredictedCost:      decimal.RequireFromString("0.01"),
			LeaseExpiresAt:     now.Add(5 * time.Minute),
			At:                 now,
		},
	}

	start := make(chan struct{})
	results := make([]ReserveResult, len(reqs))
	errs := make([]error, len(reqs))
	var wg sync.WaitGroup
	wg.Add(len(reqs))
	for i := range reqs {
		i := i
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = service.Reserve(ctx, reqs[i])
		}()
	}
	close(start)
	// 先用独立事务持有 policy row, 让两个 reserve 事务都在同一旧快照下排队;
	// 释放后一个事务写入窗口, 另一个会在同窗口 upsert/update 上触发 40001。
	time.Sleep(150 * time.Millisecond)
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release policy blocker tx: %v", err)
	}
	wg.Wait()

	seenReservations := map[int64]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Reserve[%d] err=%v result=%+v; want allow after bounded serializable retry", i, err, results[i])
		}
		if !results[i].Allowed || results[i].Decision.Code == decisionCodeFailClosed || results[i].Reservation.ID == 0 {
			t.Fatalf("Reserve[%d] result=%+v; want allowed non-fail-closed reservation", i, results[i])
		}
		if seenReservations[results[i].Reservation.ID] {
			t.Fatalf("Reserve[%d] reused reservation id %d; want distinct reservations for distinct claims", i, results[i].Reservation.ID)
		}
		seenReservations[results[i].Reservation.ID] = true
		if got := f.reservationCount(claimIDs[i]); got != 1 {
			t.Fatalf("claim[%d] reservation count=%d; want 1", i, got)
		}
	}
	if got := f.windowRequestCount(policyID, now); got != 2 {
		t.Fatalf("request_count=%d; want both different claims counted", got)
	}
	if got := f.auditCount("reserve_allowed"); got != 2 {
		t.Fatalf("reserve_allowed audit count=%d; want both different claims audited", got)
	}
	if got := f.auditDecisionCount(decisionCodeFailClosed); got != 0 {
		t.Fatalf("quota_fail_closed audit count=%d; want 0", got)
	}
}

// ReserveEnforcePassPersistsLedgerWindowAndAudit 守住通过路径必须同时落
// reservation、窗口 request_count 和 reserve_allowed 审计。
func TestServiceReserve_EnforcePassPersistsLedgerWindowAndAudit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))

	now := time.Date(2026, 5, 28, 14, 30, 0, 0, time.UTC)
	policyID := f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricRequests, WindowFixed, 3600, "10", ModeEnforce)
	claimID := f.seedClaim("reserve-allow")

	result, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            claimID,
		RequestFingerprint: "reserve-allow-" + uuid.NewString(),
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	})
	if err != nil {
		t.Fatalf("Reserve allow: %v", err)
	}
	if !result.Allowed || result.Decision.Kind != DecisionAllow || result.Reservation.ID == 0 {
		t.Fatalf("result=%+v; want allowed reservation", result)
	}
	if got := f.reservationCount(claimID); got != 1 {
		t.Fatalf("reservation count=%d; want 1", got)
	}
	if got := f.windowRequestCount(policyID, now); got != 1 {
		t.Fatalf("window request_count=%d; want 1", got)
	}
	if got := f.auditCount("reserve_allowed"); got != 1 {
		t.Fatalf("reserve_allowed audit count=%d; want 1", got)
	}
}

// ConcurrencyDenyPayloadDoesNotInventZeroCounters 守住并发 scope 饱和时,
// payload 不写 current=0/request_count=0 这种误导运维的假计数。
func TestServiceReserve_ConcurrencyDenyPayloadDoesNotInventZeroCounters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	service := NewService(NewPostgresStore(pool))
	store := NewPostgresStore(pool)

	now := time.Date(2026, 5, 28, 15, 30, 0, 0, time.UTC)
	f.seedPolicyWithMode(now, ScopeUser, fmt.Sprint(f.userID), MetricConcurrency, WindowNone, 0, "1", ModeEnforce)
	scope := Scope{TenantID: f.tenantID, Kind: ScopeUser, ID: fmt.Sprint(f.userID)}
	incumbentClaimID := f.seedClaim("concurrency-incumbent")
	incumbentReservationID := f.seedReservation(incumbentClaimID, "concurrency-incumbent")
	slot, err := store.AcquireConcurrencySlot(ctx, ConcurrencyAcquire{
		TenantID:       f.tenantID,
		ReservationID:  incumbentReservationID,
		ClaimID:        incumbentClaimID,
		Scope:          scope,
		SlotLimit:      1,
		At:             now,
		LeaseExpiresAt: now.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("seed concurrency slot: %v", err)
	}
	if slot.ID == 0 {
		t.Fatalf("seed concurrency slot returned empty slot")
	}

	claimID := f.seedClaim("reserve-concurrency-deny")
	result, err := service.Reserve(ctx, ReserveRequest{
		TenantID:            f.tenantID,
		ClaimID:             claimID,
		RequestFingerprint:  "reserve-concurrency-deny-" + uuid.NewString(),
		Scopes:              f.reserveScopes(),
		PredictedCost:       decimal.RequireFromString("0.01"),
		NeedConcurrencySlot: true,
		LeaseExpiresAt:      now.Add(5 * time.Minute),
		At:                  now,
	})
	if !IsDenied(err) {
		t.Fatalf("Reserve err=%v result=%+v; want concurrency denied", err, result)
	}
	if result.Decision.Kind != DecisionDeny || result.Decision.Metric != MetricConcurrency {
		t.Fatalf("decision=%+v; want concurrency deny", result.Decision)
	}
	if got := f.reservationCount(claimID); got != 0 {
		t.Fatalf("reservation count=%d; want 0 after rolled-back concurrency deny", got)
	}
	if got := f.latestAuditPayloadField("reserve_denied", "reason"); got != "concurrency_cap_saturated" {
		t.Fatalf("reserve_denied payload reason=%q; want concurrency_cap_saturated", got)
	}
	if got := f.latestAuditPayloadField("reserve_denied", "current"); got != "" {
		t.Fatalf("reserve_denied payload current=%q; want omitted because DB returned no count", got)
	}
	if got := f.latestAuditPayloadField("reserve_denied", "request_count"); got != "" {
		t.Fatalf("reserve_denied payload request_count=%q; want omitted for concurrency metric", got)
	}
}

// ReserveFailClosedOnStoreError 守住判定路径错误不能误 allow。
func TestServiceReserve_FailClosedOnStoreError(t *testing.T) {
	service := NewService(failingReserveStore{err: errors.New("policy backend unavailable")})
	result, err := service.Reserve(context.Background(), ReserveRequest{
		TenantID:           1,
		ClaimID:            10,
		RequestFingerprint: "fp-fail-closed",
		Scopes:             []Scope{{TenantID: 1, Kind: ScopeGlobal, ID: "*"}},
		PredictedCost:      decimal.RequireFromString("1"),
		LeaseExpiresAt:     time.Now().UTC().Add(time.Minute),
		At:                 time.Now().UTC(),
	})
	if !IsDenied(err) {
		t.Fatalf("Reserve err=%v; want denied fail-closed", err)
	}
	if result.Decision.Kind != DecisionDeny || result.Reservation.ID != 0 {
		t.Fatalf("result=%+v; want deny without reservation", result)
	}
}

func (f *quotaFixture) reserveScopes() []Scope {
	return []Scope{
		{TenantID: f.tenantID, Kind: ScopeGlobal, ID: "*"},
		{TenantID: f.tenantID, Kind: ScopeUser, ID: fmt.Sprint(f.userID)},
		{TenantID: f.tenantID, Kind: ScopeAPIKey, ID: fmt.Sprint(f.apiKeyID)},
	}
}

func (f *quotaFixture) seedPolicyWithMode(at time.Time, kind ScopeKind, id string, metric Metric, windowKind WindowKind, windowSeconds int64, limit string, mode Mode) int64 {
	f.t.Helper()
	var policyID int64
	// valid_from 绑定测试请求时刻，避免墙钟晚于硬编码 at 时策略被过滤。
	validFrom := at.UTC().Add(-time.Minute)
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO quota_policies (
			tenant_id, scope_kind, scope_id, metric, window_kind,
			window_seconds, limit_value, burst_value, mode, priority,
			enabled, valid_from
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7::numeric(20,8), 0, $8, 10,
			true, $9
		) RETURNING id`,
		f.tenantID, string(kind), id, string(metric), string(windowKind),
		windowSeconds, limit, string(mode), validFrom,
	).Scan(&policyID); err != nil {
		f.t.Fatalf("seed policy %s/%s/%s: %v", kind, id, metric, err)
	}
	return policyID
}

func (f *quotaFixture) seedWindow(policyID int64, at time.Time, reserved string, settled string, requestCount int64) int64 {
	f.t.Helper()
	window := f.policyWindow(policyID, at)
	var id int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO quota_windows (
			tenant_id, policy_id, window_start, window_end,
			reserved_value, settled_value, request_count
		) VALUES (
			$1, $2, $3, $4,
			$5::numeric(20,8), $6::numeric(20,8), $7
		) RETURNING id`,
		f.tenantID, policyID, window.Start, window.End, reserved, settled, requestCount,
	).Scan(&id); err != nil {
		f.t.Fatalf("seed window policy=%d: %v", policyID, err)
	}
	return id
}

func (f *quotaFixture) policyWindow(policyID int64, at time.Time) Window {
	f.t.Helper()
	var kind string
	var seconds int64
	var validFrom time.Time
	if err := f.pool.QueryRow(f.ctx,
		`SELECT window_kind, window_seconds, valid_from
		 FROM quota_policies
		 WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, policyID,
	).Scan(&kind, &seconds, &validFrom); err != nil {
		f.t.Fatalf("read policy window policy=%d: %v", policyID, err)
	}
	window, err := serviceWindowForPolicy(Policy{
		ID:        policyID,
		Window:    Window{Kind: WindowKind(kind), Seconds: seconds},
		ValidFrom: validFrom,
	}, at)
	if err != nil {
		f.t.Fatalf("compute policy window policy=%d: %v", policyID, err)
	}
	return window
}

func (f *quotaFixture) reservationCount(claimID int64) int64 {
	f.t.Helper()
	var count int64
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*) FROM quota_reservations WHERE tenant_id=$1 AND claim_id=$2`,
		f.tenantID, claimID,
	).Scan(&count); err != nil {
		f.t.Fatalf("count reservations: %v", err)
	}
	return count
}

func (f *quotaFixture) auditCount(eventType string) int64 {
	f.t.Helper()
	var count int64
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*) FROM quota_audit_events WHERE tenant_id=$1 AND event_type=$2`,
		f.tenantID, eventType,
	).Scan(&count); err != nil {
		f.t.Fatalf("count audit %s: %v", eventType, err)
	}
	return count
}

func (f *quotaFixture) auditDecisionCount(decisionCode string) int64 {
	f.t.Helper()
	var count int64
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*) FROM quota_audit_events WHERE tenant_id=$1 AND decision_code=$2`,
		f.tenantID, decisionCode,
	).Scan(&count); err != nil {
		f.t.Fatalf("count audit decision %s: %v", decisionCode, err)
	}
	return count
}

func (f *quotaFixture) latestRetryAfter(eventType string) int {
	f.t.Helper()
	var retryAfter int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COALESCE(retry_after_seconds, 0)
		 FROM quota_audit_events
		 WHERE tenant_id=$1 AND event_type=$2
		 ORDER BY id DESC
		 LIMIT 1`,
		f.tenantID, eventType,
	).Scan(&retryAfter); err != nil {
		f.t.Fatalf("read retry_after for %s: %v", eventType, err)
	}
	return retryAfter
}

func (f *quotaFixture) latestAuditPayloadField(eventType string, field string) string {
	f.t.Helper()
	var value string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COALESCE(payload ->> $3, '')
		 FROM quota_audit_events
		 WHERE tenant_id=$1 AND event_type=$2
		 ORDER BY id DESC
		 LIMIT 1`,
		f.tenantID, eventType, field,
	).Scan(&value); err != nil {
		f.t.Fatalf("read audit payload field %s/%s: %v", eventType, field, err)
	}
	return value
}

func (f *quotaFixture) windowRequestCount(policyID int64, at time.Time) int64 {
	f.t.Helper()
	window := f.policyWindow(policyID, at)
	var count int64
	if err := f.pool.QueryRow(f.ctx,
		`SELECT request_count FROM quota_windows
		 WHERE tenant_id=$1 AND policy_id=$2 AND window_start=$3`,
		f.tenantID, policyID, window.Start,
	).Scan(&count); err != nil {
		f.t.Fatalf("read window request count: %v", err)
	}
	return count
}

func (f *quotaFixture) windowReservedValue(policyID int64, at time.Time) decimal.Decimal {
	f.t.Helper()
	window := f.policyWindow(policyID, at)
	var raw string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT reserved_value::text FROM quota_windows
		 WHERE tenant_id=$1 AND policy_id=$2 AND window_start=$3`,
		f.tenantID, policyID, window.Start,
	).Scan(&raw); err != nil {
		f.t.Fatalf("read window reserved value: %v", err)
	}
	value, err := decimal.NewFromString(raw)
	if err != nil {
		f.t.Fatalf("parse reserved value %q: %v", raw, err)
	}
	return value
}

func (f *quotaFixture) markReservationReleased(reservationID int64, claimID int64) {
	f.t.Helper()
	tag, err := f.pool.Exec(f.ctx,
		`UPDATE quota_reservations
		 SET status='released', released_at=NOW(), release_reason='test-release', updated_at=NOW()
		 WHERE tenant_id=$1 AND id=$2 AND claim_id=$3`,
		f.tenantID, reservationID, claimID,
	)
	if err != nil {
		f.t.Fatalf("mark reservation released: %v", err)
	}
	if tag.RowsAffected() != 1 {
		f.t.Fatalf("mark reservation released affected=%d; want 1", tag.RowsAffected())
	}
}

func (f *quotaFixture) reservationStatus(reservationID int64) ReservationStatus {
	f.t.Helper()
	var status string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT status FROM quota_reservations WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, reservationID,
	).Scan(&status); err != nil {
		f.t.Fatalf("read reservation status: %v", err)
	}
	return ReservationStatus(status)
}

type failingReserveStore struct {
	err error
}

func (s failingReserveStore) WithTx(ctx context.Context, fn func(PGStore) error) error {
	return fn(s)
}

func (s failingReserveStore) ListActivePolicies(context.Context, PolicyFilter) ([]Policy, error) {
	return nil, s.err
}

func (s failingReserveStore) UpsertWindow(context.Context, WindowUpsert) (WindowCounter, error) {
	return WindowCounter{}, s.err
}

func (s failingReserveStore) GetWindowForUpdate(context.Context, int64, int64) (WindowCounter, error) {
	return WindowCounter{}, s.err
}

func (s failingReserveStore) IncrementWindowRequestCount(context.Context, WindowRequestCount) (WindowCounter, error) {
	return WindowCounter{}, s.err
}

func (s failingReserveStore) IncrementWindowReserved(context.Context, WindowReserve) (WindowCounter, error) {
	return WindowCounter{}, s.err
}

func (s failingReserveStore) ApplyWindowSettlement(context.Context, WindowSettlement) (WindowCounter, error) {
	return WindowCounter{}, s.err
}

func (s failingReserveStore) GetReservationByClaimForUpdate(context.Context, int64, int64) (Reservation, error) {
	return Reservation{}, pgx.ErrNoRows
}

func (s failingReserveStore) InsertReservation(context.Context, ReservationInsert) (Reservation, error) {
	return Reservation{}, s.err
}

func (s failingReserveStore) ReactivateReservation(context.Context, ReservationReactivate) (Reservation, error) {
	return Reservation{}, s.err
}

func (s failingReserveStore) SettleReservation(context.Context, Settlement) error {
	return s.err
}

func (s failingReserveStore) ReleaseReservation(context.Context, ReservationRelease) error {
	return s.err
}

func (s failingReserveStore) MarkReservationReconciliationNeeded(context.Context, int64, int64, int64) error {
	return s.err
}

func (s failingReserveStore) ListStaleReservedReservations(context.Context, time.Time, int) ([]StaleReservation, error) {
	return nil, s.err
}

func (s failingReserveStore) AcquireConcurrencySlot(context.Context, ConcurrencyAcquire) (ConcurrencySlot, error) {
	return ConcurrencySlot{}, s.err
}

func (s failingReserveStore) ReleaseConcurrencySlots(context.Context, int64, int64, string) error {
	return s.err
}

func (s failingReserveStore) ExpireConcurrencySlots(context.Context, int64, time.Time) error {
	return s.err
}

func (s failingReserveStore) InsertAuditEvent(context.Context, AuditEvent) (int64, error) {
	return 0, s.err
}

func (s failingReserveStore) EnqueueReconciliationJob(context.Context, ReconciliationEnqueue) (ReconciliationJob, error) {
	return ReconciliationJob{}, s.err
}

func (s failingReserveStore) ListDueReconciliationJobs(context.Context, int64, time.Time, int) ([]ReconciliationJob, error) {
	return nil, s.err
}

func (s failingReserveStore) ListTenantsWithDueReconciliationJobs(context.Context, time.Time, int) ([]int64, error) {
	return nil, s.err
}

func (s failingReserveStore) MarkReconciliationJobRunning(context.Context, int64, int64) error {
	return s.err
}

func (s failingReserveStore) CompleteReconciliationJob(context.Context, int64, int64) error {
	return s.err
}

func (s failingReserveStore) FailReconciliationJob(context.Context, ReconciliationFailure) error {
	return s.err
}
