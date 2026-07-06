package queuewait

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

type selectorStep struct {
	res *pool.SelectionResult
	err error
}

type scriptedSelector struct {
	t       *testing.T
	wantPin int64

	mu    sync.Mutex
	steps []selectorStep
	calls []pool.SelectionRequest
}

func (s *scriptedSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wantPin != 0 && req.PinnedAccountID != s.wantPin {
		s.t.Fatalf("PinnedAccountID=%d want %d", req.PinnedAccountID, s.wantPin)
	}
	s.calls = append(s.calls, req)
	if len(s.steps) == 0 {
		return nil, errors.New("scripted selector exhausted")
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return step.res, step.err
}

func (s *scriptedSelector) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

type manualClock struct {
	now time.Time
}

func (c *manualClock) Now() time.Time { return c.now }

func (c *manualClock) Add(d time.Duration) { c.now = c.now.Add(d) }

func TestExecutor_OverflowDoesNotCallSelector(t *testing.T) {
	tracker := NewTracker()
	clk := &manualClock{now: time.Unix(100, 0)}
	exec := NewExecutor()
	exec.tracker = tracker
	exec.now = clk.Now
	exec.sleep = func(context.Context, time.Duration) error {
		t.Fatal("溢出路径不应 sleep")
		return nil
	}
	exec.jitter = func(d time.Duration) time.Duration { return d }
	selector := &scriptedSelector{t: t, wantPin: 101}

	res := exec.Wait(context.Background(), selector, baseRequest(), basePlan(0, 1000))
	if res.Status != StatusOverflow {
		t.Fatalf("status=%v want overflow", res.Status)
	}
	if selector.callCount() != 0 {
		t.Fatalf("selector calls=%d want 0", selector.callCount())
	}
	if got := tracker.Depth(waitKey()); got != 0 {
		t.Fatalf("depth=%d want 0", got)
	}
}

func TestExecutor_ImmediateSuccessDoesNotSleep(t *testing.T) {
	token := uuid.New()
	selector := &scriptedSelector{
		t:       t,
		wantPin: 101,
		steps: []selectorStep{{
			res: &pool.SelectionResult{AccountID: 101, AcquisitionToken: token},
		}},
	}
	exec := NewExecutor()
	exec.now = func() time.Time { return time.Unix(100, 0) }
	exec.sleep = func(context.Context, time.Duration) error {
		t.Fatal("立即成功不应 sleep")
		return nil
	}
	exec.jitter = func(d time.Duration) time.Duration { return d }

	res := exec.Wait(context.Background(), selector, baseRequest(), basePlan(1, 1000))
	if res.Status != StatusAcquired {
		t.Fatalf("status=%v want acquired", res.Status)
	}
	if res.Selection == nil || res.Selection.AccountID != 101 || res.Selection.AcquisitionToken != token {
		t.Fatalf("selection=%+v want account 101 token %s", res.Selection, token)
	}
}

func TestExecutor_PinsWaitPlanAccountAndSucceedsAfterRetry(t *testing.T) {
	clk := &manualClock{now: time.Unix(100, 0)}
	var sleeps []time.Duration
	exec := NewExecutor()
	exec.now = clk.Now
	exec.sleep = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clk.Add(d)
		return nil
	}
	exec.jitter = func(d time.Duration) time.Duration { return d }
	token := uuid.New()
	wait := &pool.SelectionResult{WaitPlan: basePlan(1, 1000)}
	selector := &scriptedSelector{
		t:       t,
		wantPin: 101,
		steps: []selectorStep{
			{res: wait},
			{res: wait},
			{res: &pool.SelectionResult{AccountID: 101, AcquisitionToken: token}},
		},
	}

	res := exec.Wait(context.Background(), selector, baseRequest(), basePlan(1, 1000))
	if res.Status != StatusAcquired {
		t.Fatalf("status=%v want acquired", res.Status)
	}
	if selector.callCount() != 3 {
		t.Fatalf("selector calls=%d want 3", selector.callCount())
	}
	wantSleeps := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	if !slices.Equal(sleeps, wantSleeps) {
		t.Fatalf("sleeps=%v want %v", sleeps, wantSleeps)
	}
}

func TestExecutor_TimeoutKeepsQueueWaitOutcome(t *testing.T) {
	tracker := NewTracker()
	clk := &manualClock{now: time.Unix(100, 0)}
	var sleeps []time.Duration
	exec := NewExecutor()
	exec.tracker = tracker
	exec.now = clk.Now
	exec.sleep = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clk.Add(d)
		return nil
	}
	exec.jitter = func(d time.Duration) time.Duration { return d }
	wait := &pool.SelectionResult{WaitPlan: basePlan(1, 250)}
	selector := &scriptedSelector{
		t:       t,
		wantPin: 101,
		steps:   []selectorStep{{res: wait}, {res: wait}, {res: wait}},
	}

	res := exec.Wait(context.Background(), selector, baseRequest(), basePlan(1, 250))
	if res.Status != StatusTimeout {
		t.Fatalf("status=%v want timeout", res.Status)
	}
	if selector.callCount() != 3 {
		t.Fatalf("selector calls=%d want 3(含 deadline 终轮 Select)", selector.callCount())
	}
	wantSleeps := []time.Duration{100 * time.Millisecond, 150 * time.Millisecond}
	if !slices.Equal(sleeps, wantSleeps) {
		t.Fatalf("sleeps=%v want %v", sleeps, wantSleeps)
	}
	if got := tracker.Depth(waitKey()); got != 0 {
		t.Fatalf("depth=%d want 0", got)
	}
}

func TestExecutor_FinalClampWindowCanAcquire(t *testing.T) {
	clk := &manualClock{now: time.Unix(100, 0)}
	var sleeps []time.Duration
	exec := NewExecutor()
	exec.now = clk.Now
	exec.sleep = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clk.Add(d)
		return nil
	}
	exec.jitter = func(d time.Duration) time.Duration { return d }
	token := uuid.New()
	wait := &pool.SelectionResult{WaitPlan: basePlan(1, 250)}
	selector := &scriptedSelector{
		t:       t,
		wantPin: 101,
		steps: []selectorStep{
			{res: wait},
			{res: wait},
			{res: &pool.SelectionResult{AccountID: 101, AcquisitionToken: token}},
		},
	}

	res := exec.Wait(context.Background(), selector, baseRequest(), basePlan(1, 250))
	if res.Status != StatusAcquired {
		t.Fatalf("status=%v want acquired; MUTATION:deadline 判定挪回 Select 前会超时", res.Status)
	}
	if res.Selection == nil || res.Selection.AccountID != 101 || res.Selection.AcquisitionToken != token {
		t.Fatalf("selection=%+v want account 101 token %s", res.Selection, token)
	}
	wantSleeps := []time.Duration{100 * time.Millisecond, 150 * time.Millisecond}
	if !slices.Equal(sleeps, wantSleeps) {
		t.Fatalf("sleeps=%v want %v", sleeps, wantSleeps)
	}
	if selector.callCount() != 3 {
		t.Fatalf("selector calls=%d want 3", selector.callCount())
	}
}

func TestExecutor_NonPositiveTimeoutStillSelectsOnce(t *testing.T) {
	clk := &manualClock{now: time.Unix(100, 0)}
	exec := NewExecutor()
	exec.now = clk.Now
	exec.sleep = func(context.Context, time.Duration) error {
		t.Fatal("TimeoutMS<=0 只做一次 Select 后应直接 Timeout")
		return nil
	}
	exec.jitter = func(d time.Duration) time.Duration { return d }
	wait := &pool.SelectionResult{WaitPlan: basePlan(1, 0)}
	selector := &scriptedSelector{
		t:       t,
		wantPin: 101,
		steps:   []selectorStep{{res: wait}},
	}

	res := exec.Wait(context.Background(), selector, baseRequest(), basePlan(1, 0))
	if res.Status != StatusTimeout {
		t.Fatalf("status=%v want timeout", res.Status)
	}
	if selector.callCount() != 1 {
		t.Fatalf("selector calls=%d want 1", selector.callCount())
	}
}

func TestExecutor_SelectorErrorPassesThrough(t *testing.T) {
	tracker := NewTracker()
	boom := errors.New("selector db down")
	exec := NewExecutor()
	exec.tracker = tracker
	exec.now = func() time.Time { return time.Unix(100, 0) }
	exec.sleep = func(context.Context, time.Duration) error {
		t.Fatal("selector error 不应 sleep")
		return nil
	}
	exec.jitter = func(d time.Duration) time.Duration { return d }
	selector := &scriptedSelector{
		t:       t,
		wantPin: 101,
		steps:   []selectorStep{{err: boom}},
	}

	res := exec.Wait(context.Background(), selector, baseRequest(), basePlan(1, 1000))
	if res.Status != StatusSelectorError {
		t.Fatalf("status=%v want selector_error", res.Status)
	}
	if !errors.Is(res.Err, boom) {
		t.Fatalf("err=%v want %v", res.Err, boom)
	}
	if got := tracker.Depth(waitKey()); got != 0 {
		t.Fatalf("depth=%d want 0", got)
	}
}

func TestExecutor_CancelReleasesWaitingSlot(t *testing.T) {
	tracker := NewTracker()
	clk := &manualClock{now: time.Unix(100, 0)}
	ctx, cancel := context.WithCancel(context.Background())
	exec := NewExecutor()
	exec.tracker = tracker
	exec.now = clk.Now
	exec.sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}
	exec.jitter = func(d time.Duration) time.Duration { return d }
	wait := &pool.SelectionResult{WaitPlan: basePlan(1, 1000)}
	selector := &scriptedSelector{
		t:       t,
		wantPin: 101,
		steps:   []selectorStep{{res: wait}},
	}

	res := exec.Wait(ctx, selector, baseRequest(), basePlan(1, 1000))
	if res.Status != StatusCancelled {
		t.Fatalf("status=%v want cancelled", res.Status)
	}
	if got := tracker.Depth(waitKey()); got != 0 {
		t.Fatalf("depth=%d want 0", got)
	}
}

func baseRequest() pool.SelectionRequest {
	return pool.SelectionRequest{
		TenantID:    7,
		PoolGroupID: 42,
		ClaimID:     999,
		AttemptSeq:  1,
	}
}

func basePlan(maxWaiting, timeoutMS int) *pool.WaitPlan {
	return &pool.WaitPlan{
		AccountID:      101,
		MaxConcurrency: 1,
		TimeoutMS:      timeoutMS,
		MaxWaiting:     maxWaiting,
	}
}

func waitKey() Key {
	return Key{TenantID: 7, PoolGroupID: 42, AccountID: 101}
}
