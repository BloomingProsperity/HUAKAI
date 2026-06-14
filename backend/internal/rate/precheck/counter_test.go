package precheck

import (
	"sync"
	"testing"
	"time"
)

// fixedClock returns a controllable clock anchored to a window boundary so the
// fixed-window math is deterministic.
func fixedClock(base time.Time) (func() time.Time, *time.Time) {
	cur := base
	return func() time.Time { return cur }, &cur
}

var windowBase = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

// AC1: under the RPM budget the account is allowed.
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

// AC2 + mutation guard: at the RPM budget the next request is denied on rpm.
// If the `count+1 > RPM` comparison is deleted or flipped, this MUST go red.
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
	// Discriminating: a DIFFERENT account at the same instant is unaffected.
	if d := c.Check(8, lim, 0); !d.Allowed {
		t.Fatalf("untouched account must still allow, got %+v", d)
	}
}

// AC3: RPM and TPM are independent budgets. Two discriminating fixtures — one
// where only TPM is full, one where only RPM is full — prove neither dimension
// masks the other.
func TestCheck_TPM_And_RPM_Independent(t *testing.T) {
	t.Run("tpm full, rpm spare", func(t *testing.T) {
		clock, _ := fixedClock(windowBase)
		c := New(time.Minute, clock)
		lim := Limits{RPM: 100, TPM: 10}
		c.Record(1, 10) // 1 req (rpm far from 100), 10 tokens (tpm exactly full)
		d := c.Check(1, lim, 1)
		if d.Allowed || d.Dimension != DimensionTPM {
			t.Fatalf("tpm full must deny on tpm while rpm has room, got %+v", d)
		}
	})
	t.Run("rpm full, tpm spare", func(t *testing.T) {
		clock, _ := fixedClock(windowBase)
		c := New(time.Minute, clock)
		lim := Limits{RPM: 1, TPM: 1000}
		c.Record(2, 5) // 1 req (rpm exactly full), 5 tokens (tpm far from 1000)
		d := c.Check(2, lim, 1)
		if d.Allowed || d.Dimension != DimensionRPM {
			t.Fatalf("rpm full must deny on rpm while tpm has room, got %+v", d)
		}
	})
}

// AC4: crossing the window boundary resets the budget.
func TestWindow_Rollover_Resets(t *testing.T) {
	clock, cur := fixedClock(windowBase)
	c := New(time.Minute, clock)
	lim := Limits{RPM: 1}
	c.Record(3, 0)
	if d := c.Check(3, lim, 0); d.Allowed {
		t.Fatalf("rpm 1 used should deny in-window, got %+v", d)
	}
	*cur = windowBase.Add(time.Minute) // next fixed window
	if d := c.Check(3, lim, 0); !d.Allowed {
		t.Fatalf("new window must reset and allow, got %+v", d)
	}
}

// AC6: a zero limit means unlimited — the account is never blocked on it.
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

// Fail-open: a nil Counter and id<=0 never block, and Record is a safe no-op.
func TestNilCounter_And_BadID_FailOpen(t *testing.T) {
	var c *Counter
	if d := c.Check(1, Limits{RPM: 1}, 0); !d.Allowed {
		t.Fatalf("nil counter must allow, got %+v", d)
	}
	c.Record(1, 1) // must not panic
	live := New(time.Minute, func() time.Time { return windowBase })
	if d := live.Check(0, Limits{RPM: 1}, 0); !d.Allowed {
		t.Fatalf("account id 0 must allow, got %+v", d)
	}
}

// estTokens is forward-looking: a single request whose own estimate would blow
// the TPM budget is denied before it is recorded.
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

// Concurrency: Record/Check under -race must not corrupt the counter.
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
