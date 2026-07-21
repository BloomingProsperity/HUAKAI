package credentialworker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

func TestSchedulerTickTriggersRefresh(t *testing.T) {
	ticks := make(chan time.Time, 1)
	called := make(chan int64, 1)
	ref := &refresherSpy{called: called}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccount(11)}, &stormSpy{}, ref, WithTickChannel(ticks))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ticks <- time.Now()
	select {
	case got := <-called:
		if got != 11 {
			t.Fatalf("refreshed account=%d, want 11", got)
		}
	case <-time.After(time.Second):
		t.Fatal("tick did not trigger refresh")
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestSchedulerNoCandidatesNoop(t *testing.T) {
	lister := &listSpy{}
	storm := &stormSpy{}
	ref := &refresherSpy{}
	s := newTestSchedulerWith(lister, storm, ref)
	fixed := time.Date(2026, 5, 14, 1, 2, 3, 0, time.UTC)
	s.now = func() time.Time { return fixed }

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if lister.calls != 1 || !lister.before.Equal(fixed.Add(DefaultWarningWindow)) || lister.limit != DefaultAccountLimit {
		t.Fatalf("bad scan args: calls=%d before=%s limit=%d", lister.calls, lister.before, lister.limit)
	}
	if storm.calls != 0 || len(ref.calls) != 0 {
		t.Fatalf("noop scan must not acquire/refresh: storm=%d refresh=%d", storm.calls, len(ref.calls))
	}
}

func TestSchedulerRejectsMissingCanonicalRefresher(t *testing.T) {
	s := NewScheduler(nil, nil, nil, nil,
		withRefreshQueries(&listSpy{}),
		withStormAcquirer(&stormSpy{}),
	)
	if err := s.RunOnce(context.Background()); err == nil || !strings.Contains(err.Error(), "refresher required") {
		t.Fatalf("RunOnce err=%v，期望缺少统一刷新入口时启动失败", err)
	}
}

func TestSchedulerStormRejectsSkipsRefresh(t *testing.T) {
	storm := &stormSpy{outcome: auth.OutcomeStormBudgetExhausted}
	ref := &refresherSpy{}
	audit := &auditSpy{}
	ledger := &ledgerSpy{}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccount(12)}, storm, ref, withAuditWriter(audit), WithAuditLedger(ledger))

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(ref.calls) != 0 {
		t.Fatalf("refresh called despite storm skip: %v", ref.calls)
	}
	if got := audit.lastOutcome(); got != auth.OutcomeStormBudgetExhausted {
		t.Fatalf("audit outcome=%q, want storm budget exhausted", got)
	}
	if ledger.Size(context.Background()) != 1 {
		t.Fatalf("ledger entries=%d, want 1", ledger.Size(context.Background()))
	}
}

func TestSchedulerEndpointStormDenialSkipsRefreshAndAuditsScope(t *testing.T) {
	// 修掉的回归:一次 endpoint-scope 的 storm 拒绝必须跳过刷新、释放 account 槽位、
	// 不去咨询 global scope,并把该拒绝按 "provider_endpoint" scope(而非 "account")
	// 记入 audit。Mutation:删掉 processAccount 中的 endpoint acquire,或把它误标为
	// "account" → scope/skip/short-circuit 这些断言会转红。
	storm := &stormSpy{endpointOutcome: auth.OutcomeStormBudgetExhausted}
	ref := &refresherSpy{}
	audit := &auditSpy{}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(31, "anthropic")}, storm, ref,
		withAuditWriter(audit), WithAuditLedger(&ledgerSpy{}))

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(ref.calls) != 0 {
		t.Fatalf("refresh called despite endpoint storm denial: %v", ref.calls)
	}
	if storm.released != 1 {
		t.Fatalf("account slot released=%d, want 1 (slot must free on endpoint denial)", storm.released)
	}
	if storm.globalCalls != 0 {
		t.Fatalf("global scope acquired=%d, want 0 (endpoint denial must short-circuit before global)", storm.globalCalls)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(audit.entries))
	}
	if got := audit.entries[0]; got.Outcome != auth.OutcomeStormBudgetExhausted || got.StormScope != "provider_endpoint" {
		t.Fatalf("audit outcome=%q scope=%q, want storm_budget_exhausted / provider_endpoint", got.Outcome, got.StormScope)
	}
}

func TestSchedulerGlobalStormDenialRefundsEndpointAndAuditsScope(t *testing.T) {
	// 修掉的回归:一次 global-scope 的拒绝必须退还已被消耗的 endpoint token(一次
	// 从未运行的尝试不能浪费 endpoint 预算)、跳过刷新,并按 "global" scope 记入 audit。
	// Mutation:删掉 global-deny 分支上的 endpointRefund() 调用 → endpointRefunds 保持
	// 为 0 → 转红;改写 audit scope 标签 → scope 断言转红。
	storm := &stormSpy{globalOutcome: auth.OutcomeStormBudgetExhausted}
	ref := &refresherSpy{}
	audit := &auditSpy{}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(32, "anthropic")}, storm, ref,
		withAuditWriter(audit), WithAuditLedger(&ledgerSpy{}))

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(ref.calls) != 0 {
		t.Fatalf("refresh called despite global storm denial: %v", ref.calls)
	}
	if storm.endpointCalls != 1 || storm.endpointRefunds != 1 {
		t.Fatalf("endpoint acquire/refund = %d/%d, want 1/1 (global denial must refund the endpoint token)", storm.endpointCalls, storm.endpointRefunds)
	}
	if storm.released != 1 {
		t.Fatalf("account slot released=%d, want 1", storm.released)
	}
	if got := audit.entries[len(audit.entries)-1]; got.Outcome != auth.OutcomeStormBudgetExhausted || got.StormScope != "global" {
		t.Fatalf("audit outcome=%q scope=%q, want storm_budget_exhausted / global", got.Outcome, got.StormScope)
	}
}

func TestSchedulerAllScopesAdmitConsultEachOnceThenRefresh(t *testing.T) {
	// 修掉的回归:happy path 必须把三个 scope(account、endpoint、global)各咨询恰好
	// 一次然后刷新;一次已完成的尝试绝不能退还 endpoint token(其预算保持被消耗,
	// 使一次成功无法重新打开 storm 窗口)。Mutation:跳过 endpoint 或 global 的
	// acquire → 其调用计数降为 0 → 转红。
	storm := &stormSpy{}
	ref := &refresherSpy{}
	audit := &auditSpy{}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(33, "anthropic")}, storm, ref,
		withAuditWriter(audit), WithAuditLedger(&ledgerSpy{}))

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if storm.calls != 1 || storm.endpointCalls != 1 || storm.globalCalls != 1 {
		t.Fatalf("scope calls account/endpoint/global = %d/%d/%d, want 1/1/1", storm.calls, storm.endpointCalls, storm.globalCalls)
	}
	if len(ref.calls) != 1 || ref.calls[0] != 33 {
		t.Fatalf("refresh calls=%v, want [33]", ref.calls)
	}
	if storm.endpointRefunds != 0 {
		t.Fatalf("endpoint refunds=%d, want 0 on success (consumed token must persist)", storm.endpointRefunds)
	}
	if got := audit.lastOutcome(); got != auth.OutcomeRefreshSucceeded {
		t.Fatalf("audit outcome=%q, want refresh_succeeded", got)
	}
}

func TestSchedulerHotPathUsesStormScopesAndCanonicalRefresher(t *testing.T) {
	// gateway 热路径必须经过 storm 准入，再交给统一刷新入口按凭据模式分派。
	storm := &stormSpy{}
	canonicalRef := &refresherSpy{}
	audit := &auditSpy{}
	lister := &listSpy{}
	s := newTestSchedulerWith(lister, storm, canonicalRef,
		withAuditWriter(audit),
		WithAuditLedger(&ledgerSpy{}),
	)

	if err := s.RefreshHotPath(context.Background(), 77, 44, "anthropic"); err != nil {
		t.Fatalf("RefreshHotPath: %v", err)
	}
	if lister.calls != 0 {
		t.Fatalf("hot path must not scan scheduled candidates; lister calls=%d", lister.calls)
	}
	if storm.calls != 1 || storm.endpointCalls != 1 || storm.globalCalls != 1 {
		t.Fatalf("storm scopes account/endpoint/global=%d/%d/%d want 1/1/1", storm.calls, storm.endpointCalls, storm.globalCalls)
	}
	if len(canonicalRef.calls) != 1 || canonicalRef.calls[0] != 44 {
		t.Fatalf("canonical refresher calls=%v want [44]", canonicalRef.calls)
	}
	if storm.released != 1 {
		t.Fatalf("account storm release calls=%d want 1", storm.released)
	}
	if got := audit.lastOutcome(); got != auth.OutcomeRefreshSucceeded {
		t.Fatalf("audit outcome=%q want refresh_succeeded", got)
	}
}

func TestSchedulerRefreshSuccessWritesAudit(t *testing.T) {
	storm := &stormSpy{}
	audit := &auditSpy{}
	ledger := &ledgerSpy{}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccount(13)}, storm, &refresherSpy{}, withAuditWriter(audit), WithAuditLedger(ledger))

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := audit.lastOutcome(); got != auth.OutcomeRefreshSucceeded {
		t.Fatalf("audit outcome=%q, want refresh_succeeded", got)
	}
	if storm.released != 1 {
		t.Fatalf("release calls=%d, want 1", storm.released)
	}
	if ledger.Size(context.Background()) != 1 {
		t.Fatalf("ledger entries=%d, want 1", ledger.Size(context.Background()))
	}
}

func TestSchedulerRefreshFailureBackoffAndAudit(t *testing.T) {
	fail := errors.New("access_token=sk-ABCDEFGH refresh failed")
	ref := &refresherSpy{errs: []error{fail, fail, fail}}
	audit := &auditSpy{}
	var delays []time.Duration
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccount(14)}, &stormSpy{}, ref,
		WithMaxAttempts(3),
		WithBackoff(func(attempt int) time.Duration { return time.Duration(attempt) * time.Second }),
		withSleep(func(_ context.Context, d time.Duration) error {
			delays = append(delays, d)
			return nil
		}),
		withAuditWriter(audit),
		WithAuditLedger(&ledgerSpy{}),
	)

	if err := s.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce must return refresh error")
	}
	if len(ref.calls) != 3 {
		t.Fatalf("refresh attempts=%d, want 3", len(ref.calls))
	}
	if len(delays) != 2 || delays[0] != time.Second || delays[1] != 2*time.Second {
		t.Fatalf("backoff delays=%v, want [1s 2s]", delays)
	}
	if got := audit.lastOutcome(); got != auth.OutcomePermanentDisable {
		t.Fatalf("audit outcome=%q, want permanent_disable", got)
	}
	if strings.Contains(audit.lastError(), "sk-ABCDEFGH") {
		t.Fatalf("audit error leaked token: %q", audit.lastError())
	}
}

func TestWithRefreshTimeoutBoundsEachAttempt(t *testing.T) {
	ref := &blockingRefresher{entered: make(chan struct{})}
	timeout := 50 * time.Millisecond
	s := NewScheduler(nil, nil, nil, ref, WithRefreshTimeout(timeout), WithMaxAttempts(1))
	if s.refreshTimeout != timeout {
		t.Fatalf("refreshTimeout=%s, want %s", s.refreshTimeout, timeout)
	}

	done := make(chan error, 1)
	started := time.Now()
	go func() {
		done <- s.refreshWithBackoff(context.Background(), testAccount(41))
	}()

	select {
	case <-ref.entered:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("refresher was not called")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("refreshWithBackoff err=%v, want context deadline exceeded", err)
		}
		if elapsed := time.Since(started); elapsed < timeout/2 || elapsed > 500*time.Millisecond {
			t.Fatalf("refreshWithBackoff elapsed=%s, want near %s", elapsed, timeout)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("refreshWithBackoff ignored refreshTimeout and did not return")
	}
}

func TestRefreshTimeoutDefaultDoesNotInjectDeadline(t *testing.T) {
	ref := &deadlineProbeRefresher{}
	s := NewScheduler(nil, nil, nil, ref, WithRefreshTimeout(0), WithRefreshTimeout(-time.Second), WithMaxAttempts(1))
	if s.refreshTimeout != 0 {
		t.Fatalf("refreshTimeout=%s, want default zero", s.refreshTimeout)
	}
	if err := s.refreshWithBackoff(context.Background(), testAccount(42)); err != nil {
		t.Fatalf("refreshWithBackoff: %v", err)
	}
	if ref.hadDeadline {
		t.Fatal("default refreshTimeout must not wrap refresh attempts with a deadline")
	}
}

func TestSchedulerStopsBackoffLoopForNonRetryableRefreshError(t *testing.T) {
	// 修掉的回归:对当前 tick 已把一次失败分类为终态的 vendor refresher,绝不能被
	// 通用重试循环再次调用。Mutation 自检:删掉 RetryableRefresh() 的处理会让本测试
	// 把 refresher 调用三次。
	ref := &refresherSpy{errs: []error{nonRetryableRefreshErr{}}}
	audit := &auditSpy{}
	var delays []time.Duration
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccount(15)}, &stormSpy{}, ref,
		WithMaxAttempts(3),
		WithBackoff(func(attempt int) time.Duration { return time.Duration(attempt) * time.Second }),
		withSleep(func(_ context.Context, d time.Duration) error {
			delays = append(delays, d)
			return nil
		}),
		withAuditWriter(audit),
		WithAuditLedger(&ledgerSpy{}),
	)

	if err := s.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce must return refresh error")
	}
	if len(ref.calls) != 1 {
		t.Fatalf("refresh attempts=%d, want 1", len(ref.calls))
	}
	if len(delays) != 0 {
		t.Fatalf("non-retryable refresh must not sleep/retry, delays=%v", delays)
	}
	if got := audit.lastOutcome(); got != auth.OutcomePermanentDisable {
		t.Fatalf("audit outcome=%q, want permanent_disable", got)
	}
}

func TestSchedulerPersistenceUncertaintyOverridesRetryableRemoteError(t *testing.T) {
	ref := &refresherSpy{errs: []error{errors.Join(retryableRefreshErr{}, remoteRetrySuppressedErr{})}}
	delays := []time.Duration{}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccount(31)}, &stormSpy{}, ref,
		WithMaxAttempts(3), WithBackoff(func(attempt int) time.Duration { return time.Duration(attempt) }),
		withSleep(func(_ context.Context, d time.Duration) error {
			delays = append(delays, d)
			return nil
		}),
	)
	if err := s.RunOnce(context.Background()); err == nil {
		t.Fatal("持久化结果不确定必须向上返回错误")
	}
	if len(ref.calls) != 1 || len(delays) != 0 {
		t.Fatalf("持久化结果不确定后仍重试远端：calls=%v delays=%v", ref.calls, delays)
	}
}

func TestRecoverAgentTaskAcquiresAccountLeaseBeforeRecovery(t *testing.T) {
	storm := &stormSpy{}
	recoverer := &leaseAwareAgentRecoverer{}
	s := NewScheduler(nil, nil, nil, recoverer,
		withStormAcquirer(storm), withAuditWriter(&auditSpy{}), WithAuditLedger(&ledgerSpy{}),
	)
	if err := s.RecoverAgentTask(context.Background(), 7, 42, 3); err != nil {
		t.Fatalf("RecoverAgentTask: %v", err)
	}
	if !recoverer.called || storm.calls != 1 || storm.released != 1 || storm.endpointCalls != 1 || storm.globalCalls != 1 {
		t.Fatalf("恢复链路未完整经过账号租约：called=%v storm=%+v", recoverer.called, storm)
	}
}

func TestSchedulerStopGracefully(t *testing.T) {
	ticks := make(chan time.Time)
	s := newTestScheduler(nil, &stormSpy{}, &refresherSpy{}, WithTickChannel(ticks))
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func testAccount(id int64) dbbilling.ListAccountsForRefreshRow {
	return dbbilling.ListAccountsForRefreshRow{ID: id, TenantID: 7, ProviderID: 99}
}

func testAccountWithVendor(id int64, vendor string) dbbilling.ListAccountsForRefreshRow {
	row := testAccount(id)
	row.VendorName = vendor
	return row
}

func newTestScheduler(rows []dbbilling.ListAccountsForRefreshRow, storm *stormSpy, ref *refresherSpy, opts ...Option) *Scheduler {
	return newTestSchedulerWith(&listSpy{rows: rows}, storm, ref, opts...)
}

func newTestSchedulerWith(l *listSpy, storm *stormSpy, ref *refresherSpy, opts ...Option) *Scheduler {
	base := []Option{withRefreshQueries(l), withStormAcquirer(storm), withAuditWriter(&auditSpy{}), WithAuditLedger(&ledgerSpy{})}
	return NewScheduler(nil, nil, nil, ref, append(base, opts...)...)
}

type listSpy struct {
	rows   []dbbilling.ListAccountsForRefreshRow
	calls  int
	before time.Time
	limit  int32
}

func (l *listSpy) ListAccountsForRefresh(_ context.Context, arg dbbilling.ListAccountsForRefreshParams) ([]dbbilling.ListAccountsForRefreshRow, error) {
	l.calls++
	l.before = arg.RefreshBefore.Time
	l.limit = arg.LimitCount
	return l.rows, nil
}

type stormSpy struct {
	outcome         auth.Outcome // account-scope 拒绝结果("" = 放行)
	endpointOutcome auth.Outcome // provider-endpoint 拒绝结果("" = 放行)
	globalOutcome   auth.Outcome // global-scope 拒绝结果("" = 放行)
	calls           int
	released        int
	endpointCalls   int
	globalCalls     int
	endpointRefunds int
}

func (s *stormSpy) Acquire(context.Context, int64, int64) (func(), auth.Outcome, error) {
	s.calls++
	if s.outcome != "" {
		return nil, s.outcome, nil
	}
	return func() { s.released++ }, "", nil
}

func (s *stormSpy) AcquireProviderEndpoint(context.Context, int64, string, string) (func(), auth.Outcome, error) {
	s.endpointCalls++
	if s.endpointOutcome != "" {
		return nil, s.endpointOutcome, nil
	}
	return func() { s.endpointRefunds++ }, "", nil
}

func (s *stormSpy) AcquireGlobal(context.Context, int64) (func(), auth.Outcome, error) {
	s.globalCalls++
	if s.globalOutcome != "" {
		return nil, s.globalOutcome, nil
	}
	return func() {}, "", nil
}

type refresherSpy struct {
	calls  []int64
	errs   []error
	called chan int64
}

func (r *refresherSpy) Refresh(_ context.Context, accountID int64) error {
	r.calls = append(r.calls, accountID)
	if r.called != nil {
		r.called <- accountID
	}
	if n := len(r.calls); n <= len(r.errs) {
		return r.errs[n-1]
	}
	return nil
}

type blockingRefresher struct {
	entered chan struct{}
}

func (r *blockingRefresher) Refresh(ctx context.Context, accountID int64) error {
	close(r.entered)
	<-ctx.Done()
	return ctx.Err()
}

type deadlineProbeRefresher struct {
	hadDeadline bool
}

func (r *deadlineProbeRefresher) Refresh(ctx context.Context, accountID int64) error {
	_, r.hadDeadline = ctx.Deadline()
	return nil
}

type nonRetryableRefreshErr struct{}

func (nonRetryableRefreshErr) Error() string { return "rate_limit_exceeded" }

func (nonRetryableRefreshErr) RetryableRefresh() bool { return false }

type retryableRefreshErr struct{}

func (retryableRefreshErr) Error() string          { return "remote temporary failure" }
func (retryableRefreshErr) RetryableRefresh() bool { return true }

type remoteRetrySuppressedErr struct{}

func (remoteRetrySuppressedErr) Error() string             { return "refresh persistence uncertain" }
func (remoteRetrySuppressedErr) SuppressRemoteRetry() bool { return true }
func (remoteRetrySuppressedErr) RetryableRefresh() bool    { return false }

type leaseAwareAgentRecoverer struct {
	called bool
}

func (r *leaseAwareAgentRecoverer) Refresh(context.Context, int64) error { return nil }

func (r *leaseAwareAgentRecoverer) RecoverAgentTask(ctx context.Context, _ int64, accountID int64, _ int) error {
	if err := auth.RequireRefreshAccountLease(ctx, accountID); err != nil {
		return err
	}
	r.called = true
	return nil
}

type auditSpy struct {
	entries []auth.RefreshAuditEntry
}

func (a *auditSpy) WriteRefreshAudit(_ context.Context, e *auth.RefreshAuditEntry) error {
	if e != nil {
		a.entries = append(a.entries, *e)
	}
	return nil
}

func (a *auditSpy) lastOutcome() auth.Outcome {
	if len(a.entries) == 0 {
		return ""
	}
	return a.entries[len(a.entries)-1].Outcome
}

func (a *auditSpy) lastError() string {
	if len(a.entries) == 0 {
		return ""
	}
	return a.entries[len(a.entries)-1].ErrorMessageRedacted
}

type ledgerSpy struct {
	entries []auditledger.LedgerEntry
}

func (l *ledgerSpy) Append(_ context.Context, e auditledger.PreparedEntry) (auditledger.LedgerEntry, error) {
	entry := e.AsLedgerEntry()
	l.entries = append(l.entries, entry)
	return entry, nil
}

func (l *ledgerSpy) GetByRequestID(context.Context, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
}

func (l *ledgerSpy) GetByRequestIDAndTenantScope(context.Context, string, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
}

func (l *ledgerSpy) LatestMerkleRoot(context.Context) ([32]byte, error) {
	return auditledger.ZeroRoot, nil
}
func (l *ledgerSpy) Size(context.Context) int {
	return len(l.entries)
}
