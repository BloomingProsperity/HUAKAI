package credentialworker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestSchedulerTickTriggersRefresh(t *testing.T) {
	ticks := make(chan time.Time, 1)
	called := make(chan int64, 1)
	ref := &refresherSpy{called: called}
	s := newTestScheduler([]db.ListAccountsForRefreshRow{testAccount(11)}, &stormSpy{}, ref, WithTickChannel(ticks))

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

func TestSchedulerStormRejectsSkipsRefresh(t *testing.T) {
	storm := &stormSpy{outcome: auth.OutcomeStormBudgetExhausted}
	ref := &refresherSpy{}
	audit := &auditSpy{}
	ledger := &ledgerSpy{}
	s := newTestScheduler([]db.ListAccountsForRefreshRow{testAccount(12)}, storm, ref, withAuditWriter(audit), WithAuditLedger(ledger))

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

func TestSchedulerRefreshSuccessWritesAudit(t *testing.T) {
	storm := &stormSpy{}
	audit := &auditSpy{}
	ledger := &ledgerSpy{}
	s := newTestScheduler([]db.ListAccountsForRefreshRow{testAccount(13)}, storm, &refresherSpy{}, withAuditWriter(audit), WithAuditLedger(ledger))

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
	s := newTestScheduler([]db.ListAccountsForRefreshRow{testAccount(14)}, &stormSpy{}, ref,
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

func testAccount(id int64) db.ListAccountsForRefreshRow {
	return db.ListAccountsForRefreshRow{ID: id, TenantID: 7, ProviderID: 99}
}

func newTestScheduler(rows []db.ListAccountsForRefreshRow, storm *stormSpy, ref *refresherSpy, opts ...Option) *Scheduler {
	return newTestSchedulerWith(&listSpy{rows: rows}, storm, ref, opts...)
}

func newTestSchedulerWith(l *listSpy, storm *stormSpy, ref *refresherSpy, opts ...Option) *Scheduler {
	base := []Option{withRefreshQueries(l), withStormAcquirer(storm), withAuditWriter(&auditSpy{}), WithAuditLedger(&ledgerSpy{})}
	return NewScheduler(nil, nil, nil, ref, append(base, opts...)...)
}

type listSpy struct {
	rows   []db.ListAccountsForRefreshRow
	calls  int
	before time.Time
	limit  int32
}

func (l *listSpy) ListAccountsForRefresh(_ context.Context, arg db.ListAccountsForRefreshParams) ([]db.ListAccountsForRefreshRow, error) {
	l.calls++
	l.before = arg.RefreshBefore.Time
	l.limit = arg.Limit
	return l.rows, nil
}

type stormSpy struct {
	outcome  auth.Outcome
	calls    int
	released int
}

func (s *stormSpy) Acquire(context.Context, int64, int64) (func(), auth.Outcome, error) {
	s.calls++
	if s.outcome != "" {
		return nil, s.outcome, nil
	}
	return func() { s.released++ }, "", nil
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

func (l *ledgerSpy) Append(_ context.Context, e auditledger.LedgerEntry) (auditledger.LedgerEntry, error) {
	l.entries = append(l.entries, e)
	return e, nil
}

func (l *ledgerSpy) GetByRequestID(context.Context, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
}

func (l *ledgerSpy) LatestMerkleRoot(context.Context) ([32]byte, error) {
	return auditledger.ZeroRoot, nil
}
func (l *ledgerSpy) Size(context.Context) int {
	return len(l.entries)
}
