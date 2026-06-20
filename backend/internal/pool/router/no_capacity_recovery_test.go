package router

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestEarliestPoolRecovery 守"池内最早恢复时刻"估算:取所有账号中最早能重新可用的时刻。
// 变异:把 earliestPoolRecovery 的 min(acctRecover.Before(earliest))改成 max → 取到 +30s → 红。
func TestEarliestPoolRecovery(t *testing.T) {
	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	accounts := []*AccountSnapshot{
		{ID: 1, HealthStateUntil: base.Add(2 * time.Second)},
		{ID: 2, HealthStateUntil: base.Add(30 * time.Second)},
		{ID: 3, ModelRateLimits: map[string]ModelRateLimit{"m": {RateLimitResetAt: base.Add(10 * time.Second)}}},
	}
	if got := earliestPoolRecovery(accounts, "m", base); !got.Equal(base.Add(2 * time.Second)) {
		t.Fatalf("earliest=%v want +2s(应取池内最早恢复,非 +30s/+10s)", got)
	}
}

// TestEarliestPoolRecovery_AccountNeedsBothGatesClear 守"单账号同时被健康冷却+模型限流挡时,该账号
// 要两门都清除才可用 → 其恢复时刻取较晚者(max)"。双向 fixture(model 晚 / health 晚)锁死取较晚语义,
// 不偏向某一侧。变异:把账号内 max 改成 min → 两子例分别取到 +5s/+8s → 红。
func TestEarliestPoolRecovery_AccountNeedsBothGatesClear(t *testing.T) {
	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name         string
		healthUntil  time.Duration
		rateResetAt  time.Duration
		wantRecovery time.Duration
	}{
		{"model_later", 5 * time.Second, 20 * time.Second, 20 * time.Second},
		{"health_later", 8 * time.Second, 3 * time.Second, 8 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			accounts := []*AccountSnapshot{
				{ID: 1, HealthStateUntil: base.Add(tc.healthUntil), ModelRateLimits: map[string]ModelRateLimit{"m": {RateLimitResetAt: base.Add(tc.rateResetAt)}}},
			}
			if got := earliestPoolRecovery(accounts, "m", base); !got.Equal(base.Add(tc.wantRecovery)) {
				t.Fatalf("earliest=%v want +%v(账号需两门都清除取较晚者)", got, tc.wantRecovery)
			}
		})
	}
}

// TestEarliestPoolRecovery_AllPastReturnsZero 守"恢复时刻全在过去 → 无可估恢复 → 零值(调用方回退默认)"。
func TestEarliestPoolRecovery_AllPastReturnsZero(t *testing.T) {
	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	accounts := []*AccountSnapshot{
		{ID: 1, HealthStateUntil: base.Add(-2 * time.Second)},
		{ID: 2, ModelRateLimits: map[string]ModelRateLimit{"m": {RateLimitResetAt: base.Add(-10 * time.Second)}}},
	}
	if got := earliestPoolRecovery(accounts, "m", base); !got.IsZero() {
		t.Fatalf("earliest=%v want 零值(全在过去)", got)
	}
}

// TestSelectEmptyEligibleWrapsNoCapacityWithRecovery 端到端:全账号健康冷却 → Select 返回的错误既满足
// errors.Is(ErrAllChannelsDegraded)(分类语义不变),又可 errors.As 出 NoCapacityError 带恢复时刻。
// 变异:把空 eligible 分支改回裸 return ErrAllChannelsDegraded → errors.As 取不出 → 红。
func TestSelectEmptyEligibleWrapsNoCapacityWithRecovery(t *testing.T) {
	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	cooled := &AccountSnapshot{ID: 1, TenantID: 1, HealthState: "cooldown", HealthStateUntil: base.Add(7 * time.Second)}
	sel := NewDefaultSelector(&stubAccountSource{accounts: []*AccountSnapshot{cooled}}, WithNow(func() time.Time { return base }))

	_, err := sel.Select(context.Background(), SelectionRequest{TenantID: 1, RequestedModel: "m"})
	// 两个无容量哨兵(ErrAllChannelsDegraded / ErrNoEligibleAccount)都被同样包进 NoCapacityError;
	// 此处只断言"仍归类为无容量哨兵"(Unwrap 保 errors.Is 成立),不依赖具体是哪一个。
	if !errors.Is(err, ErrAllChannelsDegraded) && !errors.Is(err, ErrNoEligibleAccount) {
		t.Fatalf("err=%v 应仍满足无容量哨兵的 errors.Is(分类不变)", err)
	}
	var noCap *NoCapacityError
	if !errors.As(err, &noCap) {
		t.Fatalf("err=%v 应可 errors.As 出 NoCapacityError", err)
	}
	if !noCap.EarliestRecoveryAt.Equal(base.Add(7 * time.Second)) {
		t.Fatalf("EarliestRecoveryAt=%v want +7s", noCap.EarliestRecoveryAt)
	}
}
