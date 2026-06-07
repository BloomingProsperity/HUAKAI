package apikeyexpiry

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

const defaultInterval = 5 * time.Minute

type WorkerConfig struct {
	Service  *Service
	Interval time.Duration
	Logger   *zap.Logger
}

type Worker struct {
	service  *Service
	interval time.Duration
	logger   *zap.Logger

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &Worker{
		service:  cfg.Service,
		interval: cfg.Interval,
		logger:   cfg.Logger,
	}
}

func (w *Worker) Start(ctx context.Context) {
	if w == nil || w.service == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
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
	w.running = false
	done := w.done
	w.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (w *Worker) RunOnce(ctx context.Context) (int64, error) {
	if w == nil || w.service == nil {
		return 0, nil
	}
	return w.service.SweepExpiredKeys(ctx)
}

func (w *Worker) loop(ctx context.Context) {
	defer close(w.done)
	interval := w.interval
	if interval <= 0 {
		interval = defaultInterval
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
			changed, err := w.RunOnce(ctx)
			if err != nil && w.logger != nil {
				w.logger.Warn("api key expiry sweep failed", zap.Error(err))
				continue
			}
			if changed > 0 && w.logger != nil {
				w.logger.Info("api key expiry sweep completed", zap.Int64("expired_keys", changed))
			}
		}
	}
}
