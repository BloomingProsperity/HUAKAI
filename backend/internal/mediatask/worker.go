package mediatask

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// OrphanReporter 接收 worker 已在上游创建、却因租约丢失而无法落库的孤儿上游
// 任务,供运维做孤儿对账(上游已计费但本平台未追踪/结算的任务)。实现需保证
// 并发安全且不得阻塞 worker 主循环。
type OrphanReporter interface {
	ReportOrphanProviderTask(ctx context.Context, ev OrphanProviderTask)
}

// OrphanProviderTask 描述一条潜在孤儿上游任务的对账线索:上游已创建但本平台
// 因租约被抢走而未能把 providerTaskID 落库,该上游任务可能跑完并被上游计费,
// 却没有对应的本平台扣费。
type OrphanProviderTask struct {
	TaskID         int64
	TenantID       int64
	UserID         int64
	Provider       string
	ProviderTaskID string
	Owner          string
	ObservedAt     time.Time
}

type WorkerOptions struct {
	Owner    string
	LeaseTTL time.Duration
	Now      func() time.Time
	// Logger 用于结构化记录孤儿上游任务等运维事件;为 nil 时回退到 slog 默认实例。
	Logger *slog.Logger
	// OrphanReporter 可选,接收孤儿上游任务以便运维对账;为 nil 时仅打日志。
	OrphanReporter OrphanReporter
}

type Worker struct {
	store    Store
	configs  ConfigSource
	registry ProviderRegistry
	owner    string
	leaseTTL time.Duration
	now      func() time.Time
	logger   *slog.Logger
	orphans  OrphanReporter

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

func NewWorker(store Store, configs ConfigSource, registry ProviderRegistry, opts WorkerOptions) *Worker {
	if opts.Owner == "" {
		opts.Owner = fmt.Sprintf("mediatask-%d-%s", os.Getpid(), uuid.NewString())
	}
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = 30 * time.Second
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Worker{
		store: store, configs: configs, registry: registry, owner: opts.Owner,
		leaseTTL: opts.LeaseTTL, now: opts.Now, logger: opts.Logger, orphans: opts.OrphanReporter,
	}
}

func (w *Worker) Start(ctx context.Context) {
	if w == nil || w.store == nil || w.configs == nil || w.registry == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		return
	}
	w.stop = make(chan struct{})
	w.done = make(chan struct{})
	w.running = true
	go w.loop(ctx)
}

func (w *Worker) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	close(w.stop)
	done := w.done
	w.running = false
	w.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if w == nil || w.store == nil || w.configs == nil || w.registry == nil {
		return false, nil
	}
	cfg, err := w.configs.Load(ctx)
	if err != nil {
		return false, err
	}
	cfg = cfg.withDefaults()
	if !cfg.Enabled {
		return false, nil
	}
	now := w.now().UTC()
	task, err := w.store.AcquireLease(ctx, w.owner, w.leaseTTL, now)
	if errors.Is(err, ErrNoRunnableTask) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, w.processLeased(ctx, cfg, task, now)
}

func (w *Worker) loop(ctx context.Context) {
	defer close(w.done)
	for {
		cfg, err := w.configs.Load(ctx)
		interval := 5 * time.Second
		if err == nil {
			interval = cfg.withDefaults().PollInterval
		}
		w.runOnceRecovered(ctx)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-w.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// runOnceRecovered 包一层 recover:单次 RunOnce(含 provider.Submit/Poll 与 store 调用)的 panic 不会杀死
// worker goroutine。否则 panic 会触发 loop 的 defer close(w.done) 但 w.running 仍为 true=「已死却自认在
// 运行」僵态,媒体任务永久停滞、已 Reserve 的预扣久挂(靠 billing LeaseSweeper 兜底)。与仓内既定范式
// (hermesadmin InspectionWorker.tick 的 recover)一致;panic 仅当作本轮失败,下一轮照常继续。
func (w *Worker) runOnceRecovered(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			w.logger.Error("mediatask worker RunOnce panicked; recovered to keep worker alive", slog.Any("recover", rec))
		}
	}()
	_, _ = w.RunOnce(ctx)
}

func (w *Worker) processLeased(ctx context.Context, cfg Config, task Task, now time.Time) error {
	if cfg.TaskTimeout > 0 && !task.CreatedAt.IsZero() && !now.Before(task.CreatedAt.Add(cfg.TaskTimeout)) {
		_, err := w.store.ExpireTask(ctx, task, w.owner, now)
		return err
	}
	provider, ok, err := w.registry.Provider(ctx, task.Provider)
	if err != nil {
		return err
	}
	if !ok {
		_, err := w.store.CompleteFailure(ctx, task, w.owner, "provider_unavailable", now)
		return err
	}
	if task.ProviderTaskID == "" || task.Status == StatusQueued {
		providerTaskID, err := provider.Submit(ctx, SubmitReq{
			TaskID: task.ID, RequestID: task.RequestID, TaskType: task.TaskType, InputParams: task.InputParams,
			IdempotencyKey: DeriveIdempotencyKey(task.ID, task.RequestID),
		})
		if err != nil {
			_, ferr := w.store.CompleteFailure(ctx, task, w.owner, "provider_submit_failed", now)
			return errors.Join(err, ferr)
		}
		_, err = w.store.MarkProviderSubmitted(ctx, task, w.owner, providerTaskID, now)
		if errors.Is(err, ErrLeaseLost) {
			// 租约在 Submit 期间过期被另一个 worker 抢走:上游任务已创建,但本 worker
			// 已无权把 providerTaskID 落库。绝不静默丢弃——上游已携同一幂等键去重,理论
			// 上不会重复计费,但仍需把这条线索记录下来供运维孤儿对账(防上游侧幂等失效时
			// 留痕)。落库由抢到租约的 worker 用同一幂等键完成。
			w.reportOrphan(ctx, task, providerTaskID, now)
			return nil
		}
		return err
	}
	result, err := provider.Poll(ctx, task.ProviderTaskID)
	if err != nil {
		return err
	}
	result = result.Normalized()
	switch result.Status {
	case StatusSucceeded:
		_, err = w.store.CompleteSuccess(ctx, task, w.owner, result, now)
	case StatusFailed:
		_, err = w.store.CompleteFailure(ctx, task, w.owner, firstNonEmpty(result.ErrorClass, "provider_failed"), now)
	case StatusExpired:
		_, err = w.store.ExpireTask(ctx, task, w.owner, now)
	default:
		err = w.store.UpdateProgress(ctx, task, w.owner, result.Progress, now)
	}
	return err
}

// reportOrphan 记录一条潜在孤儿上游任务:上游已创建 providerTaskID,但本 worker
// 因租约被抢走未能落库。做结构化日志(始终)+ 可选 OrphanReporter 投递(若已配置),
// 让运维能据 task.ID + providerTaskID + tenant 把上游已计费却无本平台扣费的孤儿对上账。
// providerTaskID 为空时无对账价值,直接跳过。
func (w *Worker) reportOrphan(ctx context.Context, task Task, providerTaskID string, now time.Time) {
	if providerTaskID == "" {
		return
	}
	logger := w.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx, "mediatask 孤儿上游任务:租约丢失致 providerTaskID 未落库",
		slog.Int64("task_id", task.ID),
		slog.Int64("tenant_id", task.TenantID),
		slog.Int64("user_id", task.UserID),
		slog.String("provider", task.Provider),
		slog.String("provider_task_id", providerTaskID),
		slog.String("lease_owner", w.owner),
	)
	if w.orphans != nil {
		w.orphans.ReportOrphanProviderTask(ctx, OrphanProviderTask{
			TaskID:         task.ID,
			TenantID:       task.TenantID,
			UserID:         task.UserID,
			Provider:       task.Provider,
			ProviderTaskID: providerTaskID,
			Owner:          w.owner,
			ObservedAt:     now,
		})
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
