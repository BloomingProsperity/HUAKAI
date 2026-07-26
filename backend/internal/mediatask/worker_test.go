package mediatask

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestWorkerQueuedTaskSubmitsProviderOnce(t *testing.T) {
	// 变异:对一个 queued 任务在 Submit 之前先 Poll;provider.submitCalls
	// 会保持为零,本测试失败。
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

func TestWorkerPersistsSubmittingBeforeCallingProvider(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 1, 0, 0, time.UTC)
	store := newWorkerStore(Task{
		ID: 101, TenantID: 7, UserID: 42, RequestID: "req-write-ahead",
		TaskType: "image_generation", Provider: "http", Status: StatusQueued, CreatedAt: now,
	})
	var statusSeenByProvider Status
	provider := &workerProvider{
		submitID: "up-write-ahead",
		onSubmit: func(SubmitReq) {
			store.mu.Lock()
			defer store.mu.Unlock()
			statusSeenByProvider = store.task.Status
		},
	}
	worker := NewWorker(
		store,
		StaticConfigSource{Config: testConfig()},
		StaticProviderRegistry{"http": provider},
		WorkerOptions{Owner: "write-ahead-worker", Now: func() time.Time { return now }},
	)

	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	if statusSeenByProvider != StatusSubmitting || store.submittingCalls != 1 {
		t.Fatalf("上游调用看到的状态=%q，写前次数=%d；必须先耐久写入 submitting",
			statusSeenByProvider, store.submittingCalls)
	}
	if store.task.Status != StatusInProgress || store.task.ProviderTaskID != "up-write-ahead" {
		t.Fatalf("提交成功后任务未进入 in_progress: %+v", store.task)
	}
}

func TestWorkerUnknownSubmitOutcomeNeverRetriesOrRefunds(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 2, 0, 0, time.UTC)
	store := newWorkerStore(Task{
		ID: 102, TenantID: 7, UserID: 42, RequestID: "req-unknown",
		TaskType: "image_generation", Provider: "http", Status: StatusQueued, CreatedAt: now,
	})
	provider := &workerProvider{submitErr: errors.New("连接写出后被重置")}
	worker := NewWorker(
		store,
		StaticConfigSource{Config: testConfig()},
		StaticProviderRegistry{"http": provider},
		WorkerOptions{Owner: "unknown-worker", Now: func() time.Time { return now }},
	)

	processed, err := worker.RunOnce(context.Background())
	if !processed || err == nil {
		t.Fatalf("首次 RunOnce processed=%v err=%v", processed, err)
	}
	if store.task.Status != StatusSubmissionUnknown || store.unknownCalls != 1 {
		t.Fatalf("未知提交结果未进入人工恢复态: %+v unknown_calls=%d", store.task, store.unknownCalls)
	}
	if store.failureCalls != 0 || store.deferCalls != 0 {
		t.Fatalf("未知提交结果不得退款或重新排队: failure=%d defer=%d",
			store.failureCalls, store.deferCalls)
	}
	if store.lastIdempotencyKey != DeriveIdempotencyKey(store.task.ID, store.task.RequestID) {
		t.Fatalf("恢复记录幂等键=%q", store.lastIdempotencyKey)
	}

	processed, err = worker.RunOnce(context.Background())
	if err != nil || processed {
		t.Fatalf("未知态第二轮不应再获租约: processed=%v err=%v", processed, err)
	}
	if provider.submitCalls != 1 {
		t.Fatalf("未知态被重复提交到上游，submit_calls=%d", provider.submitCalls)
	}
}

func TestWorkerEmptyProviderTaskIDBecomesUnknownBeforePersistence(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 2, 30, 0, time.UTC)
	store := newWorkerStore(Task{
		ID: 1021, TenantID: 7, UserID: 42, RequestID: "req-empty-provider-id",
		TaskType: "image_generation", Provider: "http", Status: StatusQueued, CreatedAt: now,
	})
	provider := &workerProvider{returnEmptySubmitID: true}
	worker := NewWorker(
		store,
		StaticConfigSource{Config: testConfig()},
		StaticProviderRegistry{"http": provider},
		WorkerOptions{Owner: "empty-id-worker", Now: func() time.Time { return now }},
	)

	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	if store.task.Status != StatusSubmissionUnknown ||
		store.task.ProviderTaskID != "" ||
		store.task.ErrorClass != "provider_submit_response_invalid" ||
		store.unknownCalls != 1 {
		t.Fatalf("空上游任务号必须直接进入未知恢复态，task=%+v unknown_calls=%d",
			store.task, store.unknownCalls)
	}
	if store.failureCalls != 0 || store.deferCalls != 0 {
		t.Fatalf("空上游任务号不得退款或重提: failure=%d defer=%d",
			store.failureCalls, store.deferCalls)
	}
}

func TestWorkerRecoveredSubmittingBecomesUnknownWithoutCallingProvider(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 3, 0, 0, time.UTC)
	store := newWorkerStore(Task{
		ID: 103, TenantID: 7, UserID: 42, RequestID: "req-crashed-submit",
		TaskType: "image_generation", Provider: "http", Status: StatusSubmitting, CreatedAt: now,
	})
	provider := &workerProvider{submitID: "must-not-be-created"}
	worker := NewWorker(
		store,
		StaticConfigSource{Config: testConfig()},
		StaticProviderRegistry{"http": provider},
		WorkerOptions{Owner: "recovery-worker", Now: func() time.Time { return now }},
	)

	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	if provider.submitCalls != 0 || store.task.Status != StatusSubmissionUnknown {
		t.Fatalf("进程中断后的 submitting 被错误重提: calls=%d task=%+v",
			provider.submitCalls, store.task)
	}
}

func TestWorkerDeterministicSubmitRejectionFailsWithoutUnknownState(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 4, 0, 0, time.UTC)
	store := newWorkerStore(Task{
		ID: 104, TenantID: 7, UserID: 42, RequestID: "req-rejected",
		TaskType: "image_generation", Provider: "http", Status: StatusQueued, CreatedAt: now,
	})
	provider := &workerProvider{
		submitErr: terminalProviderError("provider_submit_rejected", ErrInvalidInput),
	}
	worker := NewWorker(
		store,
		StaticConfigSource{Config: testConfig()},
		StaticProviderRegistry{"http": provider},
		WorkerOptions{Owner: "reject-worker", Now: func() time.Time { return now }},
	)

	processed, err := worker.RunOnce(context.Background())
	if !processed || err == nil {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	if store.failureCalls != 1 || store.unknownCalls != 0 ||
		store.task.Status != StatusFailed || store.task.ErrorClass != "provider_submit_rejected" {
		t.Fatalf("明确拒绝必须终态失败并释放，而不是进入未知态: %+v", store.task)
	}
}

func TestWorkerSubmissionReleaseUsesFailureSettlementWithoutProviderCall(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 5, 0, 0, time.UTC)
	store := newWorkerStore(Task{
		ID: 105, TenantID: 7, UserID: 42, RequestID: "req-release",
		TaskType: "image_generation", Provider: "http",
		Status: StatusSubmissionReleasing, CreatedAt: now,
	})
	provider := &workerProvider{submitID: "must-not-submit"}
	worker := NewWorker(
		store,
		StaticConfigSource{Config: testConfig()},
		StaticProviderRegistry{"http": provider},
		WorkerOptions{Owner: "release-worker", Now: func() time.Time { return now }},
	)

	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	if provider.submitCalls != 0 || provider.pollCalls != 0 ||
		store.failureCalls != 1 || store.task.Status != StatusFailed {
		t.Fatalf("人工释放没有只走统一失败结算: submit=%d poll=%d task=%+v",
			provider.submitCalls, provider.pollCalls, store.task)
	}
}

func TestWorkerSettlementPendingResumesSettlementBeforeTimeoutOrProviderCall(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 6, 0, 0, time.UTC)
	actual := int64(40)
	store := newWorkerStore(Task{
		ID: 106, TenantID: 7, UserID: 42, RequestID: "req-settlement-recovery",
		TaskType: "video_generate", Provider: geminiVideoProviderName,
		ProviderTaskID: "operations/op-106", Status: StatusSettlementPending,
		Result: json.RawMessage(`{"status":"completed"}`), ActualCents: &actual,
		CreatedAt: now.Add(-24 * time.Hour),
	})
	provider := &workerProvider{submitID: "must-not-submit"}
	worker := NewWorker(
		store,
		StaticConfigSource{Config: testConfig()},
		StaticProviderRegistry{geminiVideoProviderName: provider},
		WorkerOptions{Owner: "settlement-recovery-worker", Now: func() time.Time { return now }},
	)

	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	if provider.submitCalls != 0 || provider.pollCalls != 0 ||
		store.completeCalls != 1 || store.failureCalls != 0 || store.expireCalls != 0 ||
		store.task.Status != StatusSucceeded {
		t.Fatalf("待结算任务必须只续结算: submit=%d poll=%d complete=%d failure=%d expire=%d task=%+v",
			provider.submitCalls, provider.pollCalls, store.completeCalls,
			store.failureCalls, store.expireCalls, store.task)
	}
}

func TestWorkerSuccessSettlesOnceAcrossConcurrentRunOnce(t *testing.T) {
	// 变异:去掉 CompleteSuccess 里的 lease_owner / 终态守卫;
	// 两个 worker 都会结算,completeCalls 变成 2。
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
	// 变异:把 provider 的 failed 映射成进度更新而非终态失败;
	// failureCalls 保持为零,被冻结的款项不会被释放。
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

func TestWorkerBoundProviderRetryableSubmitRequeuesWithoutRefund(t *testing.T) {
	now := time.Date(2026, 7, 21, 2, 0, 0, 0, time.UTC)
	store := newWorkerStore(Task{
		ID: 31, TenantID: 7, UserID: 42, APIKeyID: 13, RequestID: "video-31",
		TaskType: "video_generate", Provider: "grok_video", Status: StatusQueued, CreatedAt: now,
	})
	provider := &boundWorkerProvider{submitErr: retryableProviderError("provider_account_temporarily_unavailable", ErrProviderUnavailable)}
	worker := NewWorker(store, StaticConfigSource{Config: testConfig()}, StaticProviderRegistry{"grok_video": provider},
		WorkerOptions{Owner: "w-video", Now: func() time.Time { return now }})

	processed, err := worker.RunOnce(context.Background())
	if !processed || err == nil {
		t.Fatalf("processed=%v err=%v", processed, err)
	}
	if provider.boundSubmitCalls != 1 || provider.legacySubmitCalls != 0 {
		t.Fatalf("bound/legacy submit=%d/%d", provider.boundSubmitCalls, provider.legacySubmitCalls)
	}
	if store.failureCalls != 0 || store.task.Status != StatusQueued || store.task.LeaseOwner != "" {
		t.Fatalf("retryable submit changed terminal state: %+v", store.task)
	}
	if store.deferCalls != 1 || store.task.LeaseExpiresAt == nil || !store.task.LeaseExpiresAt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("可重试提交没有延后重新入队: calls=%d task=%+v", store.deferCalls, store.task)
	}
	if _, err := store.AcquireLease(context.Background(), "too-early", time.Second, now.Add(time.Second)); !errors.Is(err, ErrNoRunnableTask) {
		t.Fatalf("退避窗口内不应重新拿到任务: %v", err)
	}
}

func TestMediaTaskWorkerLogsNormalizedProviderClass(t *testing.T) {
	err := retryableProviderErrorAfter("upstream_rate_limited", time.Minute, ErrProviderUnavailable)
	if got := mediaTaskWorkerErrorClass(err); got != "upstream_rate_limited" {
		t.Fatalf("error_class=%q want upstream_rate_limited", got)
	}
}

func TestWorkerBoundProviderTerminalPollFailsAndRefunds(t *testing.T) {
	now := time.Date(2026, 7, 21, 2, 5, 0, 0, time.UTC)
	store := newWorkerStore(Task{
		ID: 32, TenantID: 7, UserID: 42, APIKeyID: 13, RequestID: "video-32",
		TaskType: "video_generate", Provider: "grok_video", ProviderTaskID: "up-32",
		Status: StatusInProgress, CreatedAt: now,
	})
	provider := &boundWorkerProvider{pollErr: terminalProviderError("provider_task_not_found", ErrProviderUnavailable)}
	worker := NewWorker(store, StaticConfigSource{Config: testConfig()}, StaticProviderRegistry{"grok_video": provider},
		WorkerOptions{Owner: "w-video", Now: func() time.Time { return now }})

	processed, err := worker.RunOnce(context.Background())
	if !processed || err == nil {
		t.Fatalf("processed=%v err=%v", processed, err)
	}
	if provider.boundPollCalls != 1 || provider.legacyPollCalls != 0 {
		t.Fatalf("bound/legacy poll=%d/%d", provider.boundPollCalls, provider.legacyPollCalls)
	}
	if store.failureCalls != 1 || store.task.Status != StatusFailed || store.task.ErrorClass != "provider_task_not_found" {
		t.Fatalf("terminal poll did not refund/fail: %+v", store.task)
	}
}

func TestWorkerTimeoutExpiresAndRefunds(t *testing.T) {
	// 变异:超时判定时检查 updated_at 而非 created_at;这个陈旧任务
	// 会被 poll 而非过期,expireCalls 保持为零。
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

func TestWorkerPersistedVideoSuccessSettlesAfterTaskTimeout(t *testing.T) {
	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	actual := int64(91)
	task := Task{
		ID: 41, TenantID: 7, UserID: 42, APIKeyID: 13, RequestID: "video-41",
		TaskType: "video_generate", Provider: "grok_video", ProviderTaskID: "up-41",
		ProviderAccountID: 88, PoolGroupID: 9, RouteID: "route-video",
		Status: StatusInProgress, CreatedAt: now.Add(-2 * time.Hour),
		Result: json.RawMessage(`{"status":"completed"}`), ActualCents: &actual,
	}
	store := newWorkerStore(task)
	cfg := testConfig()
	cfg.TaskTimeout = time.Minute
	provider := &boundWorkerProvider{}
	worker := NewWorker(
		store,
		StaticConfigSource{Config: cfg},
		StaticProviderRegistry{"grok_video": provider},
		WorkerOptions{Owner: "w-video-recovery", Now: func() time.Time { return now }},
	)

	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	if store.completeCalls != 1 || store.expireCalls != 0 || store.task.Status != StatusSucceeded {
		t.Fatalf("持久化成功未优先结算: complete=%d expire=%d task=%+v",
			store.completeCalls, store.expireCalls, store.task)
	}
	if provider.boundPollCalls != 0 || provider.boundSubmitCalls != 0 {
		t.Fatalf("恢复路径不应再次调用上游: submit=%d poll=%d",
			provider.boundSubmitCalls, provider.boundPollCalls)
	}
	var reason struct {
		AccountID   int64  `json:"selected_account_id"`
		PoolGroupID int64  `json:"pool_group_id"`
		RouteID     string `json:"route_id"`
	}
	if err := json.Unmarshal(store.lastSuccess.RoutingReason, &reason); err != nil {
		t.Fatalf("解析恢复路由原因: %v", err)
	}
	if reason.AccountID != 88 || reason.PoolGroupID != 9 || reason.RouteID != "route-video" {
		t.Fatalf("恢复路由原因=%+v, want account/pool/route=88/9/route-video", reason)
	}
}

func TestWorkerLeaseLostReportsOrphanWithIdempotencyKey(t *testing.T) {
	// 场景:worker A 已 Submit 创建真实上游任务,但租约在 Submit 期间过期被另一个
	// worker 抢走,MarkProviderSubmitted 返回 ErrLeaseLost。
	// 断言 (a):Submit 携带了由任务身份派生的幂等键(让上游对重复提交去重);
	// 断言 (b):ErrLeaseLost 时孤儿被上报(含 providerTaskID + tenant),而非静默吞掉。
	//
	// 变异一:把 worker.go 里 SubmitReq 的 IdempotencyKey 字段去掉 →
	//   provider.gotReq.IdempotencyKey 为空,断言 (a) RED。
	// 变异二:把 ErrLeaseLost 分支改回 `return nil`(删掉 w.reportOrphan 调用)→
	//   reporter.calls 为 0,断言 (b) RED。
	now := time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)
	store := newLeaseStealingStore(Task{
		ID: 9, TenantID: 7, UserID: 42, RequestID: "req-9", TaskType: "image_generation",
		Provider: "http", Status: StatusQueued, CreatedAt: now,
	})
	provider := &capturingProvider{submitID: "up-orphan-9"}
	reporter := &spyOrphanReporter{}
	worker := NewWorker(store, StaticConfigSource{Config: testConfig()}, StaticProviderRegistry{"http": provider},
		WorkerOptions{Owner: "wA", Now: func() time.Time { return now }, OrphanReporter: reporter})

	processed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !processed {
		t.Fatal("RunOnce processed=false want true")
	}

	// 断言 (a):Submit 真的发生且携带了幂等键,值等于任务派生键。
	if provider.submitCalls != 1 {
		t.Fatalf("submitCalls=%d want 1", provider.submitCalls)
	}
	wantKey := DeriveIdempotencyKey(9, "req-9")
	if wantKey != "mediatask-9" {
		t.Fatalf("派生键自检失败 want mediatask-9 got %q", wantKey)
	}
	if provider.gotReq.IdempotencyKey != wantKey {
		t.Fatalf("SubmitReq.IdempotencyKey=%q want %q(重复上游提交不会被去重)", provider.gotReq.IdempotencyKey, wantKey)
	}

	// 断言 (b):租约丢失时孤儿被上报,且携带 providerTaskID + tenant,而非静默丢弃。
	if reporter.calls != 1 {
		t.Fatalf("orphan reporter calls=%d want 1(孤儿 providerTaskID 被静默吞掉)", reporter.calls)
	}
	got := reporter.last
	if got.ProviderTaskID != "up-orphan-9" || got.TaskID != 9 || got.TenantID != 7 || got.UserID != 42 {
		t.Fatalf("orphan event=%+v want providerTaskID=up-orphan-9 task=9 tenant=7 user=42", got)
	}
}

type capturingProvider struct {
	submitID    string
	submitCalls int
	gotReq      SubmitReq
}

func (p *capturingProvider) Submit(_ context.Context, req SubmitReq) (string, error) {
	p.submitCalls++
	p.gotReq = req
	return p.submitID, nil
}

func (p *capturingProvider) Poll(context.Context, string) (PollResult, error) {
	return PollResult{Status: StatusInProgress}, nil
}

type spyOrphanReporter struct {
	mu    sync.Mutex
	calls int
	last  OrphanProviderTask
}

func (r *spyOrphanReporter) ReportOrphanProviderTask(_ context.Context, ev OrphanProviderTask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.last = ev
}

// leaseStealingStore 模拟"租约在 Submit 期间被抢走"的竞态:任务可被正常租出,
// 但 MarkProviderSubmitted 必定命 0 行(返回 ErrLeaseLost),复现孤儿成本路径。
type leaseStealingStore struct {
	*workerStore
}

func newLeaseStealingStore(task Task) *leaseStealingStore {
	return &leaseStealingStore{workerStore: newWorkerStore(task)}
}

func (s *leaseStealingStore) MarkProviderSubmitted(_ context.Context, _ Task, _, _ string, _ time.Time) (Task, error) {
	return Task{}, ErrLeaseLost
}

type workerProvider struct {
	submitID            string
	submitErr           error
	poll                PollResult
	submitCalls         int
	pollCalls           int
	onSubmit            func(SubmitReq)
	returnEmptySubmitID bool
}

type boundWorkerProvider struct {
	submitErr         error
	pollErr           error
	boundSubmitCalls  int
	boundPollCalls    int
	legacySubmitCalls int
	legacyPollCalls   int
}

func (p *boundWorkerProvider) Submit(context.Context, SubmitReq) (string, error) {
	p.legacySubmitCalls++
	return "", nil
}

func (p *boundWorkerProvider) Poll(context.Context, string) (PollResult, error) {
	p.legacyPollCalls++
	return PollResult{}, nil
}

func (p *boundWorkerProvider) SubmitBound(context.Context, Task, SubmitReq) (string, error) {
	p.boundSubmitCalls++
	return "", p.submitErr
}

func (p *boundWorkerProvider) PollBound(context.Context, Task, string) (PollResult, error) {
	p.boundPollCalls++
	return PollResult{}, p.pollErr
}

func (p *workerProvider) Submit(_ context.Context, req SubmitReq) (string, error) {
	p.submitCalls++
	if p.onSubmit != nil {
		p.onSubmit(req)
	}
	if p.submitErr != nil {
		return "", p.submitErr
	}
	if p.returnEmptySubmitID {
		return "", nil
	}
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
	mu                 sync.Mutex
	task               Task
	lastSuccess        PollResult
	lastIdempotencyKey string
	completeCalls      int
	failureCalls       int
	expireCalls        int
	deferCalls         int
	submittingCalls    int
	unknownCalls       int
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
	if !isRunnableWorkerStatus(s.task.Status) || s.task.LeaseOwner != "" ||
		(s.task.LeaseExpiresAt != nil && s.task.LeaseExpiresAt.After(now)) {
		return Task{}, ErrNoRunnableTask
	}
	s.task.LeaseOwner = owner
	expires := now.Add(ttl)
	s.task.LeaseExpiresAt = &expires
	return s.task, nil
}

func isRunnableWorkerStatus(status Status) bool {
	switch status {
	case StatusQueued, StatusSubmitting, StatusSubmissionReleasing, StatusInProgress, StatusSettlementPending:
		return true
	default:
		return false
	}
}

func (s *workerStore) MarkSubmitting(_ context.Context, task Task, owner string, now time.Time) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task.ID != task.ID || s.task.LeaseOwner != owner || s.task.Status != StatusQueued {
		return Task{}, ErrLeaseLost
	}
	s.submittingCalls++
	s.task.Status = StatusSubmitting
	s.task.UpdatedAt = now
	return s.task, nil
}

func (s *workerStore) DeferSubmission(_ context.Context, task Task, owner string, now, retryAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task.ID != task.ID || s.task.LeaseOwner != owner || s.task.Status != StatusSubmitting {
		return ErrLeaseLost
	}
	s.deferCalls++
	s.task.Status = StatusQueued
	s.task.ErrorClass = ""
	s.task.LeaseOwner = ""
	s.task.LeaseExpiresAt = &retryAt
	s.task.UpdatedAt = now
	return nil
}

func (s *workerStore) MarkSubmissionUnknown(
	_ context.Context,
	task Task,
	owner string,
	errorClass string,
	idempotencyKey string,
	now time.Time,
) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task.ID != task.ID || s.task.LeaseOwner != owner ||
		(s.task.Status != StatusSubmitting && s.task.Status != StatusInProgress) ||
		s.task.ProviderTaskID != "" {
		return Task{}, ErrLeaseLost
	}
	s.unknownCalls++
	s.lastIdempotencyKey = idempotencyKey
	s.task.Status = StatusSubmissionUnknown
	s.task.ErrorClass = errorClass
	s.task.LeaseOwner = ""
	s.task.LeaseExpiresAt = nil
	s.task.UpdatedAt = now
	return s.task, nil
}

func (s *workerStore) DeferLease(_ context.Context, task Task, owner string, now, retryAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task.ID != task.ID || s.task.LeaseOwner != owner ||
		(s.task.Status != StatusQueued && s.task.Status != StatusInProgress) {
		return ErrLeaseLost
	}
	s.deferCalls++
	s.task.LeaseOwner = ""
	s.task.LeaseExpiresAt = &retryAt
	s.task.UpdatedAt = now
	return nil
}

func (s *workerStore) MarkProviderSubmitted(_ context.Context, task Task, owner, providerTaskID string, now time.Time) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task.ID != task.ID || s.task.LeaseOwner != owner || s.task.Status != StatusSubmitting {
		return Task{}, ErrLeaseLost
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

func (s *workerStore) ReleaseLease(_ context.Context, task Task, owner string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task.ID != task.ID || s.task.LeaseOwner != owner ||
		(s.task.Status != StatusQueued && s.task.Status != StatusInProgress) {
		return ErrLeaseLost
	}
	s.task.LeaseOwner = ""
	s.task.LeaseExpiresAt = nil
	s.task.UpdatedAt = now
	return nil
}

func (s *workerStore) CompleteSuccess(_ context.Context, task Task, owner string, result PollResult, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task.ID != task.ID || s.task.LeaseOwner != owner || IsTerminal(s.task.Status) {
		return false, nil
	}
	s.completeCalls++
	s.lastSuccess = result
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
