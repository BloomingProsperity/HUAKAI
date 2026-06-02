package modelsync

import (
	"context"
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

type Scheduler struct {
	service Syncer
	cfg     SchedulerConfig

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
		_, _ = s.service.Sync(ctx, "startup")
	}
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.service.Sync(ctx, "periodic")
		}
	}
}
