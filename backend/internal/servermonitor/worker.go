package servermonitor

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

var (
	ErrNodeIdentityInUse    = errors.New("server monitor node identity is already active")
	ErrNodeLeaseUnavailable = errors.New("server monitor node lease unavailable")
	ErrInitialCollection    = errors.New("server monitor initial collection failed")
	ErrInitialSnapshotWrite = errors.New("server monitor initial snapshot write failed")
)

type CollectorAPI interface {
	Collect(context.Context) (Collection, error)
}

type SnapshotStore interface {
	WriteSnapshot(context.Context, Snapshot) error
	Cleanup(context.Context, time.Time, int) (CleanupResult, error)
}

type Lease interface {
	TryAcquire(context.Context) (bool, func(), error)
}

type WorkerConfig struct {
	Identity        Identity
	Session         Session
	Collector       CollectorAPI
	Store           SnapshotStore
	NodeLease       Lease
	CleanupLease    Lease
	Interval        time.Duration
	Retention       time.Duration
	CleanupInterval time.Duration
	CleanupBatch    int
	Logger          *slog.Logger
	NewTicker       func(time.Duration) ticker
	Now             func() time.Time
}

type ticker interface {
	Chan() <-chan time.Time
	Stop()
}

type realTicker struct {
	ticker *time.Ticker
}

func (t realTicker) Chan() <-chan time.Time { return t.ticker.C }
func (t realTicker) Stop()                  { t.ticker.Stop() }

type Worker struct {
	cfg         WorkerConfig
	mu          sync.Mutex
	started     bool
	sequence    int64
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewWorker(cfg WorkerConfig) (*Worker, error) {
	if cfg.Identity.NodeID == "" || cfg.Session.ID == [16]byte{} || cfg.Session.StartedAt.IsZero() {
		return nil, errors.New("server monitor worker requires identity and session")
	}
	if cfg.Collector == nil || cfg.Store == nil || cfg.NodeLease == nil {
		return nil, errors.New("server monitor worker requires collector, store and node lease")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Retention <= 0 {
		cfg.Retention = DefaultRetention
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = DefaultCleanupInterval
	}
	if cfg.CleanupBatch <= 0 {
		cfg.CleanupBatch = DefaultCleanupBatch
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewTicker == nil {
		cfg.NewTicker = func(interval time.Duration) ticker {
			return realTicker{ticker: time.NewTicker(interval)}
		}
	}
	return &Worker{cfg: cfg}, nil
}

func (w *Worker) Start(parent context.Context) error {
	if w == nil {
		return errors.New("server monitor worker is nil")
	}
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return errors.New("server monitor worker already started")
	}
	w.mu.Unlock()

	acquired, release, err := w.cfg.NodeLease.TryAcquire(parent)
	if err != nil {
		return ErrNodeLeaseUnavailable
	}
	if !acquired || release == nil {
		return ErrNodeIdentityInUse
	}
	if err := w.collectAndWrite(parent); err != nil {
		release()
		if errors.Is(err, errCollect) {
			return ErrInitialCollection
		}
		return ErrInitialSnapshotWrite
	}

	runCtx, cancel := context.WithCancel(parent)
	w.mu.Lock()
	w.started = true
	w.cancel = cancel
	w.done = make(chan struct{})
	done := w.done
	w.mu.Unlock()
	go w.run(runCtx, done, release)
	return nil
}

var errCollect = errors.New("collect server monitor snapshot")

func (w *Worker) collectAndWrite(ctx context.Context) error {
	collection, err := w.cfg.Collector.Collect(ctx)
	if err != nil {
		return errors.Join(errCollect, err)
	}
	w.mu.Lock()
	w.sequence++
	sequence := w.sequence
	w.mu.Unlock()
	snapshot := Snapshot{
		Identity:           w.cfg.Identity,
		SourceKind:         SourceKindBuiltin,
		ViewScope:          collection.ViewScope,
		SessionID:          w.cfg.Session.ID,
		SessionStartedAt:   w.cfg.Session.StartedAt,
		Sequence:           sequence,
		CollectedAt:        collection.CollectedAt,
		CollectionStatus:   collection.CollectionStatus,
		ActiveErrorClasses: collection.ActiveErrorClasses,
		OSName:             collection.OSName,
		OSArch:             collection.OSArch,
		Metrics:            collection.Metrics,
		MetricStates:       collection.MetricStates,
	}
	return w.cfg.Store.WriteSnapshot(ctx, snapshot)
}

func (w *Worker) run(ctx context.Context, done chan struct{}, releaseNode func()) {
	defer close(done)
	defer releaseNode()
	collectionTicker := w.cfg.NewTicker(w.cfg.Interval)
	cleanupTicker := w.cfg.NewTicker(w.cfg.CleanupInterval)
	defer collectionTicker.Stop()
	defer cleanupTicker.Stop()
	w.runCleanup(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-collectionTicker.Chan():
			if err := w.collectAndWrite(ctx); err != nil && !errors.Is(err, context.Canceled) {
				class := "snapshot_write_failed"
				if errors.Is(err, errCollect) {
					class = "collection_cycle_failed"
				}
				w.cfg.Logger.WarnContext(ctx, "服务器监测采集周期失败", "error_class", class)
			}
		case <-cleanupTicker.Chan():
			w.runCleanup(ctx)
		}
	}
}

func (w *Worker) runCleanup(ctx context.Context) {
	if w.cfg.CleanupLease == nil {
		return
	}
	acquired, release, err := w.cfg.CleanupLease.TryAcquire(ctx)
	if err != nil || !acquired || release == nil {
		if err != nil {
			w.cfg.Logger.WarnContext(ctx, "服务器监测清理租约不可用", "error_class", "cleanup_lease_unavailable")
		}
		return
	}
	defer release()
	cutoff := w.cfg.Now().UTC().Add(-w.cfg.Retention)
	// 单次租约最多清十批，既能追平长时间停机后的积压，也避免长期占住数据库。
	for range 10 {
		result, err := w.cfg.Store.Cleanup(ctx, cutoff, w.cfg.CleanupBatch)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				w.cfg.Logger.WarnContext(ctx, "服务器监测历史清理失败", "error_class", "cleanup_failed")
			}
			return
		}
		if result.SamplesDeleted < int64(w.cfg.CleanupBatch) && result.NodesDeleted < int64(w.cfg.CleanupBatch) {
			return
		}
	}
}

func (w *Worker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	cancel := w.cancel
	done := w.done
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
