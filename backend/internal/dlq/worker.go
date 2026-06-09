package dlq

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type WorkerConfig struct {
	HighWorkers   int
	MediumWorkers int
	LowWorkers    int
	LeaseTTL      time.Duration
	IdleSleep     time.Duration
}

// depthRefresher is implemented by *Store and allows the Worker to
// refresh the dlq_depth expvar gauge without coupling to the concrete type.
type depthRefresher interface {
	UpdateDLQDepthGauge(context.Context) error
}

type Worker struct {
	service      *Service
	cfg          WorkerConfig
	depthRefresh depthRefresher // optional; set via WithDepthRefresher

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewWorker(service *Service, cfg WorkerConfig) *Worker {
	if cfg.HighWorkers <= 0 {
		cfg.HighWorkers = 2
	}
	if cfg.MediumWorkers <= 0 {
		cfg.MediumWorkers = 1
	}
	if cfg.LowWorkers <= 0 {
		cfg.LowWorkers = 1
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 30 * time.Second
	}
	if cfg.IdleSleep <= 0 {
		cfg.IdleSleep = time.Second
	}
	return &Worker{service: service, cfg: cfg}
}

// WithDepthRefresher wires a depth-gauge refresher (typically *Store) into the
// Worker so that the dlq_depth expvar map is kept fresh for alerting.
func WithDepthRefresher(r depthRefresher) func(*Worker) {
	return func(w *Worker) { w.depthRefresh = r }
}

// ApplyWorkerOptions applies functional options after construction.
func (w *Worker) ApplyWorkerOptions(opts ...func(*Worker)) {
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}
}

func (w *Worker) Start(ctx context.Context) {
	if w == nil || w.service == nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.startLane(runCtx, LaneHigh, w.cfg.HighWorkers)
	w.startLane(runCtx, LaneMed, w.cfg.MediumWorkers)
	w.startLane(runCtx, LaneLow, w.cfg.LowWorkers)
	if w.depthRefresh != nil {
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			w.runDepthRefresh(runCtx)
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
	// LOW lane 是 metrics 等冷事件。shutdown 时尽量 drain 一批，避免积压。
	for {
		processed, err := w.service.ProcessClaim(ctx, LaneLow, "shutdown-low-drain", w.cfg.LeaseTTL)
		if err != nil || !processed {
			return err
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context, lane Lane, workerID string) (bool, error) {
	if w == nil || w.service == nil {
		return false, ErrStoreNotConfigured
	}
	return w.service.ProcessClaim(ctx, lane, workerID, w.cfg.LeaseTTL)
}

func (w *Worker) startLane(ctx context.Context, lane Lane, n int) {
	for i := 0; i < n; i++ {
		workerID := fmt.Sprintf("dlq-%s-%d", lane, i+1)
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				processed, err := w.service.ProcessClaim(ctx, lane, workerID, w.cfg.LeaseTTL)
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
		}()
	}
}

// runDepthRefresh periodically calls UpdateDLQDepthGauge so the dlq_depth
// expvar map stays fresh. It runs every 30 seconds until ctx is cancelled.
func (w *Worker) runDepthRefresh(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	// Refresh once immediately on startup.
	if err := w.depthRefresh.UpdateDLQDepthGauge(ctx); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "dlq depth gauge refresh failed", "error", err.Error())
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.depthRefresh.UpdateDLQDepthGauge(ctx); err != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "dlq depth gauge refresh failed", "error", err.Error())
			}
		}
	}
}
