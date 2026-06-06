package mediatask

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

type WorkerOptions struct {
	Owner    string
	LeaseTTL time.Duration
	Now      func() time.Time
}

type Worker struct {
	store    Store
	configs  ConfigSource
	registry ProviderRegistry
	owner    string
	leaseTTL time.Duration
	now      func() time.Time

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
	return &Worker{store: store, configs: configs, registry: registry, owner: opts.Owner, leaseTTL: opts.LeaseTTL, now: opts.Now}
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
		_, _ = w.RunOnce(ctx)
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
		})
		if err != nil {
			_, ferr := w.store.CompleteFailure(ctx, task, w.owner, "provider_submit_failed", now)
			return errors.Join(err, ferr)
		}
		_, err = w.store.MarkProviderSubmitted(ctx, task, w.owner, providerTaskID, now)
		if errors.Is(err, ErrLeaseLost) {
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
