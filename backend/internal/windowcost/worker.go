package windowcost

import (
	"context"
	"log/slog"
	"time"
)

const (
	// DefaultInterval is how often the worker re-aggregates window costs.
	DefaultInterval = 90 * time.Second
)

// AccountRecord is one active account row returned by the Lister.
type AccountRecord struct {
	ID                   int64
	TenantID             int64
	SessionWindow5hStart time.Time // zero → no active window → skip
}

// Lister returns accounts that have a positive window_cost_limit_cents and an
// active session window start.
type Lister interface {
	ListLimitedAccounts(ctx context.Context) ([]AccountRecord, error)
}

// Aggregator sums actual_cost for an account since a given timestamp.
type Aggregator interface {
	// SumWindowCost returns the total actual_cost (in micro-dollars, same unit
	// as the usage_records.actual_cost column × 1e8 → we store as cents after
	// conversion) for the account since windowStart.
	// The returned value is in cents (1/100 USD) — callers must convert from
	// the raw numeric column.
	SumWindowCost(ctx context.Context, accountID int64, windowStart time.Time) (cents int64, err error)
}

// Worker periodically aggregates window costs into a Cache.
type Worker struct {
	lister     Lister
	aggregator Aggregator
	cache      *Cache
	interval   time.Duration
	logger     *slog.Logger
}

// NewWorker constructs a Worker. interval<=0 uses DefaultInterval.
func NewWorker(lister Lister, aggregator Aggregator, cache *Cache, interval time.Duration, logger *slog.Logger) *Worker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		lister:     lister,
		aggregator: aggregator,
		cache:      cache,
		interval:   interval,
		logger:     logger,
	}
}

// Start launches the background aggregation loop. It returns immediately;
// the loop runs until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	go func() {
		// Run once immediately on startup, then on ticker.
		w.tick(ctx)
		t := time.NewTicker(w.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w.tick(ctx)
			}
		}
	}()
}

func (w *Worker) tick(ctx context.Context) {
	accounts, err := w.lister.ListLimitedAccounts(ctx)
	if err != nil {
		w.logger.Warn("windowcost: list limited accounts failed", "err", err)
		// fail-open: leave cache as-is
		return
	}
	for _, a := range accounts {
		if a.SessionWindow5hStart.IsZero() {
			// No active window — skip; cache entry stays stale → fail-open.
			continue
		}
		cents, err := w.aggregator.SumWindowCost(ctx, a.ID, a.SessionWindow5hStart)
		if err != nil {
			w.logger.Warn("windowcost: aggregate cost failed", "account_id", a.ID, "err", err)
			// fail-open: do not update cache for this account
			continue
		}
		w.cache.Set(a.ID, cents)
	}
}
