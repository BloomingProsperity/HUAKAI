package gatewayhttp

import (
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

// TestPoolNoCapacityRetryAfter 守"用池最早恢复时刻算精确 Retry-After,无可估则回退默认"。
// 变异:删 poolNoCapacityRetryAfter 的 errors.As 提取(恒返回 回退)→ 前三条精确用例红。
func TestPoolNoCapacityRetryAfter(t *testing.T) {
	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"recovery_in_2s", &pool.NoCapacityError{Cause: pool.ErrAllChannelsDegraded, EarliestRecoveryAt: base.Add(2 * time.Second)}, 2},
		{"recovery_in_30s", &pool.NoCapacityError{Cause: pool.ErrNoEligibleAccount, EarliestRecoveryAt: base.Add(30 * time.Second)}, 30},
		{"recovery_ceil_1500ms", &pool.NoCapacityError{Cause: pool.ErrAllChannelsDegraded, EarliestRecoveryAt: base.Add(1500 * time.Millisecond)}, 2},
		{"zero_recovery_fallback", &pool.NoCapacityError{Cause: pool.ErrAllChannelsDegraded}, noCapacityFallbackRetryAfter},
		{"past_recovery_fallback", &pool.NoCapacityError{Cause: pool.ErrAllChannelsDegraded, EarliestRecoveryAt: base.Add(-3 * time.Second)}, noCapacityFallbackRetryAfter},
		{"plain_error_fallback", errors.New("not a no-capacity error"), noCapacityFallbackRetryAfter},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := poolNoCapacityRetryAfter(tc.err, base); got != tc.want {
				t.Fatalf("poolNoCapacityRetryAfter=%d want %d", got, tc.want)
			}
		})
	}
}
