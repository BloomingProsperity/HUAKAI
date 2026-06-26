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
