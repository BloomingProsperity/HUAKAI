package alerting

import (
	"context"
	"log/slog"
	"time"
)

const DefaultSchedulerInterval = time.Minute

type RuleEvaluator interface {
	EvaluateRules(context.Context, int64, map[string]float64) error
}

type SourceRuleEvaluator interface {
	EvaluateRulesFromSource(context.Context, int64, MetricSource) error
}

type EnabledRuleTenantLister interface {
	ListTenantsWithEnabledRules(context.Context) ([]int64, error)
}

type MetricSource interface {
	Snapshot(context.Context, int64) (map[string]float64, error)
}

type DimensionMetricSource interface {
	SnapshotForDimensions(context.Context, int64, map[string]string) (map[string]float64, error)
}

type SchedulerTicker interface {
	C() <-chan time.Time
	Stop()
}

// LeaderLock gates a single evaluation tick so that, across multiple gateway
// replicas, only the leader fires alerts for a given tick — otherwise every
// replica evaluates the same rules and emits duplicate notifications. OBS-193.
type LeaderLock interface {
	// TryAcquire is non-blocking: (true, release, nil) when this replica is the
	// tick leader (caller must call release after evaluating), (false, nil, nil)
	// when another replica holds it, or a non-nil error on a lock fault.
	TryAcquire(ctx context.Context) (acquired bool, release func(), err error)
}

type SchedulerConfig struct {
	Evaluator    RuleEvaluator
	Store        EnabledRuleTenantLister
	MetricSource MetricSource
	Interval     time.Duration
	NewTicker    func(time.Duration) SchedulerTicker
	// LeaderLock is optional; nil = evaluate every tick (single-replica default,
	// exact current behavior).
	LeaderLock LeaderLock
}

type Scheduler struct {
	evaluator    RuleEvaluator
	store        EnabledRuleTenantLister
	metricSource MetricSource
	interval     time.Duration
	newTicker    func(time.Duration) SchedulerTicker
	leaderLock   LeaderLock
}

func NewScheduler(cfg SchedulerConfig) *Scheduler {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultSchedulerInterval
	}
	if cfg.NewTicker == nil {
		cfg.NewTicker = func(interval time.Duration) SchedulerTicker {
			return realSchedulerTicker{ticker: time.NewTicker(interval)}
		}
	}
	return &Scheduler{
		evaluator:    cfg.Evaluator,
		store:        cfg.Store,
		metricSource: cfg.MetricSource,
		interval:     cfg.Interval,
		newTicker:    cfg.NewTicker,
		leaderLock:   cfg.LeaderLock,
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	if s == nil || s.evaluator == nil || s.store == nil || s.metricSource == nil {
		return ErrStoreNotConfigured
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := s.newTicker(s.interval)
	if ticker == nil {
		return ErrInvalidInput
	}
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			s.evaluateOnce(ctx)
		}
	}
}

func (s *Scheduler) evaluateOnce(ctx context.Context) {
	// OBS-193: with a leader lock configured, only the replica that wins the
	// non-blocking lock evaluates this tick — preventing duplicate alerts across
	// replicas. On a lock fault we fail OPEN (evaluate anyway): a duplicate alert
	// is better than silently dropping alerting.
	if s.leaderLock != nil {
		acquired, release, err := s.leaderLock.TryAcquire(ctx)
		if err != nil {
			logIfLive(ctx, "alerting leader-lock acquire failed; evaluating anyway", err)
		} else if !acquired {
			return
		} else {
			defer release()
		}
	}
	tenantIDs, err := s.store.ListTenantsWithEnabledRules(ctx)
	if err != nil {
		logIfLive(ctx, "alerting scheduler list enabled tenants failed", err)
		return
	}
	for _, tenantID := range tenantIDs {
		if tenantID <= 0 {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if evaluator, ok := s.evaluator.(SourceRuleEvaluator); ok {
			if err := evaluator.EvaluateRulesFromSource(ctx, tenantID, s.metricSource); err != nil {
				logIfLive(ctx, "alerting scheduler evaluation failed", err, "tenant_id", tenantID)
			}
			continue
		}
		snapshot, err := s.metricSource.Snapshot(ctx, tenantID)
		if err != nil {
			logIfLive(ctx, "alerting scheduler metric snapshot failed", err, "tenant_id", tenantID)
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if err := s.evaluator.EvaluateRules(ctx, tenantID, snapshot); err != nil {
			logIfLive(ctx, "alerting scheduler evaluation failed", err, "tenant_id", tenantID)
		}
	}
}

func cloneMetricSnapshot(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return map[string]float64{}
	}
	out := make(map[string]float64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func logIfLive(ctx context.Context, msg string, err error, args ...any) {
	if err == nil || ctx.Err() != nil {
		return
	}
	args = append(args, "error", err.Error())
	slog.WarnContext(ctx, msg, args...)
}

type realSchedulerTicker struct {
	ticker *time.Ticker
}

func (t realSchedulerTicker) C() <-chan time.Time {
	return t.ticker.C
}

func (t realSchedulerTicker) Stop() {
	t.ticker.Stop()
}
