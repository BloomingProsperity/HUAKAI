package precheck

import (
	"sync"
	"testing"
	"time"
)

// fixedClock 返回一个锚定在窗口边界上的可控时钟,使固定窗口的算术保持
// 确定性。
func fixedClock(base time.Time) (func() time.Time, *time.Time) {
	cur := base
	return func() time.Time { return cur }, &cur
}

var windowBase = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

func TestTryRecordConcurrentRPMNeverExceedsLimit(t *testing.T) {
	counter := New(time.Minute, time.Now)
	const limit = 7
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if counter.TryRecord(41, Limits{RPM: limit}, 0).Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != limit {
		t.Fatalf("并发准入=%d want %d", allowed, limit)
	}
}

// AC1:在 RPM 预算之内时账号被放行。
func TestCheck_UnderRPM_Allows(t *testing.T) {
	clock, _ := fixedClock(windowBase)
	c := New(time.Minute, clock)
	lim := Limits{RPM: 5}
	for i := 0; i < 4; i++ {
		c.Record(7, 0)
	}
	if d := c.Check(7, lim, 0); !d.Allowed {
		t.Fatalf("4 of 5 used should allow, got %+v", d)
	}
}

// AC2 + 变异守卫:到达 RPM 预算时,下一个请求会在 rpm 维度被拒。
// 若 `count+1 > RPM` 比较被删除或反转,此测试必然转红。
func TestCheck_AtRPM_Denies(t *testing.T) {
	clock, _ := fixedClock(windowBase)
	c := New(time.Minute, clock)
	lim := Limits{RPM: 5}
	for i := 0; i < 5; i++ {
		c.Record(7, 0)
	}
	d := c.Check(7, lim, 0)
	if d.Allowed || d.Dimension != DimensionRPM {
		t.Fatalf("5 of 5 used should deny on rpm, got %+v", d)
	}
	// 有区分度:同一时刻另一个不同的账号不受影响。
	if d := c.Check(8, lim, 0); !d.Allowed {
		t.Fatalf("untouched account must still allow, got %+v", d)
	}
}

// AC3:RPM 和 TPM 是相互独立的预算。两个有区分度的 fixture —— 一个只 TPM
// 满,一个只 RPM 满 —— 证明任一维度都不会掩盖另一维度。
func TestCheck_TPM_And_RPM_Independent(t *testing.T) {
	t.Run("tpm full, rpm spare", func(t *testing.T) {
		clock, _ := fixedClock(windowBase)
		c := New(time.Minute, clock)
		lim := Limits{RPM: 100, TPM: 10}
		c.Record(1, 10) // 1 个请求(rpm 离 100 还很远),10 个 token(tpm 恰好满)
		d := c.Check(1, lim, 1)
		if d.Allowed || d.Dimension != DimensionTPM {
			t.Fatalf("tpm full must deny on tpm while rpm has room, got %+v", d)
		}
	})
	t.Run("rpm full, tpm spare", func(t *testing.T) {
		clock, _ := fixedClock(windowBase)
		c := New(time.Minute, clock)
		lim := Limits{RPM: 1, TPM: 1000}
		c.Record(2, 5) // 1 个请求(rpm 恰好满),5 个 token(tpm 离 1000 还很远)
		d := c.Check(2, lim, 1)
		if d.Allowed || d.Dimension != DimensionRPM {
			t.Fatalf("rpm full must deny on rpm while tpm has room, got %+v", d)
		}
	})
}

// AC4:跨越窗口边界会重置预算。
func TestWindow_Rollover_Resets(t *testing.T) {
	clock, cur := fixedClock(windowBase)
	c := New(time.Minute, clock)
	lim := Limits{RPM: 1}
	c.Record(3, 0)
	if d := c.Check(3, lim, 0); d.Allowed {
		t.Fatalf("rpm 1 used should deny in-window, got %+v", d)
	}
	*cur = windowBase.Add(time.Minute) // 下一个固定窗口
	if d := c.Check(3, lim, 0); !d.Allowed {
		t.Fatalf("new window must reset and allow, got %+v", d)
	}
}

// AC6:零 limit 表示无限制 —— 账号永不会在该维度上被阻塞。
func TestCheck_ZeroLimit_Unlimited(t *testing.T) {
	clock, _ := fixedClock(windowBase)
	c := New(time.Minute, clock)
	lim := Limits{RPM: 0, TPM: 0}
	for i := 0; i < 1000; i++ {
		c.Record(9, 1000)
	}
	if d := c.Check(9, lim, 1_000_000); !d.Allowed {
		t.Fatalf("zero limits must always allow, got %+v", d)
	}
}

// Fail-open:nil Counter 和 id<=0 永不阻塞,且 Record 是一个安全的空操作。
func TestNilCounter_And_BadID_FailOpen(t *testing.T) {
	var c *Counter
	if d := c.Check(1, Limits{RPM: 1}, 0); !d.Allowed {
		t.Fatalf("nil counter must allow, got %+v", d)
	}
	c.Record(1, 1) // 不得 panic
	live := New(time.Minute, func() time.Time { return windowBase })
	if d := live.Check(0, Limits{RPM: 1}, 0); !d.Allowed {
		t.Fatalf("account id 0 must allow, got %+v", d)
	}
}

// estTokens 是前瞻性的:一个其自身估算就会撑爆 TPM 预算的单个请求,会在
// 被记录之前就被拒绝。
func TestCheck_EstTokens_LooksAhead(t *testing.T) {
	clock, _ := fixedClock(windowBase)
	c := New(time.Minute, clock)
	lim := Limits{TPM: 100}
	if d := c.Check(4, lim, 101); d.Allowed || d.Dimension != DimensionTPM {
		t.Fatalf("a single oversized request must be denied on tpm, got %+v", d)
	}
	if d := c.Check(4, lim, 100); !d.Allowed {
		t.Fatalf("a request exactly at budget must fit, got %+v", d)
	}
}

// 并发:在 -race 下的 Record/Check 不得破坏计数器。
func TestCounter_RaceSafe(t *testing.T) {
	c := New(time.Minute, func() time.Time { return windowBase })
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Record(11, 1)
			_ = c.Check(11, Limits{RPM: 1000, TPM: 1000}, 1)
		}()
	}
	wg.Wait()
}

// TestCounter_EvictsStaleEntriesAtCap 守护有界性(#2): 达 maxKeys 上限后新增账号键时,
// 必须清扫上一窗口遗留的陈旧条目, 使 map 收敛到当前窗口活跃账号数, 而非历史全部账号数。
// 判别性: 删除 live() 里的 sweepStale 调用 → 陈旧条目不回收 → 跨窗后 reqs 仍含 1,2,3,4 共 4 条
// → 下方断言 len==1 转红。
func TestCounter_EvictsStaleEntriesAtCap(t *testing.T) {
	clock, cur := fixedClock(windowBase)
	c := New(time.Minute, clock)
	c.maxKeys = 3 // 压低上限以免插入 10 万条; 仅测内存生命周期, 不改限流判定语义

	// 窗口 W1: 账号 1/2/3 各记一次, map 填到上限。
	for _, id := range []int64{1, 2, 3} {
		c.Record(id, 0)
	}
	if got := len(c.reqs); got != 3 {
		t.Fatalf("W1 后 reqs 应有 3 条, 实际 %d", got)
	}

	// 跨入下一个固定窗口 W2; 账号 1/2/3 自此变陈旧(下次访问本会被重置)。
	*cur = windowBase.Add(time.Minute)

	// 账号 4 是此前未见的新键, 此刻 len(reqs)=3 已达 maxKeys → 触发清扫陈旧的 1/2/3, 仅留 4。
	c.Record(4, 0)
	if got := len(c.reqs); got != 1 {
		t.Fatalf("跨窗后新增键应清扫陈旧条目使 reqs 收敛到 1(仅账号4), 实际 %d", got)
	}
	if got := len(c.toks); got != 1 {
		t.Fatalf("toks map 同样应被清扫到 1, 实际 %d", got)
	}
	// 账号 4 在 W2 的计数从 0 起算(限流判定不受清扫影响)。
	if d := c.Check(4, Limits{RPM: 2}, 0); !d.Allowed {
		t.Fatalf("新窗口账号 4 应在预算内, 实际 %+v", d)
	}
}

// TestCounter_SweepKeepsCurrentWindowBucket 守护清扫的关键安全属性: 清扫只能删「陈旧」桶,
// 绝不能误删当前窗口的活跃桶并清零其已累计计数——否则该账号当前窗口的 RPM/TPM 计数被重置,
// 已用满预算的账号会被错误放行, 限流被绕过。
// 判别性: 把 sweepStale 的 wc.start.Before(start) 误写成 <=(即 !After), 会连当前窗口活跃桶一并删,
// 账号2 计数被清零 → 下方 Check 由「拒」变「放行」→ 本测试转红。
func TestCounter_SweepKeepsCurrentWindowBucket(t *testing.T) {
	clock, cur := fixedClock(windowBase)
	c := New(time.Minute, clock)
	c.maxKeys = 2

	c.Record(1, 0)                     // W1: 账号1, 跨窗后将成陈旧清扫候选
	*cur = windowBase.Add(time.Minute) // 跨入下一窗口 W2
	c.Record(2, 0)                     // W2: 账号2 当前窗口活跃(已计 1 次); map={1(W1陈旧),2(W2活跃)} 达上限

	c.Record(3, 0) // 新键触发 sweep: 应只删陈旧的账号1, 保留当前窗口活跃的账号2

	// 账号2 当前窗口已记 1 次, RPM=1 下「再来一个」必须被拒——证明其活跃计数未被 sweep 误清零。
	if d := c.Check(2, Limits{RPM: 1}, 0); d.Allowed {
		t.Fatal("sweep 误删了当前窗口活跃桶: 账号2 的已累计计数被清零, 限流被绕过")
	}
}
