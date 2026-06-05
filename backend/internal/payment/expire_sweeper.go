package payment

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	defaultExpireSweepInterval   = time.Minute
	defaultExpireSweepBatchLimit = 200
)

type ExpireSweeperConfig struct {
	Store      Store
	Interval   time.Duration
	BatchLimit int
	Clock      func() time.Time
	Logger     *zap.Logger
}

type ExpireSweeper struct {
	store      Store
	interval   time.Duration
	batchLimit int
	clock      func() time.Time
	logger     *zap.Logger

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

func NewExpireSweeper(cfg ExpireSweeperConfig) *ExpireSweeper {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultExpireSweepInterval
	}
	if cfg.BatchLimit <= 0 {
		cfg.BatchLimit = defaultExpireSweepBatchLimit
	}
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &ExpireSweeper{
		store:      cfg.Store,
		interval:   cfg.Interval,
		batchLimit: cfg.BatchLimit,
		clock:      cfg.Clock,
		logger:     cfg.Logger,
	}
}

func (w *ExpireSweeper) Start(ctx context.Context) {
	if w == nil || w.store == nil {
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

func (w *ExpireSweeper) loop(ctx context.Context) {
	defer close(w.done)
	interval := w.interval
	if interval <= 0 {
		interval = defaultExpireSweepInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			if _, err := w.RunOnce(ctx, w.now()); err != nil && w.logger != nil {
				w.logger.Warn("payment expire sweep failed", zap.Error(err))
			}
		}
	}
}

func (w *ExpireSweeper) RunOnce(ctx context.Context, now time.Time) (int, error) {
	if w == nil || w.store == nil {
		return 0, nil
	}
	if now.IsZero() {
		now = w.now()
	}
	batchLimit := w.batchLimit
	if batchLimit <= 0 {
		batchLimit = defaultExpireSweepBatchLimit
	}
	expired, err := w.store.ExpireStalePendingOrders(ctx, now.UTC(), batchLimit)
	if err != nil {
		return expired, fmt.Errorf("payment expire sweep: %w", err)
	}
	return expired, nil
}

func (w *ExpireSweeper) now() time.Time {
	if w == nil || w.clock == nil {
		return time.Now().UTC()
	}
	return w.clock().UTC()
}

func (w *ExpireSweeper) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	close(w.stop)
	w.running = false
	done := w.done
	w.mu.Unlock()
	if done != nil {
		<-done
	}
}
