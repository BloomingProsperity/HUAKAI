package mediatask

import (
	"context"
	"testing"
	"time"
)

// blockingProvider 的 Submit/Poll 一直阻塞直到传入的 ctx 被取消,再返回 ctx.Err()。
// 用于模拟"慢上游/半开连接永不响应",验证 worker 对单次调用的 per-call context 超时确实生效。
type blockingProvider struct{}

func (blockingProvider) Submit(ctx context.Context, _ SubmitReq) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (blockingProvider) Poll(ctx context.Context, _ string) (PollResult, error) {
	<-ctx.Done()
	return PollResult{}, ctx.Err()
}

// TestWorkerProviderCallTimeoutBoundsSubmit 抓对抗 bug-hunt 第三轮 S2:provider.Submit 永久挂起时,
// processLeased 对 Submit 的 context.WithTimeout(cfg.providerCallTimeout()) 必须让单轮 RunOnce 有界返回,
// 而非永久卡死串行 worker、整子系统停摆、预扣久冻。
// §14 变异:把 Submit 的 submitCtx 改回直接传 ctx(去掉 WithTimeout)→ Submit 阻塞外层 ctx(3s 才解)→
// elapsed>1s → 本测试红。
func TestWorkerProviderCallTimeoutBoundsSubmit(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	store := newWorkerStore(Task{ID: 1, TenantID: 7, UserID: 42, RequestID: "req-1", TaskType: "image_generation", Provider: "http", Status: StatusQueued, CreatedAt: now})
	worker := newTimeoutTestWorker(store, now)
	assertRunOnceBounded(t, worker, "Submit")
}

// TestWorkerProviderCallTimeoutBoundsPoll 同上,覆盖 Poll 分支(已提交、in_progress 的任务)。
// §14 变异:把 Poll 的 pollCtx 改回直接传 ctx → Poll 阻塞外层 ctx → 红。
func TestWorkerProviderCallTimeoutBoundsPoll(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	store := newWorkerStore(Task{ID: 2, TenantID: 7, UserID: 42, RequestID: "req-2", TaskType: "image_generation", Provider: "http", ProviderTaskID: "up-2", Status: StatusInProgress, CreatedAt: now})
	worker := newTimeoutTestWorker(store, now)
	assertRunOnceBounded(t, worker, "Poll")
}

func newTimeoutTestWorker(store *workerStore, now time.Time) *Worker {
	cfg := Config{
		Enabled:             true,
		ProviderBaseURL:     "http://media.invalid",
		ProviderCallTimeout: 50 * time.Millisecond,
		TaskTimeout:         15 * time.Minute,
		PollInterval:        time.Second,
	}
	return NewWorker(store, StaticConfigSource{Config: cfg}, StaticProviderRegistry{"http": blockingProvider{}}, WorkerOptions{Owner: "w1", Now: func() time.Time { return now }})
}

func assertRunOnceBounded(t *testing.T, worker *Worker, branch string) {
	t.Helper()
	// 外层 ctx 给远大于 ProviderCallTimeout(50ms)的 deadline:即便变异(per-call 超时缺失),RunOnce 也会在
	// 3s 后随外层 ctx 解阻而返回,故测试本身绝不真挂;有界性由 elapsed 断言区分。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, _ = worker.RunOnce(ctx)
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("%s 路径:RunOnce 应在 per-call 超时(50ms)附近有界返回,实际 %v —— per-call context 超时未生效", branch, elapsed)
	}
}
