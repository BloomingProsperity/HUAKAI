package modelsync

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSchedulerRunsStartupAndPeriodicSyncUntilStop(t *testing.T) {
	svc := &schedulerSyncStub{}
	scheduler := NewScheduler(svc, SchedulerConfig{
		Interval:   5 * time.Millisecond,
		RunOnStart: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := scheduler.Start(ctx)
	waitForSyncCalls(t, svc, 2)
	stop()
	callsAtStop := svc.calls()
	time.Sleep(20 * time.Millisecond)
	if got := svc.calls(); got != callsAtStop {
		t.Fatalf("scheduler kept running after Stop: before=%d after=%d", callsAtStop, got)
	}
}

type schedulerSyncStub struct {
	mu sync.Mutex
	n  int
}

func (s *schedulerSyncStub) Sync(context.Context, string) (SyncResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return SyncResult{}, nil
}

func (s *schedulerSyncStub) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func waitForSyncCalls(t *testing.T, svc *schedulerSyncStub, want int) {
	t.Helper()
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if svc.calls() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("sync calls=%d want at least %d", svc.calls(), want)
}
