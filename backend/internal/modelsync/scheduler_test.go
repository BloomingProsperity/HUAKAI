package modelsync

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
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

func TestSchedulerRecordsFailedAndSuccessfulSyncStatus(t *testing.T) {
	var logs bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(prevLogger)

	syncErr := errors.New("catalog fetch timeout")
	allowSuccess := make(chan struct{})
	svc := &sequencedSchedulerSyncStub{
		responses: []scheduledSyncResponse{
			{err: syncErr},
			{wait: allowSuccess},
		},
	}
	scheduler := NewScheduler(svc, SchedulerConfig{
		Interval:   5 * time.Millisecond,
		RunOnStart: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := scheduler.Start(ctx)
	failed := waitForSchedulerStatus(t, scheduler, func(status SchedulerStatus) bool {
		return status.LastErr == syncErr.Error() && !status.LastRunAt.IsZero()
	})
	if !failed.LastSuccessAt.IsZero() {
		t.Fatalf("failed sync status LastSuccessAt=%v, want zero", failed.LastSuccessAt)
	}

	logged := logs.String()
	for _, want := range []string{
		`"level":"WARN"`,
		`"msg":"model catalog sync failed"`,
		`"reason":"startup"`,
		`"error":"catalog fetch timeout"`,
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("sync failure log missing %s: %s", want, logged)
		}
	}

	close(allowSuccess)
	succeeded := waitForSchedulerStatus(t, scheduler, func(status SchedulerStatus) bool {
		return status.LastErr == "" && !status.LastSuccessAt.IsZero() && svc.calls() >= 2
	})
	stop()
	if succeeded.LastRunAt.Before(failed.LastRunAt) {
		t.Fatalf("successful sync LastRunAt=%v before failed LastRunAt=%v", succeeded.LastRunAt, failed.LastRunAt)
	}
}

func TestSchedulerSkipsSyncWhenAnotherReplicaHoldsLease(t *testing.T) {
	svc := &schedulerSyncStub{}
	lease := &schedulerLeaseStub{acquired: false}
	scheduler := NewScheduler(svc, SchedulerConfig{
		Interval:    time.Hour,
		RunOnStart:  true,
		LeaderLease: lease,
	})
	stop := scheduler.Start(context.Background())
	waitForLeaseCalls(t, lease, 1)
	stop()
	if got := svc.calls(); got != 0 {
		t.Fatalf("lease not acquired but sync ran %d time(s)", got)
	}
	if got := lease.releaseCalls(); got != 0 {
		t.Fatalf("unacquired lease released %d time(s)", got)
	}
}

func TestSchedulerReleasesAcquiredLeaseAfterSync(t *testing.T) {
	svc := &schedulerSyncStub{}
	lease := &schedulerLeaseStub{acquired: true}
	scheduler := NewScheduler(svc, SchedulerConfig{
		Interval:    time.Hour,
		RunOnStart:  true,
		LeaderLease: lease,
	})
	stop := scheduler.Start(context.Background())
	waitForSyncCalls(t, svc, 1)
	waitForLeaseReleases(t, lease, 1)
	stop()
	if got := lease.callsCount(); got != 1 {
		t.Fatalf("leader lease calls=%d want 1", got)
	}
}

type schedulerSyncStub struct {
	mu sync.Mutex
	n  int
}

type schedulerLeaseStub struct {
	mu       sync.Mutex
	acquired bool
	err      error
	calls    int
	releases int
}

func (s *schedulerLeaseStub) TryAcquire(context.Context) (bool, func(), error) {
	s.mu.Lock()
	s.calls++
	acquired := s.acquired
	err := s.err
	s.mu.Unlock()
	if err != nil || !acquired {
		return acquired, nil, err
	}
	return true, func() {
		s.mu.Lock()
		s.releases++
		s.mu.Unlock()
	}, nil
}

func (s *schedulerLeaseStub) callsCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *schedulerLeaseStub) releaseCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.releases
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

func waitForLeaseCalls(t *testing.T, lease *schedulerLeaseStub, want int) {
	t.Helper()
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if lease.callsCount() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("lease calls=%d want at least %d", lease.callsCount(), want)
}

func waitForLeaseReleases(t *testing.T, lease *schedulerLeaseStub, want int) {
	t.Helper()
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if lease.releaseCalls() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("lease releases=%d want at least %d", lease.releaseCalls(), want)
}

type scheduledSyncResponse struct {
	err  error
	wait <-chan struct{}
}

type sequencedSchedulerSyncStub struct {
	mu        sync.Mutex
	n         int
	responses []scheduledSyncResponse
}

func (s *sequencedSchedulerSyncStub) Sync(ctx context.Context, _ string) (SyncResult, error) {
	s.mu.Lock()
	idx := s.n
	s.n++
	var response scheduledSyncResponse
	if idx < len(s.responses) {
		response = s.responses[idx]
	}
	s.mu.Unlock()

	if response.wait != nil {
		select {
		case <-response.wait:
		case <-ctx.Done():
			return SyncResult{}, ctx.Err()
		}
	}
	return SyncResult{}, response.err
}

func (s *sequencedSchedulerSyncStub) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func waitForSchedulerStatus(t *testing.T, scheduler *Scheduler, ok func(SchedulerStatus) bool) SchedulerStatus {
	t.Helper()
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		status := scheduler.Status()
		if ok(status) {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	status := scheduler.Status()
	t.Fatalf("scheduler status=%+v did not satisfy condition", status)
	return SchedulerStatus{}
}
