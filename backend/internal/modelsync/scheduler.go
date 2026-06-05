package modelsync

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type Syncer interface {
	Sync(context.Context, string) (SyncResult, error)
}

type SchedulerConfig struct {
	Interval   time.Duration
	RunOnStart bool
}

type SchedulerStatus struct {
	LastRunAt     time.Time
	LastSuccessAt time.Time
	LastErr       string
}

type Scheduler struct {
	service Syncer
	cfg     SchedulerConfig

	statusMu sync.Mutex
	status   SchedulerStatus

	stopOnce sync.Once
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewScheduler(service Syncer, cfg SchedulerConfig) *Scheduler {
	if cfg.Interval <= 0 {
		cfg.Interval = 3 * time.Hour
	}
	return &Scheduler{service: service, cfg: cfg}
}

func (s *Scheduler) Start(ctx context.Context) func() {
	if s == nil || s.service == nil {
		return func() {}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.run(runCtx)
	return s.Stop
}

func (s *Scheduler) Status() SchedulerStatus {
	if s == nil {
		return SchedulerStatus{}
	}
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	return s.status
}

func (s *Scheduler) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.done != nil {
			<-s.done
		}
	})
}

func (s *Scheduler) run(ctx context.Context) {
	defer close(s.done)
	if s.cfg.RunOnStart {
		s.runSync(ctx, "startup")
	}
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runSync(ctx, "periodic")
		}
	}
}

func (s *Scheduler) runSync(ctx context.Context, reason string) {
	_, err := s.service.Sync(ctx, reason)
	if err != nil {
		slog.WarnContext(ctx, "model catalog sync failed",
			"component", "model_sync_scheduler",
			"reason", reason,
			"error", err.Error())
	}
	s.recordStatus(err)
}

func (s *Scheduler) recordStatus(err error) {
	now := time.Now().UTC()
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status.LastRunAt = now
	if err != nil {
		s.status.LastErr = err.Error()
		return
	}
	s.status.LastErr = ""
	s.status.LastSuccessAt = now
}
