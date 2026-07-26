package dlq

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type WorkerConfig struct {
	IdleSleep    time.Duration
	DrainTimeout time.Duration
	RetryPolicy  RetryPolicy
}

type Worker struct {
	outbox   Outbox
	cfg      WorkerConfig
	handlers map[string]Handler
	now      func() time.Time
	// instanceID 每个 Worker 唯一(进程级), 使 owner 租约令牌跨进程可区分。
	instanceID string

	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.RWMutex
}

type WorkerOption func(*Worker)

func WithWorkerClock(now func() time.Time) WorkerOption {
	return func(w *Worker) {
		if now != nil {
			w.now = now
		}
	}
}

func NewWorker(outbox Outbox, cfg WorkerConfig, opts ...WorkerOption) *Worker {
	if cfg.IdleSleep <= 0 {
		cfg.IdleSleep = time.Second
	}
	if cfg.DrainTimeout <= 0 {
		cfg.DrainTimeout = 30 * time.Second
	}
	cfg.RetryPolicy = cfg.RetryPolicy.normalized()
	w := &Worker{
		outbox:     outbox,
		instanceID: newEventID(),
		cfg:        cfg,
		handlers:   make(map[string]Handler),
		now:        func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}
	return w
}

func (w *Worker) Register(eventType string, h Handler) {
	if w == nil || eventType == "" || h == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers[eventType] = h
}

func (w *Worker) Start(ctx context.Context) {
	if w == nil || w.outbox == nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	for _, priority := range []Priority{PriorityCritical, PriorityHigh, PriorityDefault} {
		priority := priority
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			w.runLane(runCtx, priority)
		}()
	}
}

func (w *Worker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if w.cancel != nil {
		w.cancel()
	}
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	drainCtx, cancel := context.WithTimeout(ctx, w.cfg.DrainTimeout)
	defer cancel()
	for _, priority := range []Priority{PriorityCritical, PriorityHigh, PriorityDefault} {
		for {
			processed, err := w.RunOnce(drainCtx, priority, "shutdown-drain")
			if err != nil || !processed {
				break
			}
		}
	}
	return nil
}

func (w *Worker) RunOnce(ctx context.Context, priority Priority, workerID string) (bool, error) {
	if w == nil || w.outbox == nil {
		return false, ErrOutboxNotConfigured
	}
	ev, ok, err := w.outbox.Dequeue(ctx, DequeueOptions{
		Priority:          priority,
		Now:               w.now(),
		WorkerID:          workerID,
		VisibilityTimeout: w.cfg.RetryPolicy.normalized().MaxBackoff,
	})
	if err != nil || !ok {
		return false, err
	}
	if err := w.handleRecovered(ctx, ev); err != nil {
		delay, dead := w.cfg.RetryPolicy.NextDelay(ev.AttemptCount)
		if dead {
			return true, w.outbox.MarkFailedDead(ctx, ev.ID, workerID, err.Error())
		}
		return true, w.outbox.MarkFailedRetry(ctx, ev.ID, workerID, err.Error(), w.now().Add(delay))
	}
	return true, w.outbox.MarkCompleted(ctx, ev.ID, workerID)
}

func (w *Worker) runLane(ctx context.Context, priority Priority) {
	workerID := fmt.Sprintf("obsdlq-%s-%s", priority, w.instanceID)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		processed, err := w.runOnceRecovered(ctx, priority, workerID)
		if err != nil || !processed {
			timer := time.NewTimer(w.cfg.IdleSleep)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}

// runOnceRecovered 包一层 recover:单次 RunOnce(含 handler 调用)的 panic 不会杀死该优先级泳道的 goroutine
// (否则该泳道永久静默、DLQ 积压不再被消费)。panic 被当作本轮一次失败(返回 processed=false + err),使循环
// 进入 IdleSleep 而非崩溃。与仓内既定 worker recover 范式一致。
func (w *Worker) runOnceRecovered(ctx context.Context, priority Priority, workerID string) (processed bool, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			reason := RedactString(fmt.Sprint(rec))
			err = fmt.Errorf("obsdlq: RunOnce panicked: %s", reason)
			slog.ErrorContext(ctx, "obs dlq worker RunOnce panicked; recovered to keep lane alive", "priority", string(priority), "reason", reason)
		}
	}()
	return w.RunOnce(ctx, priority, workerID)
}

func (w *Worker) handleRecovered(ctx context.Context, ev OutboxEvent) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			reason := RedactString(fmt.Sprint(rec))
			err = fmt.Errorf("obsdlq: handler panicked: %s", reason)
			slog.ErrorContext(ctx, "obs dlq handler panicked; event enters retry policy",
				"event_id", ev.ID,
				"event_type", ev.EventType,
				"reason", reason,
			)
		}
	}()
	return w.handle(ctx, ev)
}

func (w *Worker) handle(ctx context.Context, ev OutboxEvent) error {
	w.mu.RLock()
	h := w.handlers[ev.EventType]
	w.mu.RUnlock()
	if h == nil {
		return fmt.Errorf("%w: %s", ErrNoHandler, ev.EventType)
	}
	return h(ctx, ev)
}
