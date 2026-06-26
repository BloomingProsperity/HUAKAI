package gateway

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func bucketTime() time.Time { return time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC) }

func TestTokenBucket_NewIsFull(t *testing.T) {
	b := NewTokenBucket(10, 5)
	tokens, _ := b.Snapshot()
	if tokens != 5 {
		t.Fatalf("new bucket tokens=%.1f; want 5", tokens)
	}
}

func TestTokenBucket_TryAcquireConsumes(t *testing.T) {
	b := NewTokenBucket(10, 5)
	now := bucketTime()
	if !b.TryAcquire(now) {
		t.Fatal("acquire 1 should succeed on full bucket")
	}
	tokens, _ := b.Snapshot()
	if tokens != 4 {
		t.Fatalf("after acquire tokens=%.1f; want 4", tokens)
	}
}

func TestTokenBucket_OverBurstFails(t *testing.T) {
	b := NewTokenBucket(10, 5)
	now := bucketTime()
	if b.TryAcquireN(now, 6) {
		t.Fatal("acquire 6 from burst-5 should fail")
	}
	tokens, _ := b.Snapshot()
	if tokens != 5 {
		t.Fatalf("failed acquire must not consume; tokens=%.1f", tokens)
	}
}

func TestTokenBucket_RefillAfter1Sec(t *testing.T) {
	b := NewTokenBucket(2, 5)
	now := bucketTime()
	for i := 0; i < 5; i++ {
		_ = b.TryAcquire(now)
	}
	tokens, _ := b.Snapshot()
	if tokens != 0 {
		t.Fatalf("after draining tokens=%.1f", tokens)
	}
	// 过去 1s → 补充 2 个 token(Rate=2)
	if !b.TryAcquireN(now.Add(time.Second), 2) {
		t.Fatal("after 1s refill must give 2 tokens")
	}
}

func TestTokenBucket_RefillCapsAtBurst(t *testing.T) {
	b := NewTokenBucket(100, 5)
	now := bucketTime()
	// 排空
	_ = b.TryAcquireN(now, 5)
	// 等足够久, 使补充量会超过 burst
	t1 := now.Add(10 * time.Second)
	tokens, _ := b.Snapshot()
	_ = tokens
	// 通过 Snapshot 触发补充其实不会补充; 用 TryAcquire(0) 这个小技巧
	_ = b.TryAcquireN(t1, 0)
	tokens, _ = b.Snapshot()
	if tokens > 5 {
		t.Fatalf("refill exceeded burst: tokens=%.1f", tokens)
	}
}

func TestTokenBucket_NextAvailableAt_Empty(t *testing.T) {
	b := NewTokenBucket(2, 1)
	now := bucketTime()
	_ = b.TryAcquire(now)
	next := b.NextAvailableAt(now)
	want := now.Add(500 * time.Millisecond) // 1 / 2 = 0.5 秒
	delta := next.Sub(want)
	if delta < -time.Millisecond || delta > time.Millisecond {
		t.Fatalf("NextAvailableAt = %v; want ~%v", next, want)
	}
}

func TestTokenBucket_NextAvailableAt_Available(t *testing.T) {
	b := NewTokenBucket(2, 5)
	now := bucketTime()
	if got := b.NextAvailableAt(now); got != now {
		t.Fatalf("NextAvailableAt with full bucket should be now; got %v", got)
	}
}

func TestTokenBucket_NextAvailableAt_NeverWhenRateZero(t *testing.T) {
	b := NewTokenBucket(0, 5)
	now := bucketTime()
	_ = b.TryAcquireN(now, 5) // 排空
	got := b.NextAvailableAt(now)
	if !got.IsZero() {
		t.Fatalf("Rate=0 empty bucket should return zero time; got %v", got)
	}
}

func TestTokenBucket_TryAcquireN_EdgeCases(t *testing.T) {
	b := NewTokenBucket(10, 5)
	now := bucketTime()
	if !b.TryAcquireN(now, 0) {
		t.Fatal("n=0 should succeed (no-op)")
	}
	if b.TryAcquireN(now, -1) {
		t.Fatal("n<0 should fail")
	}
	if !b.TryAcquireN(now, 5) {
		t.Fatal("n=burst should succeed on full bucket")
	}
	if b.TryAcquireN(now, 6) {
		t.Fatal("n>burst should fail")
	}
}

func TestTokenBucket_RefundAddsButCaps(t *testing.T) {
	b := NewTokenBucket(0, 5) // rate 0 让补充可预测
	now := bucketTime()
	_ = b.TryAcquireN(now, 3) // tokens = 2
	b.Refund(now)             // tokens = 3
	b.Refund(now)             // tokens = 4
	b.Refund(now)             // tokens = 5
	b.Refund(now)             // 本应为 6, 被钳制到 5
	tokens, _ := b.Snapshot()
	if tokens != 5 {
		t.Fatalf("Refund clamp: tokens=%.1f; want 5", tokens)
	}
}

func TestTokenBucket_ConcurrentAcquireDoesNotExceedBurst(t *testing.T) {
	b := NewTokenBucket(0, 50) // rate 0 = 测试期间不补充
	now := bucketTime()
	// 第一次调用设置游标。用 n=0 仅打上时间戳而不消费。
	_ = b.TryAcquireN(now, 0)

	var wg sync.WaitGroup
	var success int64
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.TryAcquire(now) {
				atomic.AddInt64(&success, 1)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt64(&success); got != 50 {
		t.Fatalf("concurrent acquire success=%d; want exactly 50 (burst)", got)
	}
}

func TestTokenBucket_NegativeInputClamps(t *testing.T) {
	b := NewTokenBucket(-1, -10)
	if b.Rate != 0 || b.Burst != 0 {
		t.Fatalf("negative inputs should clamp to 0; Rate=%.1f Burst=%.1f", b.Rate, b.Burst)
	}
}

func TestTokenBucket_BackwardClockNoRegress(t *testing.T) {
	b := NewTokenBucket(10, 5)
	now := bucketTime()
	_ = b.TryAcquireN(now, 2)            // tokens = 3
	_ = b.TryAcquireN(now.Add(-1*time.Second), 0) // 时间倒退, no-op
	tokens, _ := b.Snapshot()
	if tokens != 3 {
		t.Fatalf("backward clock should not regress; tokens=%.1f", tokens)
	}
}
