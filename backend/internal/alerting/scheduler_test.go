package alerting

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestScheduler_EvaluatesEnabledRulesOnTick(t *testing.T) {
	// MUTATION: leave the tick branch empty or skip EvaluateRules; this never records the per-tenant evaluations.
	ticker := newFakeSchedulerTicker()
	lister := &schedulerTenantListerStub{tenants: []int64{101, 202}}
	source := &schedulerMetricSourceStub{snapshots: map[int64]map[string]float64{
		101: {"huakai_billing_resolver_db_fail_total": 3},
		202: {"huakai_billing_resolver_db_fail_total": 7},
	}}
	evaluator := newSchedulerEvaluatorStub()
	scheduler := NewScheduler(SchedulerConfig{
		Evaluator:    evaluator,
		Store:        lister,
		MetricSource: source,
		Interval:     time.Minute,
		NewTicker: func(interval time.Duration) SchedulerTicker {
			if interval != time.Minute {
				t.Fatalf("ticker interval=%s want 1m", interval)
			}
			return ticker
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- scheduler.Run(ctx) }()

	ticker.tick()
	evaluator.waitCalls(t, 2)
	cancel()
	waitSchedulerRunReturned(t, errCh)

	got := evaluator.calls()
	want := []schedulerEvaluateCall{
		{tenantID: 101, snapshot: map[string]float64{"huakai_billing_resolver_db_fail_total": 3}},
		{tenantID: 202, snapshot: map[string]float64{"huakai_billing_resolver_db_fail_total": 7}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EvaluateRules calls=%+v want %+v", got, want)
	}
}

func TestScheduler_SkipsTenantsWithoutEnabledRules(t *testing.T) {
	// MUTATION: evaluate a fixed/all-tenant list instead of ListTenantsWithEnabledRules; tenant 404 receives wasted evaluation.
	ticker := newFakeSchedulerTicker()
	lister := &schedulerTenantListerStub{tenants: []int64{303}}
	source := &schedulerMetricSourceStub{snapshots: map[int64]map[string]float64{
		303: {"huakai_group_policy_failopen_total": 1},
		404: {"huakai_group_policy_failopen_total": 99},
	}}
	evaluator := newSchedulerEvaluatorStub()
	scheduler := NewScheduler(SchedulerConfig{
		Evaluator:    evaluator,
		Store:        lister,
		MetricSource: source,
		NewTicker:    func(time.Duration) SchedulerTicker { return ticker },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- scheduler.Run(ctx) }()

	ticker.tick()
	evaluator.waitCalls(t, 1)
	cancel()
	waitSchedulerRunReturned(t, errCh)

	if got := source.tenants(); !reflect.DeepEqual(got, []int64{303}) {
		t.Fatalf("metric source tenants=%v want only enabled-rule tenant 303", got)
	}
	if got := evaluator.calls(); len(got) != 1 || got[0].tenantID != 303 {
		t.Fatalf("EvaluateRules calls=%+v want only tenant 303", got)
	}
}

func TestScheduler_UsesSourceAwareEvaluatorWhenAvailable(t *testing.T) {
	// MUTATION: always pre-snapshot and call EvaluateRules; source-aware evaluators cannot pass rule filters through to the metric source.
	ticker := newFakeSchedulerTicker()
	lister := &schedulerTenantListerStub{tenants: []int64{303}}
	source := &schedulerMetricSourceStub{snapshots: map[int64]map[string]float64{
		303: {"usage.request_count": 95},
	}}
	evaluator := newSchedulerSourceAwareEvaluatorStub()
	scheduler := NewScheduler(SchedulerConfig{
		Evaluator:    evaluator,
		Store:        lister,
		MetricSource: source,
		NewTicker:    func(time.Duration) SchedulerTicker { return ticker },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- scheduler.Run(ctx) }()

	ticker.tick()
	evaluator.waitCalls(t, 1)
	cancel()
	waitSchedulerRunReturned(t, errCh)

	if got := source.tenants(); len(got) != 0 {
		t.Fatalf("global metric source tenants=%v want none before source-aware evaluator decides dimensions", got)
	}
	if got := evaluator.sourceCalls(); len(got) != 1 || got[0] != 303 {
		t.Fatalf("source-aware calls=%+v want tenant 303", got)
	}
}

func TestScheduler_StopsOnContextCancel(t *testing.T) {
	// MUTATION: remove the ctx.Done select case; Run blocks forever and this test times out.
	ticker := newFakeSchedulerTicker()
	tickerCreated := make(chan struct{})
	scheduler := NewScheduler(SchedulerConfig{
		Evaluator:    newSchedulerEvaluatorStub(),
		Store:        &schedulerTenantListerStub{},
		MetricSource: &schedulerMetricSourceStub{},
		NewTicker: func(time.Duration) SchedulerTicker {
			close(tickerCreated)
			return ticker
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- scheduler.Run(ctx) }()
	<-tickerCreated

	cancel()
	waitSchedulerRunReturned(t, errCh)
	select {
	case <-ticker.stopped:
	default:
		t.Fatal("scheduler did not stop ticker on context cancellation")
	}
}

func TestMemoryStore_ListTenantsWithEnabledRulesSkipsDisabledAndSorts(t *testing.T) {
	// MUTATION: list all tenants with any rule or return unsorted duplicates; disabled-only tenants leak into scheduler work.
	ctx := context.Background()
	disabled := false
	store := NewMemoryStore()
	svc := NewService(store)
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      8,
		Name:          "enabled tenant eight",
		Metric:        "huakai_group_policy_failopen_total",
		Comparator:    ComparatorGTE,
		Threshold:     1,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
	})
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      7,
		Name:          "disabled tenant seven",
		Metric:        "huakai_group_policy_failopen_total",
		Comparator:    ComparatorGTE,
		Threshold:     1,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
		Enabled:       &disabled,
	})
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID:      8,
		Name:          "second enabled tenant eight",
		Metric:        "huakai_billing_resolver_db_fail_total",
		Comparator:    ComparatorGTE,
		Threshold:     1,
		Severity:      SeverityWarning,
		WindowSeconds: 60,
	})

	tenants, err := store.ListTenantsWithEnabledRules(ctx)
	if err != nil {
		t.Fatalf("ListTenantsWithEnabledRules: %v", err)
	}
	if !reflect.DeepEqual(tenants, []int64{8}) {
		t.Fatalf("tenants=%v want only enabled tenant 8 once", tenants)
	}
}

type fakeSchedulerTicker struct {
	ch       chan time.Time
	stopped  chan struct{}
	stopOnce sync.Once
}

func newFakeSchedulerTicker() *fakeSchedulerTicker {
	return &fakeSchedulerTicker{
		ch:      make(chan time.Time, 1),
		stopped: make(chan struct{}),
	}
}

func (t *fakeSchedulerTicker) C() <-chan time.Time {
	return t.ch
}

func (t *fakeSchedulerTicker) Stop() {
	t.stopOnce.Do(func() {
		close(t.stopped)
	})
}

func (t *fakeSchedulerTicker) tick() {
	t.ch <- time.Now().UTC()
}

type schedulerTenantListerStub struct {
	tenants []int64
}

func (s *schedulerTenantListerStub) ListTenantsWithEnabledRules(context.Context) ([]int64, error) {
	return append([]int64(nil), s.tenants...), nil
}

type schedulerMetricSourceStub struct {
	mu        sync.Mutex
	snapshots map[int64]map[string]float64
	seen      []int64
}

func (s *schedulerMetricSourceStub) Snapshot(_ context.Context, tenantID int64) (map[string]float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, tenantID)
	return cloneMetricSnapshot(s.snapshots[tenantID]), nil
}

func (s *schedulerMetricSourceStub) tenants() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.seen...)
}

type schedulerEvaluateCall struct {
	tenantID int64
	snapshot map[string]float64
}

type schedulerEvaluatorStub struct {
	mu     sync.Mutex
	seen   []schedulerEvaluateCall
	called chan struct{}
}

type schedulerSourceAwareEvaluatorStub struct {
	*schedulerEvaluatorStub
	sourceSeen []int64
}

func newSchedulerSourceAwareEvaluatorStub() *schedulerSourceAwareEvaluatorStub {
	return &schedulerSourceAwareEvaluatorStub{schedulerEvaluatorStub: newSchedulerEvaluatorStub()}
}

func (s *schedulerSourceAwareEvaluatorStub) EvaluateRulesFromSource(_ context.Context, tenantID int64, _ MetricSource) error {
	s.mu.Lock()
	s.sourceSeen = append(s.sourceSeen, tenantID)
	s.mu.Unlock()
	s.called <- struct{}{}
	return nil
}

func (s *schedulerSourceAwareEvaluatorStub) sourceCalls() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.sourceSeen...)
}

func newSchedulerEvaluatorStub() *schedulerEvaluatorStub {
	return &schedulerEvaluatorStub{called: make(chan struct{}, 10)}
}

func (s *schedulerEvaluatorStub) EvaluateRules(_ context.Context, tenantID int64, snapshot map[string]float64) error {
	s.mu.Lock()
	s.seen = append(s.seen, schedulerEvaluateCall{tenantID: tenantID, snapshot: cloneMetricSnapshot(snapshot)})
	s.mu.Unlock()
	s.called <- struct{}{}
	return nil
}

func (s *schedulerEvaluatorStub) waitCalls(t *testing.T, want int) {
	t.Helper()
	for i := 0; i < want; i++ {
		select {
		case <-s.called:
		case <-time.After(250 * time.Millisecond):
			t.Fatalf("EvaluateRules calls=%d want at least %d", len(s.calls()), want)
		}
	}
}

func (s *schedulerEvaluatorStub) calls() []schedulerEvaluateCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]schedulerEvaluateCall, 0, len(s.seen))
	for _, call := range s.seen {
		out = append(out, schedulerEvaluateCall{
			tenantID: call.tenantID,
			snapshot: cloneMetricSnapshot(call.snapshot),
		})
	}
	return out
}

func waitSchedulerRunReturned(t *testing.T, errCh <-chan error) {
	t.Helper()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("scheduler Run did not return after context cancellation")
	}
}
