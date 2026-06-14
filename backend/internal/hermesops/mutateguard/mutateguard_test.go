package mutateguard

import (
	"context"
	"errors"
	"testing"
	"time"
)

// clock is a manually-advanced clock for deterministic sliding-window tests.
type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

func TestSemaphore_DisabledIsUnbounded(t *testing.T) {
	// A size<=0 Semaphore is the disable sentinel: Acquire is an immediate no-op
	// success regardless of how many are outstanding (legacy unbounded).
	// Mutation: make NewSemaphore(0) build a weighted(0) — Acquire(1) would then
	// always fail -> this loop would see an error -> RED.
	s := NewSemaphore(0)
	if s.Enabled() {
		t.Fatalf("size 0 should be disabled")
	}
	releases := make([]func(), 0, 100)
	for i := 0; i < 100; i++ {
		rel, err := s.Acquire(context.Background(), time.Millisecond)
		if err != nil {
			t.Fatalf("disabled Acquire #%d err=%v want nil (unbounded)", i, err)
		}
		releases = append(releases, rel)
	}
	for _, r := range releases {
		r()
	}
}

func TestSemaphore_BusyOnTimeout(t *testing.T) {
	// size 1, slot held -> a second Acquire times out with ErrBusy within
	// acquireWait (clean, bounded). Mutation: pass acquireWait<=0 with a Background
	// ctx and Acquire would block forever -> test deadline RED.
	s := NewSemaphore(1)
	rel, err := s.Acquire(context.Background(), 50*time.Millisecond)
	if err != nil {
		t.Fatalf("first Acquire err=%v want nil", err)
	}
	defer rel()
	start := time.Now()
	_, err = s.Acquire(context.Background(), 50*time.Millisecond)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("second Acquire err=%v want ErrBusy", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("second Acquire took too long; busy must be bounded by acquireWait")
	}
}

func TestRateLimiter_DisabledAlwaysAllows(t *testing.T) {
	// limit<=0 is the disable sentinel: Allow always true.
	l := NewRateLimiter(0, time.Minute, 0, nil)
	if l.Enabled() {
		t.Fatalf("limit 0 should be disabled")
	}
	for i := 0; i < 1000; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("disabled limiter rejected at #%d — must always allow", i)
		}
	}
}

func TestRateLimiter_SlidingWindowPerKey(t *testing.T) {
	// limit 2 / 1m: the third within the window is rejected; after the window
	// slides, the key is allowed again. A rejection does NOT record (so it cannot
	// push the window out). Mutation: record on rejection -> the post-window allow
	// would still see 3 stamps and reject -> RED.
	clk := &clock{t: time.Unix(0, 0)}
	l := NewRateLimiter(2, time.Minute, 0, clk.Now)

	if ok, _ := l.Allow("a"); !ok {
		t.Fatalf("a#1 rejected")
	}
	if ok, _ := l.Allow("a"); !ok {
		t.Fatalf("a#2 rejected")
	}
	if ok, retry := l.Allow("a"); ok || retry <= 0 {
		t.Fatalf("a#3 allowed=%v retry=%v want rejected with positive retry", ok, retry)
	}
	// A different key is independent.
	if ok, _ := l.Allow("b"); !ok {
		t.Fatalf("b#1 rejected — keys must be independent")
	}
	// Slide the window fully past the two recorded a-stamps.
	clk.t = clk.t.Add(2 * time.Minute)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatalf("a after window slide rejected — stale stamps must be pruned")
	}
}

func TestRateLimiter_FailClosedOnKeyPressure(t *testing.T) {
	// maxKeys protects memory: once full of live keys, a NEW key is denied
	// (fail-closed), matching loginthrottle's posture. Mutation: skip the maxKeys
	// check -> the new key is allowed and the table grows unbounded -> RED.
	clk := &clock{t: time.Unix(0, 0)}
	l := NewRateLimiter(5, time.Minute, 2, clk.Now)
	if ok, _ := l.Allow("k1"); !ok {
		t.Fatalf("k1 rejected")
	}
	if ok, _ := l.Allow("k2"); !ok {
		t.Fatalf("k2 rejected")
	}
	// Table full (2 live keys); a third distinct key must fail closed.
	if ok, _ := l.Allow("k3"); ok {
		t.Fatalf("k3 allowed despite key-pressure — must fail closed to protect memory")
	}
}
