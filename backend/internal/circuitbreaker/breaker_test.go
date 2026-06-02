package circuitbreaker

import (
	"sync"
	"testing"
	"time"
)

type manualClock struct {
	now time.Time
}

func newManualClock() *manualClock {
	return &manualClock{now: time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)}
}

func (c *manualClock) Now() time.Time {
	return c.now
}

func (c *manualClock) Add(d time.Duration) {
	c.now = c.now.Add(d)
}

func newTestBreaker(clock *manualClock) *Breaker {
	return New(Config{
		FailureThreshold:         3,
		OpenCooldown:             10 * time.Second,
		HalfOpenMaxProbes:        2,
		HalfOpenSuccessesToClose: 2,
		Clock:                    clock.Now,
	})
}

func openBreaker(t *testing.T, b *Breaker, key string) {
	t.Helper()
	for i := 0; i < 3; i++ {
		b.RecordFailure(key)
	}
	if got := b.StateOf(key).State; got != Open {
		t.Fatalf("StateOf(%q).State=%v want %v", key, got, Open)
	}
}

// 守的缺陷: 连续失败达到阈值后必须开闸。Mutation: off-by-one 或不开闸会让状态仍为 Closed, 测试变红。
func TestOpensAfterConsecutiveFailures(t *testing.T) {
	clock := newManualClock()
	b := newTestBreaker(clock)
	key := "tenant-a"

	b.RecordFailure(key)
	b.RecordFailure(key)
	if got := b.StateOf(key).State; got != Closed {
		t.Fatalf("state before threshold=%v want %v", got, Closed)
	}

	b.RecordFailure(key)
	view := b.StateOf(key)
	if view.State != Open {
		t.Fatalf("state after threshold=%v want %v", view.State, Open)
	}
	if view.FailureCount != 3 {
		t.Fatalf("failure count=%d want 3", view.FailureCount)
	}
}

// 守的缺陷: money 核心默认 fail-closed, 计费不可用时拒绝而不是放行不记账。Mutation: 默认 fail-open 会 Allowed=true, 测试变红。
func TestFailClosedWhenOpenDeniesByDefault(t *testing.T) {
	clock := newManualClock()
	b := newTestBreaker(clock)
	key := "tenant-a"
	openBreaker(t, b, key)

	decision := b.Allow(key)
	if decision.Allowed {
		t.Fatalf("Allowed=%v want false for default fail-closed open breaker", decision.Allowed)
	}
	if decision.ServingUntracked {
		t.Fatalf("ServingUntracked=%v want false when fail-closed denies", decision.ServingUntracked)
	}
	if decision.State != Open {
		t.Fatalf("State=%v want %v", decision.State, Open)
	}
}

// 守的缺陷: fail-open 覆盖必须显式放行并标记 untracked。Mutation: 忽略 FailMode 会继续拒绝, 测试变红。
func TestFailOpenOverrideServesUntracked(t *testing.T) {
	clock := newManualClock()
	b := newTestBreaker(clock)
	key := "tenant-a:group-risk-accepted"
	openBreaker(t, b, key)
	b.SetFailMode(key, FailOpen)

	decision := b.Allow(key)
	if !decision.Allowed {
		t.Fatalf("Allowed=%v want true for fail-open override", decision.Allowed)
	}
	if !decision.ServingUntracked {
		t.Fatalf("ServingUntracked=%v want true for fail-open override", decision.ServingUntracked)
	}
	if decision.State != Open {
		t.Fatalf("State=%v want %v", decision.State, Open)
	}
}

// 守的缺陷: 只有冷却结束后才允许 Open 转 HalfOpen 探测。Mutation: 忽略 cooldown 永远 Open 或冷却未到提前 HalfOpen, 测试变红。
func TestHalfOpenAfterCooldown(t *testing.T) {
	clock := newManualClock()
	b := newTestBreaker(clock)
	key := "tenant-a"
	openBreaker(t, b, key)

	early := b.Allow(key)
	if early.Allowed || early.State != Open {
		t.Fatalf("before cooldown decision=%+v want denied Open", early)
	}

	clock.Add(10 * time.Second)
	decision := b.Allow(key)
	if !decision.Allowed {
		t.Fatalf("Allowed=%v want true for half-open probe", decision.Allowed)
	}
	if decision.State != HalfOpen {
		t.Fatalf("State=%v want %v", decision.State, HalfOpen)
	}
	if got := b.StateOf(key).State; got != HalfOpen {
		t.Fatalf("StateOf=%v want %v", got, HalfOpen)
	}
}

// 守的缺陷: HalfOpen 成功次数达标后必须闭合并清零。Mutation: 成功后不闭合或不清零, 测试变红。
func TestHalfOpenSuccessCloses(t *testing.T) {
	clock := newManualClock()
	b := newTestBreaker(clock)
	key := "tenant-a"
	openBreaker(t, b, key)
	clock.Add(10 * time.Second)

	if decision := b.Allow(key); !decision.Allowed || decision.State != HalfOpen {
		t.Fatalf("first probe decision=%+v want allowed HalfOpen", decision)
	}
	b.RecordSuccess(key)
	if got := b.StateOf(key).State; got != HalfOpen {
		t.Fatalf("after one success state=%v want %v", got, HalfOpen)
	}

	if decision := b.Allow(key); !decision.Allowed || decision.State != HalfOpen {
		t.Fatalf("second probe decision=%+v want allowed HalfOpen", decision)
	}
	b.RecordSuccess(key)
	view := b.StateOf(key)
	if view.State != Closed {
		t.Fatalf("state=%v want %v", view.State, Closed)
	}
	if view.FailureCount != 0 {
		t.Fatalf("failure count=%d want 0 after close", view.FailureCount)
	}
}

// 守的缺陷: HalfOpen 期间任一失败必须重新 Open 并刷新冷却。Mutation: 失败却闭合或不刷新 openUntil, 测试变红。
func TestHalfOpenFailureReopens(t *testing.T) {
	clock := newManualClock()
	b := newTestBreaker(clock)
	key := "tenant-a"
	openBreaker(t, b, key)
	firstOpenUntil := b.StateOf(key).OpenUntil
	clock.Add(10 * time.Second)

	if decision := b.Allow(key); !decision.Allowed || decision.State != HalfOpen {
		t.Fatalf("probe decision=%+v want allowed HalfOpen", decision)
	}
	wantOpenUntil := clock.Now().Add(10 * time.Second)
	b.RecordFailure(key)
	view := b.StateOf(key)
	if view.State != Open {
		t.Fatalf("state=%v want %v", view.State, Open)
	}
	if !view.OpenUntil.Equal(wantOpenUntil) {
		t.Fatalf("openUntil=%s want %s", view.OpenUntil, wantOpenUntil)
	}
	if !view.OpenUntil.After(firstOpenUntil) {
		t.Fatalf("openUntil=%s should refresh after previous %s", view.OpenUntil, firstOpenUntil)
	}
}

// 守的缺陷: key 之间必须隔离, 不能把某租户开闸污染到其他租户。Mutation: 全局共享状态会让 keyB 被拒, 测试变红。
func TestPerKeyIsolation(t *testing.T) {
	clock := newManualClock()
	b := newTestBreaker(clock)
	openBreaker(t, b, "tenant-a")

	viewB := b.StateOf("tenant-b")
	if viewB.State != Closed {
		t.Fatalf("tenant-b state=%v want %v", viewB.State, Closed)
	}
	decisionB := b.Allow("tenant-b")
	if !decisionB.Allowed {
		t.Fatalf("tenant-b decision=%+v want allowed", decisionB)
	}
}

// 守的缺陷: 运维可见状态必须准确包含 state/failureCount/openUntil/failMode。Mutation: StateOf 返回 stale 或错值, 测试变红。
func TestStateOfReportsAccurateState(t *testing.T) {
	clock := newManualClock()
	b := newTestBreaker(clock)
	key := "tenant-a:group-manual"
	b.SetFailMode(key, FailOpen)
	openBreaker(t, b, key)

	view := b.StateOf(key)
	if view.State != Open {
		t.Fatalf("state=%v want %v", view.State, Open)
	}
	if view.FailureCount != 3 {
		t.Fatalf("failureCount=%d want 3", view.FailureCount)
	}
	if want := clock.Now().Add(10 * time.Second); !view.OpenUntil.Equal(want) {
		t.Fatalf("openUntil=%s want %s", view.OpenUntil, want)
	}
	if view.FailMode != FailOpen {
		t.Fatalf("failMode=%v want %v", view.FailMode, FailOpen)
	}
}

// 守的缺陷: 运维覆盖必须能强制开闸和闭合。Mutation: ForceOpen/ForceClose 无效会导致拒绝/放行方向错误, 测试变红。
func TestForceOpenForceCloseOverride(t *testing.T) {
	clock := newManualClock()
	b := newTestBreaker(clock)
	key := "tenant-a"

	b.ForceOpen(key)
	openDecision := b.Allow(key)
	if openDecision.Allowed || openDecision.State != Open {
		t.Fatalf("after ForceOpen decision=%+v want denied Open", openDecision)
	}

	b.ForceClose(key)
	closeDecision := b.Allow(key)
	if !closeDecision.Allowed || closeDecision.State != Closed {
		t.Fatalf("after ForceClose decision=%+v want allowed Closed", closeDecision)
	}
}

// 守的缺陷: 所有 map 状态访问必须由 mutex 保护。Mutation: 去掉 mutex 后 go test -race 会报 data race, 测试变红。
func TestConcurrentAccessNoRace(t *testing.T) {
	clock := newManualClock()
	b := newTestBreaker(clock)
	keys := []string{"tenant-a", "tenant-b", "tenant-c"}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < 24; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			key := keys[worker%len(keys)]
			for i := 0; i < 200; i++ {
				_ = b.Allow(key)
				if i%3 == 0 {
					b.RecordFailure(key)
					continue
				}
				b.RecordSuccess(key)
				_ = b.StateOf(key)
			}
		}(worker)
	}
	close(start)
	wg.Wait()
}
