package mediatask

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestServiceSubmitValidatesAndPassesEstimateToStore(t *testing.T) {
	// Mutation: ignore default_estimated_cents or let the client provide tenant/user.
	store := &fakeStore{created: Task{ID: 9, TenantID: 7, UserID: 42, RequestID: "req-9", Status: StatusQueued}}
	svc := NewService(store, StaticConfigSource{Config: testConfig()}, StaticProviderRegistry{"http": NewNoopProvider()})

	got, err := svc.Submit(context.Background(), 7, 42, SubmitInput{
		RequestID:   "req-9",
		TaskType:    "image_generation",
		Provider:    "http",
		InputParams: json.RawMessage(`{"prompt":"x"}`),
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got.ID != 9 {
		t.Fatalf("task id=%d want 9", got.ID)
	}
	if len(store.submitCalls) != 1 {
		t.Fatalf("submit calls=%d want 1", len(store.submitCalls))
	}
	call := store.submitCalls[0]
	if call.TenantID != 7 || call.UserID != 42 || call.EstimatedCents != 123 {
		t.Fatalf("submit call=%+v want tenant/user/estimate from service", call)
	}
}

func TestServiceDisabledDoesNotTouchStoreOrProvider(t *testing.T) {
	// Mutation: perform validation or create the task before checking enabled;
	// disabled mode must leave both DB and provider surfaces untouched.
	store := &fakeStore{}
	cfg := testConfig()
	cfg.Enabled = false
	svc := NewService(store, StaticConfigSource{Config: cfg}, StaticProviderRegistry{"http": NewNoopProvider()})

	_, err := svc.Submit(context.Background(), 7, 42, SubmitInput{
		RequestID:   "req-disabled",
		TaskType:    "image_generation",
		Provider:    "http",
		InputParams: json.RawMessage(`{"prompt":"x"}`),
	})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("Submit err=%v want ErrDisabled", err)
	}
	if len(store.submitCalls) != 0 {
		t.Fatalf("disabled submit touched store: %+v", store.submitCalls)
	}
}

func TestServiceStatusAndListAreTenantUserScoped(t *testing.T) {
	// Mutation: drop user_id from Status/List store calls and this test observes
	// a zero user scope instead of the authenticated user.
	store := &fakeStore{
		statusTask: Task{ID: 10, TenantID: 7, UserID: 42, RequestID: "req-10", Status: StatusInProgress},
		listTasks:  []Task{{ID: 10, TenantID: 7, UserID: 42, RequestID: "req-10", Status: StatusInProgress}},
	}
	svc := NewService(store, StaticConfigSource{Config: testConfig()}, StaticProviderRegistry{"http": NewNoopProvider()})

	if _, err := svc.Status(context.Background(), 7, 42, 10); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := svc.List(context.Background(), 7, 42, 20); err != nil {
		t.Fatalf("List: %v", err)
	}
	if store.statusTenant != 7 || store.statusUser != 42 || store.statusID != 10 {
		t.Fatalf("status scope tenant/user/id=%d/%d/%d", store.statusTenant, store.statusUser, store.statusID)
	}
	if store.listTenant != 7 || store.listUser != 42 {
		t.Fatalf("list scope tenant/user=%d/%d", store.listTenant, store.listUser)
	}
}

func TestCanTransitionRejectsTerminalRegression(t *testing.T) {
	if CanTransition(StatusSucceeded, StatusInProgress) {
		t.Fatal("succeeded -> in_progress must be rejected")
	}
	if !CanTransition(StatusQueued, StatusInProgress) || !CanTransition(StatusInProgress, StatusSucceeded) {
		t.Fatal("valid queued/in_progress transitions rejected")
	}
}

func testConfig() Config {
	return Config{
		Enabled:               true,
		PollInterval:          time.Second,
		TaskTimeout:           time.Minute,
		DefaultEstimatedCents: map[string]int64{"image_generation": 123},
		BillingPolicyVersion:  "test-policy",
		RequestClass:          "standard",
	}
}

type fakeStore struct {
	created                            Task
	statusTask                         Task
	listTasks                          []Task
	submitCalls                        []CreateTaskInput
	statusTenant, statusUser, statusID int64
	listTenant, listUser               int64
}

func (s *fakeStore) CreateTask(ctx context.Context, input CreateTaskInput) (Task, bool, error) {
	s.submitCalls = append(s.submitCalls, input)
	out := s.created
	out.TenantID = input.TenantID
	out.UserID = input.UserID
	out.RequestID = input.RequestID
	out.TaskType = input.TaskType
	out.Provider = input.Provider
	out.InputParams = input.InputParams
	out.EstimatedCents = input.EstimatedCents
	return out, false, nil
}

func (s *fakeStore) GetTask(ctx context.Context, tenantID, userID, id int64) (Task, error) {
	s.statusTenant, s.statusUser, s.statusID = tenantID, userID, id
	return s.statusTask, nil
}

func (s *fakeStore) ListTasks(ctx context.Context, tenantID, userID int64, limit int) ([]Task, error) {
	s.listTenant, s.listUser = tenantID, userID
	return append([]Task(nil), s.listTasks...), nil
}

func (s *fakeStore) AcquireLease(context.Context, string, time.Duration, time.Time) (Task, error) {
	return Task{}, ErrNoRunnableTask
}

func (s *fakeStore) MarkProviderSubmitted(context.Context, Task, string, string, time.Time) (Task, error) {
	return Task{}, nil
}

func (s *fakeStore) UpdateProgress(context.Context, Task, string, int, time.Time) error {
	return nil
}

func (s *fakeStore) CompleteSuccess(context.Context, Task, string, PollResult, time.Time) (bool, error) {
	return false, nil
}

func (s *fakeStore) CompleteFailure(context.Context, Task, string, string, time.Time) (bool, error) {
	return false, nil
}

func (s *fakeStore) ExpireTask(context.Context, Task, string, time.Time) (bool, error) {
	return false, nil
}
