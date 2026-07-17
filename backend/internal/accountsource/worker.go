package accountsource

import (
	"context"
	"time"
)

const defaultCleanupInterval = time.Minute

type CleanupWorker struct {
	store    *Store
	interval time.Duration
	onError  func(error)
}

type CleanupOption func(*CleanupWorker)

func WithCleanupErrorHandler(handler func(error)) CleanupOption {
	return func(worker *CleanupWorker) { worker.onError = handler }
}

func NewCleanupWorker(store *Store, options ...CleanupOption) *CleanupWorker {
	worker := &CleanupWorker{store: store, interval: defaultCleanupInterval}
	for _, option := range options {
		if option != nil {
			option(worker)
		}
	}
	return worker
}

func (w *CleanupWorker) Start(ctx context.Context) {
	if w == nil || w.store == nil {
		return
	}
	go w.run(ctx)
}

func (w *CleanupWorker) run(ctx context.Context) {
	w.sweep(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

func (w *CleanupWorker) sweep(ctx context.Context) {
	for {
		count, err := w.store.ExpireReady(ctx, 200)
		if err != nil {
			w.report(err)
			return
		}
		if count < 200 {
			return
		}
	}
}

func (w *CleanupWorker) report(err error) {
	if w != nil && w.onError != nil && err != nil {
		w.onError(err)
	}
}
