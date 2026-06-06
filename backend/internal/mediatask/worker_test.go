package mediatask

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestWorkerQueuedTaskSubmitsProviderOnce(t *testing.T) {
	// Mutation: call Poll before Submit for a queued task; provider.submitCalls
	// stays zero and this test fails.
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	store := newWorkerStore(Task{ID: 1, TenantID: 7, UserID: 42, RequestID: "req-1", TaskType: "image_generation", Provider: "http", Status: StatusQueued, CreatedAt: now})
	provider := &workerProvider{submitID: "up-1"}
	worker := NewWorker(store, StaticConfigSource{Config: testConfig()}, StaticProviderRegistry{"http": provider}, WorkerOptions{Owner: "w1", Now: func() time.Time { return now }})

	processed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !processed {
		t.Fatal("RunOnce processed=false want true")
	}
	if provider.submitCalls != 1 || provider.pollCalls != 0 {
		t.Fatalf("provider submit/poll=%d/%d want 1/0", provider.submitCalls, provider.pollCalls)
	}
	if store.task.ProviderTaskID != "up-1" || store.task.Status != StatusInProgress {
		t.Fatalf("task after submit=%+v", store.task)
	}
}

func TestWorkerSuccessSettlesOnceAcrossConcurrentRunOnce(t *testing.T) {
	// Mutation: remove lease_owner / terminal-status guard in CompleteSuccess;
	// both workers settle and completeCalls becomes 2.
	now := time.Date(2026, 6, 6, 12, 5, 0, 0, time.UTC)
	store := newWorkerStore(Task{
		ID: 2, TenantID: 7, UserID: 42, RequestID: "req-2", TaskType: "image_generation",
		Provider: "http", ProviderTaskID: "up-2", Status: StatusInProgress, CreatedAt: now,
	})
	provider := &workerProvider{poll: PollResult{Status: StatusSucceeded, Progress: 100, ActualCents: 77, Result: json.RawMessage(`{"ok":true}`)}}
	workerA := NewWorker(store, StaticConfigSource{Config: testConfig()}, StaticProviderRegistry{"http": provider}, WorkerOptions{Owner: "wA", Now: func() time.Time { return now }})
	workerB := NewWorker(store, StaticConfigSource{Config: testConfig()}, StaticProviderRegistry{"http": provider}, WorkerOptions{Owner: "wB", Now: func() time.Time { return now }})

	var wg sync.WaitGroup
	for _, w := range []*Worker{workerA, workerB} {
		wg.Add(1)
		go func(w *Worker) {
			defer wg.Done()
			_, _ = w.RunOnce(context.Background())
		}(w)
	}
	wg.Wait()

	if store.completeCalls != 1 {
		t.Fatalf("completeCalls=%d want exactly 1", store.completeCalls)
	}
	if store.task.Status != StatusSucceeded || store.task.ActualCents == nil || *store.task.ActualCents != 77 {
		t.Fatalf("task after success=%+v", store.task)
	}
}

func TestWorkerFailureRefundsTerminally(t *testing.T) {
	// Mutation: map provider failed to progress update instead of terminal
	// failure; failureCalls remains zero and held money would not be released.
	now := time.Date(2026, 6, 6, 12, 10, 0, 0, time.UTC)
	store := newWorkerStore(Task{ID: 3, TenantID: 7, UserID: 42, TaskType: "image_generation", Provider: "http", ProviderTaskID: "up-3", Status: StatusInProgress, CreatedAt: now})
	provider := &workerProvider{poll: PollResult{Status: StatusFailed, Progress: 20, ErrorClass: "provider_failed"}}
	worker := NewWorker(store, StaticConfigSource{Config: testConfig()}, StaticProviderRegistry{"http": provider}, WorkerOptions{Owner: "w1", Now: func() time.Time { return now }})

	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.failureCalls != 1 || store.task.Status != StatusFailed {
		t.Fatalf("failureCalls=%d task=%+v", store.failureCalls, store.task)
	}
}

func TestWorkerTimeoutExpiresAndRefunds(t *testing.T) {
	// Mutation: check updated_at instead of created_at for timeout; this stale
	// task is polled instead of expired and expireCalls remains zero.
	base := time.Date(2026, 6, 6, 12, 15, 0, 0, time.UTC)
	cfg := testConfig()
	cfg.TaskTimeout = time.Minute
	store := newWorkerStore(Task{ID: 4, TenantID: 7, UserID: 42, TaskType: "image_generation", Provider: "http", ProviderTaskID: "up-4", Status: StatusInProgress, CreatedAt: base.Add(-2 * time.Minute)})
	provider := &workerProvider{poll: PollResult{Status: StatusInProgress, Progress: 50}}
	worker := NewWorker(store, StaticConfigSource{Config: cfg}, StaticProviderRegistry{"http": provider}, WorkerOptions{Owner: "w1", Now: func() time.Time { return base }})

	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.expireCalls != 1 || provider.pollCalls != 0 || store.task.Status != StatusExpired {
		t.Fatalf("expireCalls=%d pollCalls=%d task=%+v", store.expireCalls, provider.pollCalls, store.task)
	}
}

type workerProvider struct {
	submitID    string
	poll        PollResult
	submitCalls int
	pollCalls   int
}

func (p *workerProvider) Submit(context.Context, SubmitReq) (string, error) {
	p.submitCalls++
	if p.submitID == "" {
		return "up-default", nil
	}
	return p.submitID, nil
}

func (p *workerProvider) Poll(context.Context, string) (PollResult, error) {
	p.pollCalls++
	return p.poll, nil
}

type workerStore struct {
	mu            sync.Mutex
	task          Task
	completeCalls int
	failureCalls  int
	expireCalls   int
}

func newWorkerStore(task Task) *workerStore {
	return &workerStore{task: task}
}

func (s *workerStore) CreateTask(context.Context, CreateTaskInput) (Task, bool, error) {
	return Task{}, false, nil
}

func (s *workerStore) GetTask(context.Context, int64, int64, int64) (Task, error) {
	return Task{}, nil
}

func (s *workerStore) ListTasks(context.Context, int64, int64, int) ([]Task, error) {
	return nil, nil
}

func (s *workerStore) AcquireLease(_ context.Context, owner string, ttl time.Duration, now time.Time) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if IsTerminal(s.task.Status) || s.task.LeaseOwner != "" {
		return Task{}, ErrNoRunnableTask
	}
	s.task.LeaseOwner = owner
	expires := now.Add(ttl)
	s.task.LeaseExpiresAt = &expires
	return s.task, nil
}

func (s *workerStore) MarkProviderSubmitted(_ context.Context, task Task, owner, providerTaskID string, now time.Time) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task.ID != task.ID || s.task.LeaseOwner != owner || s.task.Status != StatusQueued {
		return s.task, nil
	}
	s.task.ProviderTaskID = providerTaskID
	s.task.Status = StatusInProgress
	s.task.Progress = 1
	s.task.LeaseOwner = ""
	s.task.LeaseExpiresAt = nil
	s.task.UpdatedAt = now
	return s.task, nil
}

func (s *workerStore) UpdateProgress(_ context.Context, task Task, owner string, progress int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task.ID == task.ID && s.task.LeaseOwner == owner && s.task.Status == StatusInProgress {
		s.task.Progress = progress
		s.task.LeaseOwner = ""
		s.task.LeaseExpiresAt = nil
		s.task.UpdatedAt = now
	}
	return nil
}

func (s *workerStore) CompleteSuccess(_ context.Context, task Task, owner string, result PollResult, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task.ID != task.ID || s.task.LeaseOwner != owner || IsTerminal(s.task.Status) {
		return false, nil
	}
	s.completeCalls++
	s.task.Status = StatusSucceeded
	s.task.Progress = 100
	s.task.Result = result.Result
	s.task.ActualCents = &result.ActualCents
	s.task.LeaseOwner = ""
	s.task.LeaseExpiresAt = nil
	s.task.FinishedAt = &now
	return true, nil
}

func (s *workerStore) CompleteFailure(_ context.Context, task Task, owner, errorClass string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task.ID != task.ID || s.task.LeaseOwner != owner || IsTerminal(s.task.Status) {
		return false, nil
	}
	s.failureCalls++
	s.task.Status = StatusFailed
	s.task.ErrorClass = errorClass
	s.task.LeaseOwner = ""
	s.task.LeaseExpiresAt = nil
	s.task.FinishedAt = &now
	return true, nil
}

func (s *workerStore) ExpireTask(_ context.Context, task Task, owner string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task.ID != task.ID || s.task.LeaseOwner != owner || IsTerminal(s.task.Status) {
		return false, nil
	}
	s.expireCalls++
	s.task.Status = StatusExpired
	s.task.ErrorClass = "timeout"
	s.task.LeaseOwner = ""
	s.task.LeaseExpiresAt = nil
	s.task.FinishedAt = &now
	return true, nil
}
