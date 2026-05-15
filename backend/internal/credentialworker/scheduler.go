package credentialworker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

const (
	DefaultInterval      = 60 * time.Second
	DefaultWarningWindow = 15 * time.Minute
	DefaultAccountLimit  = int32(100)
	DefaultMaxAttempts   = 3
)

// Scheduler 主动扫描快过期的 provider account，并通过 storm controller 限流刷新。
type Scheduler struct {
	Queries         *db.Queries
	StormController *auth.StormController
	Signer          Signer

	Refresher   Refresher
	AuditLedger auditledger.Ledger

	interval      time.Duration
	warningWindow time.Duration
	limit         int32
	maxAttempts   int
	backoff       func(attempt int) time.Duration
	sleep         func(context.Context, time.Duration) error
	now           func() time.Time
	ticks         <-chan time.Time

	queryer     refreshQueries
	acquirer    stormAcquirer
	auditWriter auth.AuditWriter

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	seq    atomic.Int64
}

func NewScheduler(queries *db.Queries, storm *auth.StormController, signer Signer, refresher Refresher, opts ...Option) *Scheduler {
	if refresher == nil {
		refresher = NewRegistryRefresher(NewAdapterRegistry(), nil)
	}
	s := &Scheduler{
		Queries:         queries,
		StormController: storm,
		Signer:          signer,
		Refresher:       refresher,
		AuditLedger:     auditledger.NoopLedger{},
		interval:        DefaultInterval,
		warningWindow:   DefaultWarningWindow,
		limit:           DefaultAccountLimit,
		maxAttempts:     DefaultMaxAttempts,
		backoff:         defaultBackoff,
		sleep:           sleepContext,
		now:             time.Now,
		auditWriter:     auth.NoopAuditWriter{},
	}
	if queries != nil {
		s.queryer = queries
		s.auditWriter = dbAuditWriter{queries: queries}
	}
	if storm != nil {
		s.acquirer = storm
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.AuditLedger == nil {
		s.AuditLedger = auditledger.NoopLedger{}
	}
	if s.auditWriter == nil {
		s.auditWriter = auth.NoopAuditWriter{}
	}
	return s
}

func (s *Scheduler) Start(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done != nil {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancel = cancel
	s.done = done
	ticks := s.ticks
	interval := s.interval
	go func() {
		defer close(done)
		var ticker *time.Ticker
		if ticks == nil {
			ticker = time.NewTicker(interval)
			ticks = ticker.C
		}
		if ticker != nil {
			defer ticker.Stop()
		}
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticks:
				_ = s.RunOnce(runCtx)
			}
		}
	}()
	return nil
}

func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Scheduler) RunOnce(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	before := s.now().Add(s.warningWindow)
	accounts, err := s.queryer.ListAccountsForRefresh(ctx, db.ListAccountsForRefreshParams{
		RefreshBefore: pgtype.Timestamptz{Time: before, Valid: true},
		LimitCount:    s.limit,
	})
	if err != nil {
		return err
	}
	var out error
	for _, account := range accounts {
		out = errors.Join(out, s.processAccount(ctx, account))
	}
	return out
}

func (s *Scheduler) validate() error {
	switch {
	case s.queryer == nil:
		return errors.New("credentialworker: refresh queries required")
	case s.acquirer == nil:
		return errors.New("credentialworker: storm controller required")
	case s.Refresher == nil:
		return errors.New("credentialworker: refresher required")
	default:
		return nil
	}
}

func (s *Scheduler) processAccount(ctx context.Context, account db.ListAccountsForRefreshRow) error {
	release, outcome, err := s.acquirer.Acquire(ctx, account.TenantID, account.ID)
	if err != nil {
		_ = s.recordAudit(ctx, account, auth.OutcomeStormBudgetExhausted, "account", err)
		return err
	}
	if outcome != "" || release == nil {
		if outcome == "" {
			outcome = auth.OutcomeStormBudgetExhausted
		}
		return s.recordAudit(ctx, account, outcome, "account", nil)
	}
	defer release()
	if err := s.refreshWithBackoff(ctx, account); err != nil {
		return errors.Join(err, s.recordAudit(ctx, account, auth.OutcomePermanentDisable, "", err))
	}
	return s.recordAudit(ctx, account, auth.OutcomeRefreshSucceeded, "", nil)
}

func (s *Scheduler) refreshWithBackoff(ctx context.Context, account db.ListAccountsForRefreshRow) error {
	var last error
	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
		if aware, ok := s.Refresher.(ProviderAwareRefresher); ok {
			last = aware.RefreshForProvider(ctx, account.ProviderID, account.ID)
		} else {
			last = s.Refresher.Refresh(ctx, account.ID)
		}
		if last == nil || attempt == s.maxAttempts {
			return last
		}
		if err := s.sleep(ctx, s.backoff(attempt)); err != nil {
			return err
		}
	}
	return last
}

func defaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 5 {
		attempt = 5
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
