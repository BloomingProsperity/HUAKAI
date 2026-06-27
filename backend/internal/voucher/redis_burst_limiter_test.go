package voucher

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeBurstStore 是 burstCounterStore 的内存替身,可模拟后端故障(failCount)。
type fakeBurstStore struct {
	counts    map[string]int64
	failCount bool
}

func (f *fakeBurstStore) Count(_ context.Context, key string) (int64, error) {
	if f.failCount {
		return 0, errors.New("burst store down")
	}
	return f.counts[key], nil
}

func (f *fakeBurstStore) IncrementWithTTL(_ context.Context, key string, _ time.Duration) (int64, error) {
	f.counts[key]++
	return f.counts[key], nil
}

// TestRedisBurstLimiter_FailuresOnlyCheckNoIncrementAndFailOpen 钉死 RedisBurstLimiter 三点:
//   - Check 只读不写计数(纯 Check 多次仍放行、后端计数仍为 0);
//   - 达 Limit 次失败后 Check 拒;
//   - 后端故障时 fail-open 放行(绝不误伤合法兑换)。
//
// 判别(§14):若 Check 改成 >Limit 才拒 → "2 次失败后应被限"转红;若 fail-open 改成报错即拒 → 末段转红;
// 若 Check 误调 IncrementWithTTL → "Check 后计数应为 0"转红。
func TestRedisBurstLimiter_FailuresOnlyCheckNoIncrementAndFailOpen(t *testing.T) {
	ctx := context.Background()
	st := &fakeBurstStore{counts: map[string]int64{}}
	l := NewRedisBurstLimiter(st, BurstPolicy{Limit: 2, Window: time.Minute})
	a := BurstAttempt{TenantID: 1, UserID: 5, SourceIPHash: "h"}

	for i := 0; i < 5; i++ {
		if d, _ := l.CheckVoucherBurst(ctx, a); !d.Allowed {
			t.Fatalf("纯 Check 不应增计数被限,第 %d 次被拒", i)
		}
	}
	if st.counts[redisBurstKey(a)] != 0 {
		t.Fatalf("Check 不应写计数,实际 %d", st.counts[redisBurstKey(a)])
	}

	_ = l.RecordVoucherFailure(ctx, a) // 1
	_ = l.RecordVoucherFailure(ctx, a) // 2 → 达 Limit
	if d, _ := l.CheckVoucherBurst(ctx, a); d.Allowed {
		t.Fatalf("2 次失败(Limit=2)后应被限,实际放行")
	}

	// 后端故障 → fail-open 放行(即便计数已达上限,后端读不到时也不阻断)。
	st.failCount = true
	if d, _ := l.CheckVoucherBurst(ctx, a); !d.Allowed {
		t.Fatalf("后端故障应 fail-open 放行,实际被限")
	}
}
