package gatewayhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pool/queuewait"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

type queueWaitHTTPStep struct {
	res *pool.SelectionResult
	err error
}

type queueWaitHTTPSelector struct {
	t       *testing.T
	wantPin int64

	mu    sync.Mutex
	steps []queueWaitHTTPStep
	calls []pool.SelectionRequest

	onSelect func(call int, req pool.SelectionRequest)
}

func (s *queueWaitHTTPSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) > 0 && s.wantPin != 0 && req.PinnedAccountID != s.wantPin {
		s.t.Fatalf("等待轮次 PinnedAccountID=%d want %d", req.PinnedAccountID, s.wantPin)
	}
	call := len(s.calls) + 1
	s.calls = append(s.calls, req)
	if s.onSelect != nil {
		s.onSelect(call, req)
	}
	if len(s.steps) == 0 {
		s.t.Fatal("selector 调用次数超过脚本")
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return step.res, step.err
}

func (s *queueWaitHTTPSelector) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func TestHandler_QueueWaitSuccessDoesNotAbortClaim(t *testing.T) {
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	dispatcher := &mockCanonicalBufferedDispatcher{}
	token := uuid.New()
	selector := &queueWaitHTTPSelector{
		t:       t,
		wantPin: 1,
		steps: []queueWaitHTTPStep{
			{res: &pool.SelectionResult{WaitPlan: queueWaitHTTPPlan(1, 1000)}},
			{res: &pool.SelectionResult{AccountID: 1, AcquisitionToken: token}},
		},
	}
	d := clientAdapterDeps(t)
	d.Selector = selector
	d.ClaimGate = &recordingClaimGate{claimID: 99021}
	d.Settler = settler
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandler(t, d, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200", rec.Code, rec.Body.String())
	}
	if selector.callCount() != 2 {
		t.Fatalf("selector calls=%d want 2", selector.callCount())
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("abort calls=%d want 0", len(settler.aborts))
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher calls=%d want 1", dispatcher.calls)
	}
}

func TestHandler_QueueWaitCancelAbortsDetachedAndReleasesTracker(t *testing.T) {
	enableHCSFDispatchForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	waiter := queuewait.NewExecutor()
	selector := &queueWaitHTTPSelector{
		t:       t,
		wantPin: 1,
		steps: []queueWaitHTTPStep{
			{res: &pool.SelectionResult{WaitPlan: queueWaitHTTPPlan(1, 5000)}},
			{res: &pool.SelectionResult{WaitPlan: queueWaitHTTPPlan(1, 5000)}},
		},
		onSelect: func(call int, _ pool.SelectionRequest) {
			if call == 2 {
				cancel()
			}
		},
	}
	settler := &abortCtxSpySettler{}
	d := minimalDeps()
	d.Selector = selector
	d.Settler = settler
	d.QueueWaiter = waiter

	h := NewChatCompletionsHandler(d)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)).
		WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s; want 429", rec.Code, rec.Body.String())
	}
	if settler.calls != 1 {
		t.Fatalf("abort calls=%d want 1", settler.calls)
	}
	if settler.lastCtxErr != nil {
		t.Fatalf("abort ctx err=%v want nil detached ctx", settler.lastCtxErr)
	}

	token := uuid.New()
	successSelector := &queueWaitHTTPSelector{
		t:       t,
		wantPin: 1,
		steps: []queueWaitHTTPStep{
			{res: &pool.SelectionResult{WaitPlan: queueWaitHTTPPlan(1, 1000)}},
			{res: &pool.SelectionResult{AccountID: 1, AcquisitionToken: token}},
		},
	}
	successSettler := &recordingSettler{}
	dispatcher := &mockCanonicalBufferedDispatcher{}
	successDeps := clientAdapterDeps(t)
	successDeps.Selector = successSelector
	successDeps.ClaimGate = &recordingClaimGate{claimID: 99022}
	successDeps.Settler = successSettler
	successDeps.CanonicalDispatcher = dispatcher
	successDeps.QueueWaiter = waiter

	successRec := invokeHandler(t, successDeps, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if successRec.Code != http.StatusOK {
		t.Fatalf("释放后复用 waiter status=%d body=%s; want 200", successRec.Code, successRec.Body.String())
	}
	if successSelector.callCount() != 2 {
		t.Fatalf("释放后 selector calls=%d want 2", successSelector.callCount())
	}
	if len(successSettler.aborts) != 0 {
		t.Fatalf("释放后成功路径 abort calls=%d want 0", len(successSettler.aborts))
	}
}

func TestHandler_QueueWaitTimeoutAbortsClaim(t *testing.T) {
	settler := &stubSettler{}
	d := minimalDeps()
	d.Selector = waitPlanSelector{}
	d.Settler = settler
	d.QueueWaiter = fixedQueueWaiter{result: queuewait.Result{Status: queuewait.StatusTimeout}}

	rec := invokeHandler(t, d, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s; want 429", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "3" {
		t.Fatalf("Retry-After=%q want 3", got)
	}
	if settler.abortCalls != 1 || settler.lastAbortReason != "queue_wait" {
		t.Fatalf("abort calls/reason=%d/%q; want 1/queue_wait", settler.abortCalls, settler.lastAbortReason)
	}
}

func TestHandler_QueueWaitBudgetShrinksAcrossAttempts(t *testing.T) {
	clock := &queueWaitFakeClock{now: time.Unix(1000, 0)}
	waiter := &recordingQueueWaiter{
		t:        t,
		clock:    clock,
		results:  []queuewait.Result{{Status: queuewait.StatusTimeout}, {Status: queuewait.StatusTimeout}},
		advances: []time.Duration{1200 * time.Millisecond, 0},
	}
	selector := &queueWaitHTTPSelector{
		t: t,
		steps: []queueWaitHTTPStep{
			{res: &pool.SelectionResult{WaitPlan: queueWaitHTTPPlan(1, 2500)}},
			{res: &pool.SelectionResult{WaitPlan: queueWaitHTTPPlan(1, 2500)}},
		},
	}
	d := minimalDeps()
	d.Router = stubRouter{plan: queueWaitRetryPlan()}
	d.Selector = selector
	d.QueueWaiter = waiter
	d.QueueWaitNow = clock.Now

	rec := invokeHandler(t, d, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s; want 429", rec.Code, rec.Body.String())
	}
	got := waiter.timeoutMS()
	want := []int{2500, 1300}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("waiter TimeoutMS=%v want %v; MUTATION:删等待累计会第二次仍为全额", got, want)
	}
	if got := rec.Header().Get("Retry-After"); got != "3" {
		t.Fatalf("Retry-After=%q want 3(按原始 plan.TimeoutMS)", got)
	}
	if selector.callCount() != 2 {
		t.Fatalf("selector calls=%d want 2", selector.callCount())
	}
}

func TestHandler_QueueWaitBudgetExhaustedSkipsSecondWaiterCall(t *testing.T) {
	clock := &queueWaitFakeClock{now: time.Unix(1000, 0)}
	waiter := &recordingQueueWaiter{
		t:        t,
		clock:    clock,
		results:  []queuewait.Result{{Status: queuewait.StatusTimeout}},
		advances: []time.Duration{2500 * time.Millisecond},
	}
	selector := &queueWaitHTTPSelector{
		t: t,
		steps: []queueWaitHTTPStep{
			{res: &pool.SelectionResult{WaitPlan: queueWaitHTTPPlan(1, 2500)}},
			{res: &pool.SelectionResult{WaitPlan: queueWaitHTTPPlan(1, 2500)}},
		},
	}
	settler := &stubSettler{}
	d := minimalDeps()
	d.Router = stubRouter{plan: queueWaitRetryPlan()}
	d.Selector = selector
	d.Settler = settler
	d.QueueWaiter = waiter
	d.QueueWaitNow = clock.Now

	rec := invokeHandler(t, d, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s; want 429", rec.Code, rec.Body.String())
	}
	if got := waiter.callCount(); got != 1 {
		t.Fatalf("waiter calls=%d want 1; 预算耗尽后的第二个 WaitPlan 不应再等待", got)
	}
	if selector.callCount() != 2 {
		t.Fatalf("selector calls=%d want 2(仍保留换池 fallback 的选号机会)", selector.callCount())
	}
	if settler.abortCalls != 2 || settler.lastAbortReason != "queue_wait" {
		t.Fatalf("abort calls/reason=%d/%q want 2/queue_wait", settler.abortCalls, settler.lastAbortReason)
	}
	if got := rec.Header().Get("Retry-After"); got != "3" {
		t.Fatalf("Retry-After=%q want 3(预算缩水不改变客户端退避提示)", got)
	}
}

func TestHandler_QueueWaitCancelledIsTerminal(t *testing.T) {
	claimGate := &countingQueueWaitClaimGate{}
	settler := &stubSettler{}
	waiter := &recordingQueueWaiter{
		t:       t,
		results: []queuewait.Result{{Status: queuewait.StatusCancelled, Err: context.Canceled}},
	}
	selector := &queueWaitHTTPSelector{
		t: t,
		steps: []queueWaitHTTPStep{
			{res: &pool.SelectionResult{WaitPlan: queueWaitHTTPPlan(1, 5000)}},
		},
	}
	d := minimalDeps()
	d.Router = stubRouter{plan: queueWaitRetryPlan()}
	d.ClaimGate = claimGate
	d.Selector = selector
	d.Settler = settler
	d.QueueWaiter = waiter

	rec := invokeHandler(t, d, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s; want 429", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"queue_wait"`) {
		t.Fatalf("body=%s; want queue_wait code", rec.Body.String())
	}
	if claimGate.callCount() != 1 {
		t.Fatalf("reserve calls=%d want 1; MUTATION:cancelled retryable 会出现第二次 reserve", claimGate.callCount())
	}
	if selector.callCount() != 1 {
		t.Fatalf("selector calls=%d want 1", selector.callCount())
	}
	if waiter.callCount() != 1 {
		t.Fatalf("waiter calls=%d want 1", waiter.callCount())
	}
	if settler.abortCalls != 1 || settler.lastAbortReason != "queue_wait_cancelled" {
		t.Fatalf("abort calls/reason=%d/%q want 1/queue_wait_cancelled", settler.abortCalls, settler.lastAbortReason)
	}
}

type fixedQueueWaiter struct {
	result queuewait.Result
}

func (w fixedQueueWaiter) Wait(context.Context, pool.Selector, pool.SelectionRequest, *pool.WaitPlan) queuewait.Result {
	return w.result
}

type queueWaitFakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *queueWaitFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *queueWaitFakeClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type recordingQueueWaiter struct {
	t        *testing.T
	clock    *queueWaitFakeClock
	results  []queuewait.Result
	advances []time.Duration

	mu    sync.Mutex
	plans []pool.WaitPlan
}

func (w *recordingQueueWaiter) Wait(_ context.Context, _ pool.Selector, _ pool.SelectionRequest, plan *pool.WaitPlan) queuewait.Result {
	w.mu.Lock()
	idx := len(w.plans)
	if plan != nil {
		w.plans = append(w.plans, *plan)
	} else {
		w.plans = append(w.plans, pool.WaitPlan{})
	}
	if idx >= len(w.results) {
		w.mu.Unlock()
		w.t.Fatalf("queue waiter 调用次数超过脚本: call=%d", idx+1)
	}
	result := w.results[idx]
	var advance time.Duration
	if idx < len(w.advances) {
		advance = w.advances[idx]
	}
	w.mu.Unlock()
	if advance > 0 && w.clock != nil {
		w.clock.Add(advance)
	}
	return result
}

func (w *recordingQueueWaiter) timeoutMS() []int {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]int, 0, len(w.plans))
	for _, plan := range w.plans {
		out = append(out, plan.TimeoutMS)
	}
	return out
}

func (w *recordingQueueWaiter) callCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.plans)
}

type countingQueueWaitClaimGate struct {
	mu    sync.Mutex
	calls int
}

func (g *countingQueueWaitClaimGate) Reserve(_ context.Context, _ billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	return &billing.ReserveResult{ClaimID: int64(99000 + g.calls)}, nil
}

func (g *countingQueueWaitClaimGate) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func queueWaitRetryPlan() router.RoutePlan {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{
			{PoolGroupID: 42},
			{PoolGroupID: 43},
		},
		RetryableEndClasses: []string{string(gateway.UpstreamRateLimit)},
		SnapshotVersion:     "registry:7:1;router:queue-wait-test",
	}
}

func queueWaitHTTPPlan(maxWaiting, timeoutMS int) *pool.WaitPlan {
	return &pool.WaitPlan{
		AccountID:      1,
		MaxConcurrency: 1,
		TimeoutMS:      timeoutMS,
		MaxWaiting:     maxWaiting,
	}
}
