package billing

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestAbortRetryPolicy_ATCD3001 固定 Abort 的独立九次总尝试预算，同时证明
// 业务错误与调用取消不会被扩大重试。
func TestAbortRetryPolicy_ATCD3001(t *testing.T) {
	if abortTx2RetryPolicy.attempts != 9 || abortTx2RetryPolicy.backoffBase != 2*time.Millisecond || abortTx2RetryPolicy.backoffCap != 50*time.Millisecond {
		t.Fatalf("Abort policy=%+v，want attempts/base/cap=9/2ms/50ms", abortTx2RetryPolicy)
	}
	if settleTx2RetryPolicy.attempts != 6 || settleTx2RetryPolicy.backoffBase != 2*time.Millisecond || settleTx2RetryPolicy.backoffCap != 50*time.Millisecond {
		t.Fatalf("Settle policy=%+v，want unchanged 6/2ms/50ms", settleTx2RetryPolicy)
	}
	t.Run("八次冲突后第九次成功", func(t *testing.T) {
		calls, sleeps := 0, 0
		metricBefore := billingAbortMetricValueWithAttempts("40P01", "retry_success", 9)
		err := retryTx2(context.Background(), "abort", abortTx2RetryPolicy, func(context.Context) error {
			calls++
			if calls <= 8 {
				if calls%2 == 0 {
					return fakePgErr("40P01")
				}
				return fakePgErr("40001")
			}
			return nil
		}, countingSleep(&sleeps), zeroRand)
		if err != nil {
			t.Fatalf("第九次应成功，得 err=%v", err)
		}
		if calls != 9 || sleeps != 8 {
			t.Fatalf("Tx2 次数/退避次数=%d/%d，want 9/8", calls, sleeps)
		}
		if got := billingAbortMetricValueWithAttempts("40P01", "retry_success", 9) - metricBefore; got != 1 {
			t.Fatalf("retry_success attempts=9 metric delta=%d，want 1", got)
		}
	})

	t.Run("业务错误只执行一次", func(t *testing.T) {
		businessErr := errors.New("business abort failure")
		calls := 0
		err := retryTx2(context.Background(), "abort", abortTx2RetryPolicy, func(context.Context) error {
			calls++
			return businessErr
		}, countingSleep(new(int)), zeroRand)
		if !errors.Is(err, businessErr) || calls != 1 {
			t.Fatalf("err/calls=%v/%d，want 原业务错误/1", err, calls)
		}
	})

	t.Run("上下文取消立即停止", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls, sleeps := 0, 0
		err := retryTx2(ctx, "abort", abortTx2RetryPolicy, func(context.Context) error {
			calls++
			cancel()
			return fakePgErr("40001")
		}, countingSleep(&sleeps), zeroRand)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v，want context.Canceled", err)
		}
		if calls != 1 || sleeps != 0 {
			t.Fatalf("Tx2 次数/退避次数=%d/%d，want 1/0", calls, sleeps)
		}
	})
}
