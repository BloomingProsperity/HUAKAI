package mutateguard

import (
	"context"
	"errors"
	"testing"
	"time"
)

// clock 是用于确定性滑动窗口测试的手动推进时钟。
type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

func TestSemaphore_DisabledIsUnbounded(t *testing.T) {
	// size<=0 的 Semaphore 是禁用哨兵:无论有多少在途,Acquire 都是即时 no-op 成功(旧的无上限)。
	// 变异:让 NewSemaphore(0) 构造一个 weighted(0)——这样 Acquire(1) 就会总是失败 ->
	// 本循环会看到 error -> 变红。
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
	// size 1,槽位被持有 -> 第二个 Acquire 在 acquireWait 内以 ErrBusy 超时(干净、有界)。
	// 变异:用一个 Background ctx 传入 acquireWait<=0,Acquire 就会永远阻塞 -> 测试截止变红。
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
	// limit<=0 是禁用哨兵:Allow 始终为 true。
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
	// limit 2 / 1m:窗口内的第三次被拒绝;窗口滑过之后,该键再次被允许。一次拒绝 NOT(不)记录
	// (所以它无法把窗口往后推)。变异:在拒绝时也记录 -> 窗口后的 allow 仍会看到 3 个时间戳并拒绝
	// -> 变红。
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
	// 不同的键互相独立。
	if ok, _ := l.Allow("b"); !ok {
		t.Fatalf("b#1 rejected — keys must be independent")
	}
	// 把窗口完全滑过那两个已记录的 a 时间戳。
	clk.t = clk.t.Add(2 * time.Minute)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatalf("a after window slide rejected — stale stamps must be pruned")
	}
}

func TestRateLimiter_FailClosedOnKeyPressure(t *testing.T) {
	// maxKeys 保护内存:一旦被存活键填满,一个 NEW(新)键就被拒绝(fail-closed),与 loginthrottle
	// 的姿态一致。变异:跳过 maxKeys 检查 -> 新键被允许,表无界增长 -> 变红。
	clk := &clock{t: time.Unix(0, 0)}
	l := NewRateLimiter(5, time.Minute, 2, clk.Now)
	if ok, _ := l.Allow("k1"); !ok {
		t.Fatalf("k1 rejected")
	}
	if ok, _ := l.Allow("k2"); !ok {
		t.Fatalf("k2 rejected")
	}
	// 表已满(2 个存活键);第三个不同的键必须 fail-closed。
	if ok, _ := l.Allow("k3"); ok {
		t.Fatalf("k3 allowed despite key-pressure — must fail closed to protect memory")
	}
}
