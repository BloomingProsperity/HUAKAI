// HUAKAI · iKun

package loginthrottle

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clk 是注入的可推进时钟,避免 wall-clock 抖动(memory: 不拿 time.Now 当测试时间源)。
type clk struct{ t time.Time }

func (c *clk) now() time.Time          { return c.t }
func (c *clk) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClk() *clk {
	return &clk{t: time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)}
}

// failureCount/inFlightCount 是白盒断言辅助(同包,读未导出字段),用于精确钉住计数语义。
func (l *Limiter) failureCount(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		return 0
	}
	return len(b.failures)
}

func (l *Limiter) inFlightCount(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		return 0
	}
	return len(b.inFlight)
}

// TestLimiter_InFlightCapBlocksConcurrentKDF 是本包最关键的判别测:纯滑动窗口在「N 个并发请求
// 都还没记录失败前」会全部放行 → 同时冲进 argon2(CPU 放大)。reservation 模型必须在 Begin 当场
// 占槽, 使并发在途数超过 InFlightLimit 时直接拒。
//
// mutation: 若 Begin 不在当场把 reservation 写入 in-flight(改成只在 KDF 之后计数), 前 N 个并发
// Begin 全部 Allowed → 本测第 3 个仍 Allowed → 红(并发 argon2 放大复活)。
func TestLimiter_InFlightCapBlocksConcurrentKDF(t *testing.T) {
	c := newClk()
	l := New(Config{InFlightLimit: 2, WindowLimit: 100, BanAfter: 100, Now: c.now})

	le1, d1 := l.Begin("1.2.3.4")
	le2, d2 := l.Begin("1.2.3.4")
	if !d1.Allowed || !d2.Allowed {
		t.Fatalf("first two concurrent attempts should pass: d1=%v d2=%v", d1.Reason, d2.Reason)
	}
	_, d3 := l.Begin("1.2.3.4")
	if d3.Allowed {
		t.Fatal("3rd concurrent in-flight attempt must be DENIED before argon2 (else concurrent KDF amplification)")
	}
	if d3.Reason != ReasonIPInFlight {
		t.Fatalf("deny reason=%v, want ReasonIPInFlight", d3.Reason)
	}
	if d3.RetryAfter <= 0 {
		t.Fatalf("denied decision must carry RetryAfter, got %v", d3.RetryAfter)
	}
	// 释放一个槽 → 下一个应放行。
	le1.Success()
	_, d4 := l.Begin("1.2.3.4")
	if !d4.Allowed {
		t.Fatalf("after releasing one in-flight slot, next attempt must pass, got %v", d4.Reason)
	}
	le2.Cancel()
}

// TestLimiter_WindowFailureCap 钉住滑动窗口失败计数:窗口内失败累计达阈即拒,窗口过期后恢复。
// mutation: Failure 不追加失败时间 → 窗口永不触发 → 红。
func TestLimiter_WindowFailureCap(t *testing.T) {
	c := newClk()
	l := New(Config{InFlightLimit: 10, Window: time.Minute, WindowLimit: 3, BanAfter: 100, Now: c.now})

	for i := 0; i < 3; i++ {
		le, d := l.Begin("ip")
		if !d.Allowed {
			t.Fatalf("begin %d should pass, got %v", i, d.Reason)
		}
		le.Failure()
	}
	_, d := l.Begin("ip")
	if d.Allowed {
		t.Fatal("after WindowLimit failures within window, must deny")
	}
	if d.Reason != ReasonIPWindow {
		t.Fatalf("deny reason=%v, want ReasonIPWindow", d.Reason)
	}
	// 推进过窗口 → 失败被剪枝 → 恢复放行。
	c.advance(2 * time.Minute)
	_, d2 := l.Begin("ip")
	if !d2.Allowed {
		t.Fatalf("after window expiry, must allow again, got %v", d2.Reason)
	}
}

// TestLimiter_BanOutlivesWindow 钉住失败封禁:达 BanAfter 后封禁,且封禁必须比短失败窗口活得久
// (按 BanDuration 而非 Window),到期才恢复。
// mutation: Failure 不设 blockedUntil → 不封 → 红; 封禁误用 Window 时长 → 「过窗口仍封」子断言红。
func TestLimiter_BanOutlivesWindow(t *testing.T) {
	c := newClk()
	l := New(Config{
		InFlightLimit: 10, Window: time.Minute, WindowLimit: 100,
		BanWindow: 5 * time.Minute, BanAfter: 4, BanDuration: 10 * time.Minute, Now: c.now,
	})

	for i := 0; i < 4; i++ {
		le, d := l.Begin("ip")
		if !d.Allowed {
			t.Fatalf("begin %d should pass (window high, only ban applies), got %v", i, d.Reason)
		}
		le.Failure()
	}
	_, d := l.Begin("ip")
	if d.Allowed || d.Reason != ReasonIPBanned {
		t.Fatalf("after BanAfter failures must be banned, got allowed=%v reason=%v", d.Allowed, d.Reason)
	}
	// 过失败窗口(1m)但未过封禁(10m) → 仍封禁(关键:封禁不随短窗口解除)。
	c.advance(2 * time.Minute)
	_, d2 := l.Begin("ip")
	if d2.Allowed || d2.Reason != ReasonIPBanned {
		t.Fatalf("ban must outlive the short failure window, got allowed=%v reason=%v", d2.Allowed, d2.Reason)
	}
	// 过封禁时长 → 恢复。
	c.advance(10 * time.Minute)
	_, d3 := l.Begin("ip")
	if !d3.Allowed {
		t.Fatalf("after ban duration elapses, must allow, got %v", d3.Reason)
	}
}

// TestLimiter_SuccessDoesNotAccumulate 钉住:成功登录不消耗失败窗口/不触发封禁,正常用户反复成功
// 不会被误限。mutation: Success 误记失败 → 第 4 次后被窗口/封禁拒 → 红。
func TestLimiter_SuccessDoesNotAccumulate(t *testing.T) {
	c := newClk()
	l := New(Config{InFlightLimit: 10, Window: time.Minute, WindowLimit: 3, BanAfter: 4, Now: c.now})

	for i := 0; i < 25; i++ {
		le, d := l.Begin("ip")
		if !d.Allowed {
			t.Fatalf("success #%d must pass (success must not accumulate), got %v", i, d.Reason)
		}
		le.Success()
	}
	if got := l.failureCount("ip"); got != 0 {
		t.Fatalf("success path recorded %d failures, want 0", got)
	}
}

// TestLimiter_SuccessDoesNotClearPriorFailures 钉住一个安全属性: 一次成功登录只释放在途槽,
// 绝不清空该 IP 之前累计的失败 —— 否则攻击者用一个有效账号穿插登录即可重置风控窗口, 让暴力
// 破解永不触发封禁。codex 复审点名补强。
//
// mutation: 让 Success 顺手清空 failures(误把成功当「洗白」)→ 成功后 failureCount 归零 +
// 后续达不到窗口阈值 → 本测红。
func TestLimiter_SuccessDoesNotClearPriorFailures(t *testing.T) {
	c := newClk()
	l := New(Config{InFlightLimit: 10, Window: time.Minute, WindowLimit: 3, BanAfter: 100, Now: c.now})

	// 先攒 2 次失败(未到窗口阈值 3)。
	for i := 0; i < 2; i++ {
		le, d := l.Begin("ip")
		if !d.Allowed {
			t.Fatalf("failure begin %d denied: %v", i, d.Reason)
		}
		le.Failure()
	}
	// 一次成功 —— 不得清掉那 2 次失败。
	le, d := l.Begin("ip")
	if !d.Allowed {
		t.Fatalf("success begin denied: %v", d.Reason)
	}
	le.Success()
	if got := l.failureCount("ip"); got != 2 {
		t.Fatalf("a successful login cleared prior failures (failureCount=%d, want 2); attacker could reset the throttle with one valid login", got)
	}
	// 再来 1 次失败 → 累计 3 → 窗口必须触发(证明成功没把预算洗白)。
	le, d = l.Begin("ip")
	if !d.Allowed {
		t.Fatalf("post-success failure begin denied: %v", d.Reason)
	}
	le.Failure()
	if _, d := l.Begin("ip"); d.Allowed {
		t.Fatal("2 prior + 1 post-success failures = 3 must trip the window; success must not have reset the budget")
	}
}

// TestLimiter_CancelReleasesAndCommitIdempotent 钉住:Cancel 释放槽且不记失败;commit 后再调任何
// commit 方法都是 no-op(不会重复记失败/重复释放)。
// mutation: Cancel 不释放槽 → 后续 Begin 被 in-flight 拒 → 红; Cancel 误记失败 → failureCount>0 → 红。
func TestLimiter_CancelReleasesAndCommitIdempotent(t *testing.T) {
	c := newClk()
	l := New(Config{InFlightLimit: 1, WindowLimit: 100, BanAfter: 100, Now: c.now})

	le, d := l.Begin("ip")
	if !d.Allowed {
		t.Fatalf("first begin should pass, got %v", d.Reason)
	}
	_, d2 := l.Begin("ip")
	if d2.Allowed {
		t.Fatal("InFlightLimit=1, second concurrent must be denied")
	}
	le.Cancel()
	if got := l.inFlightCount("ip"); got != 0 {
		t.Fatalf("Cancel must release the in-flight slot, still %d held", got)
	}
	// commit 后再调 —— 必须 no-op,尤其 Failure 不得记失败。
	le.Cancel()
	le.Failure()
	le.Success()
	if got := l.failureCount("ip"); got != 0 {
		t.Fatalf("post-commit Failure() leaked %d failures; commit must be idempotent no-op", got)
	}
	le3, d3 := l.Begin("ip")
	if !d3.Allowed {
		t.Fatalf("after Cancel freed the slot, next begin must pass, got %v", d3.Reason)
	}
	le3.Cancel()
}

// TestLimiter_MaxKeysFailClosedThenEvict 钉住限流器自身内存保护:到 MaxKeys 且现有 key 都活跃时
// 新 key fail-closed(拒而非无限增长);当某旧 key 空闲且过期后,新 key 可回收它并放行。
// mutation: 去掉 key 压力 fail-closed → 新 key 直接放行 map 无限涨 → 「fail-closed」断言红;
// 去掉 evict → 旧 key 空闲后新 key 仍被拒 → 「evict 后放行」断言红。
func TestLimiter_MaxKeysFailClosedThenEvict(t *testing.T) {
	c := newClk()
	l := New(Config{
		InFlightLimit: 5, WindowLimit: 100, BanAfter: 100,
		Window: time.Minute, BanWindow: time.Minute, MaxKeys: 2, Now: c.now,
	})

	leA, da := l.Begin("a")
	_, db := l.Begin("b")
	if !da.Allowed || !db.Allowed {
		t.Fatalf("first two distinct keys should pass: a=%v b=%v", da.Reason, db.Reason)
	}
	// a、b 都持有 in-flight → 不可回收 → 新 key c fail-closed。
	_, dc := l.Begin("c")
	if dc.Allowed {
		t.Fatal("at MaxKeys with all keys active, new key must fail-closed")
	}
	if dc.Reason != ReasonKeyPressure {
		t.Fatalf("deny reason=%v, want ReasonKeyPressure", dc.Reason)
	}
	// 释放 a 并推进过保留窗口 → a 变得可回收。
	leA.Success()
	c.advance(2 * time.Minute)
	_, dc2 := l.Begin("c")
	if !dc2.Allowed {
		t.Fatalf("after key 'a' goes idle+stale, new key 'c' should evict it and pass, got %v", dc2.Reason)
	}
}

// TestLimiter_ConcurrentNeverExceedsInFlight 在 -race 下验证无数据竞争,且并发在途数永不超过
// InFlightLimit(reservation 在锁内原子完成)。mutation: 去掉 Begin 的互斥/原子占槽 → -race 报警
// 或并发峰值 > 上限 → 红。
func TestLimiter_ConcurrentNeverExceedsInFlight(t *testing.T) {
	const limit = 3
	l := New(Config{InFlightLimit: limit, WindowLimit: 100000, BanAfter: 100000, Now: time.Now})

	var cur, max int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	var allowed int64
	for i := 0; i < 300; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			le, d := l.Begin("ip")
			if !d.Allowed {
				return
			}
			atomic.AddInt64(&allowed, 1)
			mu.Lock()
			cur++
			if cur > max {
				max = cur
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			cur--
			mu.Unlock()
			le.Success()
		}()
	}
	wg.Wait()
	if max > limit {
		t.Fatalf("peak concurrent in-flight = %d, exceeds InFlightLimit=%d (reservation race)", max, limit)
	}
	if allowed == 0 {
		t.Fatal("expected some attempts to be allowed")
	}
}
