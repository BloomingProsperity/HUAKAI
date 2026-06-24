package alerting

import (
	"context"
	"errors"
	"testing"
)

type fakeLeaderLock struct {
	acquired bool
	err      error
	calls    int
	released bool
}

func (l *fakeLeaderLock) TryAcquire(context.Context) (bool, func(), error) {
	l.calls++
	if l.err != nil {
		return false, nil, l.err
	}
	if !l.acquired {
		return false, nil, nil
	}
	return true, func() { l.released = true }, nil
}

func newLeaderLockScheduler(lock LeaderLock) (*Scheduler, *schedulerMetricSourceStub) {
	metric := &schedulerMetricSourceStub{snapshots: map[int64]map[string]float64{1: {"x": 1}}}
	return NewScheduler(SchedulerConfig{
		Evaluator:    &schedulerEvaluatorStub{called: make(chan struct{}, 10)},
		Store:        &schedulerTenantListerStub{tenants: []int64{1}},
		MetricSource: metric,
		LeaderLock:   lock,
	}), metric
}

// evaluated() is true iff evaluateOnce reached the per-tenant work (the metric
// source was snapshotted).
func evaluated(m *schedulerMetricSourceStub) bool { return len(m.tenants()) > 0 }

func TestScheduler_LeaderLock_LeaderEvaluatesAndReleases(t *testing.T) {
	lock := &fakeLeaderLock{acquired: true}
	s, metric := newLeaderLockScheduler(lock)
	s.evaluateOnce(context.Background())
	if !evaluated(metric) {
		t.Fatal("leader must evaluate the tick")
	}
	if !lock.released {
		t.Fatal("leader must release the lock after evaluating")
	}
}

// MUTATION: removing the `else if !acquired { return }` branch makes a non-leader
// replica evaluate too — this goes red (metric snapshotted when it must not be).
func TestScheduler_LeaderLock_NonLeaderSkips(t *testing.T) {
	lock := &fakeLeaderLock{acquired: false}
	s, metric := newLeaderLockScheduler(lock)
	s.evaluateOnce(context.Background())
	if evaluated(metric) {
		t.Fatal("a non-leader replica must skip evaluation (would emit duplicate alerts)")
	}
	if lock.calls != 1 {
		t.Fatalf("lock must be tried exactly once, got %d", lock.calls)
	}
}

// A lock fault must fail OPEN — evaluate anyway rather than silently dropping
// alerting.
func TestScheduler_LeaderLock_FailsOpenOnError(t *testing.T) {
	lock := &fakeLeaderLock{err: errors.New("lock backend down")}
	s, metric := newLeaderLockScheduler(lock)
	s.evaluateOnce(context.Background())
	if !evaluated(metric) {
		t.Fatal("a lock fault must fail open and still evaluate")
	}
}

// No lock configured = exact current single-replica behavior (always evaluate).
func TestScheduler_LeaderLock_NilEvaluates(t *testing.T) {
	s, metric := newLeaderLockScheduler(nil)
	s.evaluateOnce(context.Background())
	if !evaluated(metric) {
		t.Fatal("nil leader lock must evaluate every tick")
	}
}

// PostgresLeaderLock with a nil pool is a safe sole-leader pass-through.
func TestPostgresLeaderLock_NilPool(t *testing.T) {
	var l *PostgresLeaderLock
	got, release, err := l.TryAcquire(context.Background())
	if err != nil || !got || release == nil {
		t.Fatalf("nil PostgresLeaderLock must act as sole leader, got=%v err=%v", got, err)
	}
	release() // must not panic
}
