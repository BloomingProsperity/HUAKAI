package mediatask

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestServiceSubmitValidatesAndPassesEstimateToStore(t *testing.T) {
	// 变异:忽略 default_estimated_cents, 或允许客户端自带 tenant/user。
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
	// 变异:在检查 enabled 之前就做校验或创建任务;
	// disabled 模式必须让 DB 与 provider 两侧都保持不被触碰。
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
	// 变异:从 Status/List 的 store 调用中去掉 user_id, 本测试就会观察到
	// 一个为零的 user 范围, 而非已认证的用户。
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

func TestServiceSubmitSetsClaimLeaseCoveringTaskTimeout(t *testing.T) {
	// 真 money 守卫:billing LeaseSweeper 每 30s Abort 任何 lease 过期仍 reserving 的
	// claim。若 media claim lease < TaskTimeout,跑得久的合法媒体任务(视频等)的 claim
	// 会被提前 abort,完成时无法 commit 计费 → 亏钱。断言 service 传给 store 的 claim
	// lease 窗口 >= TaskTimeout。
	// Mutation:把 service 的 cfg.TaskTimeout+claimLeaseGrace 写回 90*time.Second,
	// TaskTimeout=15min 时 90s < 15min,本断言转红。
	store := &fakeStore{created: Task{ID: 1, Status: StatusQueued}}
	cfg := testConfig()
	cfg.TaskTimeout = 15 * time.Minute // 媒体任务可跑数分钟,远超旧的 90s claim lease
	svc := NewService(store, StaticConfigSource{Config: cfg}, StaticProviderRegistry{"http": NewNoopProvider()})

	if _, err := svc.Submit(context.Background(), 7, 42, SubmitInput{
		RequestID: "req-lease", TaskType: "image_generation", Provider: "http",
		InputParams: json.RawMessage(`{"prompt":"x"}`),
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(store.submitCalls) != 1 {
		t.Fatalf("submit calls=%d want 1", len(store.submitCalls))
	}
	got := store.submitCalls[0].ClaimLeaseWindow
	if got < cfg.TaskTimeout {
		t.Fatalf("claim lease 窗口=%s 必须 >= TaskTimeout=%s,否则长任务 claim 会被 LeaseSweeper 提前 abort 亏钱", got, cfg.TaskTimeout)
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
