package accountintake

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

const DefaultStagedCleanupInterval = time.Minute

type StagedCleanupStore interface {
	Cleanup(context.Context) error
}

type StagedCleanupLease interface {
	TryAcquire(context.Context) (bool, func(), error)
}

type StagedCleanupWorkerConfig struct {
	Store    StagedCleanupStore
	Lease    StagedCleanupLease
	Interval time.Duration
	Logger   *slog.Logger
}

type StagedCleanupWorker struct {
	store    StagedCleanupStore
	lease    StagedCleanupLease
	interval time.Duration
	logger   *slog.Logger

	mu   sync.Mutex
	done chan struct{}
}

func NewStagedCleanupWorker(cfg StagedCleanupWorkerConfig) *StagedCleanupWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultStagedCleanupInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &StagedCleanupWorker{
		store: cfg.Store, lease: cfg.Lease, interval: cfg.Interval, logger: cfg.Logger,
	}
}

func (w *StagedCleanupWorker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	if w.done != nil {
		w.mu.Unlock()
		return
	}
	done := make(chan struct{})
	w.done = done
	w.mu.Unlock()
	go func() {
		defer close(done)
		w.loop(ctx)
	}()
}

// Wait 等待后台清理循环退出。调用方应先取消传给 Start 的 context。
func (w *StagedCleanupWorker) Wait(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	done := w.done
	w.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *StagedCleanupWorker) loop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.logger.WarnContext(ctx, "短期凭据清理失败", "error", err)
		}
		timer := time.NewTimer(w.interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (w *StagedCleanupWorker) RunOnce(ctx context.Context) error {
	if w == nil || w.store == nil {
		return ErrNotConfigured
	}
	if w.lease != nil {
		acquired, release, err := w.lease.TryAcquire(ctx)
		if err != nil {
			return err
		}
		if !acquired {
			return nil
		}
		if release == nil {
			return errors.New("短期凭据清理租约缺少释放函数")
		}
		defer release()
	}
	return w.store.Cleanup(ctx)
}
