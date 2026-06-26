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

	// alertDeliverer 在一次 health 转换升起 Alert 标志时触发 operator 告警
	// (CRED-293)。nil-safe:nil 表示告警仅记录日志。alertAsync 包装脱离式发送,
	// 因此生产用 goroutine,而测试内联运行。
	alertDeliverer ProviderAccountDownDeliverer
	alertAsync     func(func())

	// 同事务路径 (RR-W5-002):txPool + auditSigner + auditQueries 全配齐时
	// recordAudit 走 BeginFunc;production wiring 必须 gate 三件套都装。
	txPool       *pgxpool.Pool
	auditSigner  any
	auditQueries *dbauth.Queries

	// CRED-288:定时的 credential-rotation-due 扫描,在每个 tick 的 refresh 过程之后
	// 运行。除非 rotationStore 已设置 且 rotationMaxAge > 0(经 WithRotationScan
	// opt-in),否则关闭——因此现有部署不受影响。
	rotationStore  RotationStore
	rotationMaxAge time.Duration
	rotationAlert  RotationAlert
	rotationLimit  int
	// CRED-288c:把每个到期凭据分类为可刷新(OAuth/session → 经 refresh 流自愈)
	// 或静态(api_key → 仅告警,绝不仅凭年龄就下线)。nil → 扫描时取
	// DefaultRefreshClassifier。
	rotationClassifier RefreshClassifier

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
	// CRED-288/288c:在 refresh 过程之后,把超过其 rotation max-age 的凭据路由进
	// 恢复(可刷新 → 经 refresh 流重新铸造;静态 → 仅告警)。除非 opt-in
	// (WithRotationScan)否则为 no-op;一个扫描错误会 join 进 tick 错误,而不会中止
	// 上面的 refresh 工作。
	if _, err := ScanRotationDue(ctx, s.rotationStore, s.rotationClassifier, s.rotationAlert, s.rotationMaxAge, s.now(), s.rotationLimit); err != nil {
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
	// Scope 1(account):持久化的 DB 并发槽位;在本次尝试之后释放。
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

	// Scope 2(provider-endpoint):内存中的 per-vendor-endpoint 速率预算。
	// vendor 名作为共享 OAuth token endpoint 的 key,因此同一 vendor 的大量账号同时
	// 过期时不会对它形成踩踏。
	endpointKey := normalizeProviderName(account.VendorName)
	endpointRefund, outcome, err := s.acquirer.AcquireProviderEndpoint(ctx, account.TenantID, endpointKey, "")
	if err != nil {
		return errors.Join(err, s.recordAudit(ctx, account, auth.OutcomeStormBudgetExhausted, "provider_endpoint", err))
	}
	if outcome != "" {
		return s.recordAudit(ctx, account, outcome, "provider_endpoint", nil)
	}

	// Scope 3(global):内存中进程范围的速率预算,作为最后兜底上限。
	_, outcome, err = s.acquirer.AcquireGlobal(ctx, account.TenantID)
	if err != nil {
		endpointRefund()
		return errors.Join(err, s.recordAudit(ctx, account, auth.OutcomeStormBudgetExhausted, "global", err))
	}
	if outcome != "" {
		// 退还 endpoint token:本次尝试从未运行,因此它不能消耗 endpoint 预算
		// (A07:只在下游 scope 拒绝时退还,绝不在刷新失败时退还)。
		endpointRefund()
		return s.recordAudit(ctx, account, outcome, "global", nil)
	}

	// 三个 scope 全部放行。无论刷新结果如何,endpoint/global token 都保持被消耗状态
	// ——一次失败的尝试绝不能重新打开 storm 窗口。
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
