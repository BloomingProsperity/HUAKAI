package credentialworker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	DefaultInterval      = 60 * time.Second
	DefaultWarningWindow = 15 * time.Minute
	DefaultAccountLimit  = int32(100)
	DefaultMaxAttempts   = 3
)

// Scheduler 主动扫描快过期的 provider account，并通过 storm controller 限流刷新。
type Scheduler struct {
	Queries         *dbbilling.Queries
	StormController *auth.StormController
	Signer          Signer

	Refresher   Refresher
	AuditLedger auditledger.Ledger

	interval         time.Duration
	warningWindow    time.Duration
	limit            int32
	maxAttempts      int
	refreshTimeout   time.Duration
	backoff          func(attempt int) time.Duration
	sleep            func(context.Context, time.Duration) error
	now              func() time.Time
	ticks            <-chan time.Time
	vendorRefreshers map[string]Refresher

	queryer      refreshQueries
	acquirer     stormAcquirer
	auditWriter  auth.AuditWriter
	healthPolicy ProviderAccountHealthPolicy
	healthStore  providerAccountHealthStore

	// alertDeliverer fires the operator alert when a health transition raised the
	// Alert flag (CRED-293). nil-safe: nil means alerts are log-only. alertAsync
	// wraps the detached send so production goroutines but tests run inline.
	alertDeliverer ProviderAccountDownDeliverer
	alertAsync     func(func())

	// 同事务路径 (RR-W5-002):txPool + auditSigner + auditQueries 全配齐时
	// recordAudit 走 BeginFunc;production wiring 必须 gate 三件套都装。
	txPool       *pgxpool.Pool
	auditSigner  any
	auditQueries *dbauth.Queries

	// CRED-288: scheduled credential-rotation-due scan, run after the refresh
	// pass each tick. OFF unless rotationStore is set AND rotationMaxAge > 0
	// (opt-in via WithRotationScan) — so existing deployments are unaffected.
	rotationStore  RotationStore
	rotationMaxAge time.Duration
	rotationAlert  RotationAlert
	rotationLimit  int

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	seq    atomic.Int64
}

func NewScheduler(queries *dbbilling.Queries, storm *auth.StormController, signer Signer, refresher Refresher, opts ...Option) *Scheduler {
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
		healthPolicy:    DefaultProviderAccountHealthPolicy(),
	}
	if queries != nil {
		s.queryer = queries
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
	accounts, err := s.queryer.ListAccountsForRefresh(ctx, dbbilling.ListAccountsForRefreshParams{
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
	// CRED-288: after the refresh pass, flag credentials past their rotation
	// max-age. No-op unless opt-in (WithRotationScan); a scan error joins the
	// tick error without aborting the refresh work already done above.
	if _, err := ScanRotationDue(ctx, s.rotationStore, s.rotationAlert, s.rotationMaxAge, s.now(), s.rotationLimit); err != nil {
		out = errors.Join(out, err)
	}
	return out
}

// RotationScanConfigForTest 暴露 CRED-288 轮换扫描装配后的 store/maxAge/limit,
// 仅供测试断言"WithRotationScan option 是否真把扫描装上"(生产 wiring 的 gating 决策
// 曾经缺失导致死开关)。生产代码不读它。
func (s *Scheduler) RotationScanConfigForTest() (RotationStore, time.Duration, int) {
	return s.rotationStore, s.rotationMaxAge, s.rotationLimit
}

func (s *Scheduler) RefreshHotPath(ctx context.Context, tenantID, accountID int64, vendorName string) error {
	switch {
	case s == nil:
		return errors.New("credentialworker: scheduler missing")
	case s.acquirer == nil:
		return errors.New("credentialworker: storm controller required")
	case s.Refresher == nil:
		return errors.New("credentialworker: refresher required")
	case tenantID == 0 || accountID == 0:
		return errors.New("credentialworker: hot refresh requires tenant and account")
	}
	return s.processAccount(ctx, dbbilling.ListAccountsForRefreshRow{
		ID:         accountID,
		TenantID:   tenantID,
		VendorName: vendorName,
	})
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

func (s *Scheduler) processAccount(ctx context.Context, account dbbilling.ListAccountsForRefreshRow) error {
	// Scope 1 (account): durable DB concurrency slot; released after the attempt.
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

	// Scope 2 (provider-endpoint): in-memory per-vendor-endpoint rate budget.
	// The vendor name keys the shared OAuth token endpoint, so many accounts of
	// the same vendor expiring at once cannot stampede it.
	endpointKey := normalizeProviderName(account.VendorName)
	endpointRefund, outcome, err := s.acquirer.AcquireProviderEndpoint(ctx, account.TenantID, endpointKey, "")
	if err != nil {
		return errors.Join(err, s.recordAudit(ctx, account, auth.OutcomeStormBudgetExhausted, "provider_endpoint", err))
	}
	if outcome != "" {
		return s.recordAudit(ctx, account, outcome, "provider_endpoint", nil)
	}

	// Scope 3 (global): in-memory process-wide rate budget, last-resort cap.
	_, outcome, err = s.acquirer.AcquireGlobal(ctx, account.TenantID)
	if err != nil {
		endpointRefund()
		return errors.Join(err, s.recordAudit(ctx, account, auth.OutcomeStormBudgetExhausted, "global", err))
	}
	if outcome != "" {
		// Refund the endpoint token: this attempt never ran, so it must not
		// consume the endpoint budget (A07: refund only on a downstream scope
		// denial, never on a failed refresh).
		endpointRefund()
		return s.recordAudit(ctx, account, outcome, "global", nil)
	}

	// All three scopes admitted. Endpoint/global tokens stay consumed regardless
	// of the refresh outcome — a failed attempt must not reopen the storm window.
	if err := s.refreshWithBackoff(ctx, account); err != nil {
		if outcome := auth.RefreshAuditOutcomeFromError(err); outcome != "" {
			return errors.Join(err, s.recordAuditString(ctx, account, outcome, "", err))
		}
		return errors.Join(err, s.recordAudit(ctx, account, auth.OutcomePermanentDisable, "", err))
	}
	return s.recordAudit(ctx, account, auth.OutcomeRefreshSucceeded, "", nil)
}

func (s *Scheduler) refreshWithBackoff(ctx context.Context, account dbbilling.ListAccountsForRefreshRow) error {
	var last error
	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
		refresher := s.refresherForAccount(account)
		attemptCtx := ctx
		var cancel context.CancelFunc
		if s.refreshTimeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, s.refreshTimeout)
		}
		if aware, ok := refresher.(ProviderAwareRefresher); ok {
			last = aware.RefreshForProvider(attemptCtx, account.ProviderID, account.ID)
		} else {
			last = refresher.Refresh(attemptCtx, account.ID)
		}
		if cancel != nil {
			cancel()
		}
		if last == nil || attempt == s.maxAttempts {
			return last
		}
		if !refreshErrorRetryable(last) {
			return last
		}
		if err := s.sleep(ctx, s.backoff(attempt)); err != nil {
			return err
		}
	}
	return last
}

func (s *Scheduler) refresherForAccount(account dbbilling.ListAccountsForRefreshRow) Refresher {
	vendor := normalizeProviderName(account.VendorName)
	if vendor != "" && s.vendorRefreshers != nil {
		if refresher := s.vendorRefreshers[vendor]; refresher != nil {
			return refresher
		}
	}
	return s.Refresher
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

type refreshRetryClassifier interface {
	RetryableRefresh() bool
}

func refreshErrorRetryable(err error) bool {
	if err == nil {
		return false
	}
	var classified refreshRetryClassifier
	if errors.As(err, &classified) {
		return classified.RetryableRefresh()
	}
	return true
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
