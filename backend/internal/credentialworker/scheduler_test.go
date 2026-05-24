package credentialworker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/copilot"
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

func TestSchedulerVendorRefresherRoutesOnlyMatchingVendor(t *testing.T) {
	// Regression killed: vendor-specific refreshers must dispatch by the
	// scanned vendor name, not by "first registered refresher". Mutation
	// self-check: routing the anthropic row to the copilot refresher leaves the
	// default refresher without account 22 and this test turns red.
	copilotRef := &refresherSpy{}
	defaultRef := &refresherSpy{}
	audit := &auditSpy{}
	rows := []dbbilling.ListAccountsForRefreshRow{
		testAccountWithVendor(21, "copilot"),
		testAccountWithVendor(22, "anthropic"),
	}
	s := newTestScheduler(rows, &stormSpy{}, defaultRef,
		WithVendorRefresher("copilot", copilotRef),
		withAuditWriter(audit),
		WithAuditLedger(&ledgerSpy{}),
	)

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(copilotRef.calls) != 1 || copilotRef.calls[0] != 21 {
		t.Fatalf("copilot refresher calls=%v, want [21]", copilotRef.calls)
	}
	if len(defaultRef.calls) != 1 || defaultRef.calls[0] != 22 {
		t.Fatalf("default refresher calls=%v, want fallback [22]", defaultRef.calls)
	}
	if len(audit.entries) != 2 {
		t.Fatalf("audit entries=%d, want 2 success rows", len(audit.entries))
	}
	for _, entry := range audit.entries {
		if entry.Outcome != auth.OutcomeRefreshSucceeded {
			t.Fatalf("audit outcome for account %d=%q, want refresh_succeeded", entry.ProviderAccountID, entry.Outcome)
		}
	}
}

func TestSchedulerFallsBackToDefaultRefresherAndWritesAuditWhenVendorRefresherMissing(t *testing.T) {
	// Regression killed: a vendor without a dedicated refresher must keep the
	// legacy refresh path and still write the refresh audit row. Mutation
	// self-check: sending this anthropic row to the registered copilot refresher
	// returns crossVendorErr instead of the success audit evidence below.
	crossVendorErr := errors.New("copilot refresher received non-copilot account")
	copilotRef := &refresherSpy{errs: []error{crossVendorErr}}
	defaultRef := &refresherSpy{}
	audit := &auditSpy{}
	ledger := &ledgerSpy{}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(23, "anthropic")}, &stormSpy{}, defaultRef,
		WithVendorRefresher("copilot", copilotRef),
		withAuditWriter(audit),
		WithAuditLedger(ledger),
	)

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(copilotRef.calls) != 0 {
		t.Fatalf("copilot refresher must not receive anthropic account: %v", copilotRef.calls)
	}
	if len(defaultRef.calls) != 1 || defaultRef.calls[0] != 23 {
		t.Fatalf("default refresher calls=%v, want [23]", defaultRef.calls)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("audit entries=%d, want fallback success row", len(audit.entries))
	}
	entry := audit.entries[0]
	if entry.ProviderAccountID != 23 || entry.Outcome != auth.OutcomeRefreshSucceeded {
		t.Fatalf("audit entry=(account=%d outcome=%q), want account 23 refresh_succeeded", entry.ProviderAccountID, entry.Outcome)
	}
	if ledger.Size(context.Background()) != 1 {
		t.Fatalf("ledger entries=%d, want 1", ledger.Size(context.Background()))
	}
}

func TestSchedulerCopilotVendorRefresherRecordsAuthExpiredOn401(t *testing.T) {
	// Regression killed: Scheduler vendor routing must actually execute
	// CopilotRefresher, preserving its 401 -> auth_expired classification.
	// Mutation self-check: falling back to the default refresher or swallowing
	// the Copilot error leaves no auth_expired sidecar evidence.
	store := &schedulerCopilotStore{raw: []byte(`{"github_access_token":"expired-github-token"}`)}
	client := &http.Client{Transport: schedulerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"message":"bad credentials"}`)),
		}, nil
	})}
	audit := &auditSpy{}
	refresher := &copilot.CopilotRefresher{
		Store: store,
		Adapter: copilot.CopilotRefreshAdapter{
			TokenURL:   "https://api.github.test/copilot_internal/v2/token",
			HTTPClient: client,
			Now:        func() time.Time { return time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC) },
		},
	}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(24, "copilot")}, &stormSpy{}, &refresherSpy{},
		WithMaxAttempts(1),
		WithVendorRefresher("copilot", refresher),
		withAuditWriter(audit),
		WithAuditLedger(&ledgerSpy{}),
	)

	err := s.RunOnce(context.Background())
	if !errors.Is(err, copilot.ErrCopilotAuthExpired) {
		t.Fatalf("RunOnce err=%v, want ErrCopilotAuthExpired", err)
	}
	if store.failureAccountID != 24 || store.failureOutcome != "auth_expired" {
		t.Fatalf("copilot failure hook=(account=%d outcome=%q), want account 24 auth_expired", store.failureAccountID, store.failureOutcome)
	}
	if len(store.saved) != 0 {
		t.Fatalf("expired Copilot auth must not save credential: %s", string(store.saved))
	}
	if got := audit.lastOutcome(); got != auth.OutcomePermanentDisable {
		t.Fatalf("scheduler audit outcome=%q, want permanent_disable", got)
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

type schedulerCopilotStore struct {
	raw              []byte
	saved            []byte
	failureAccountID int64
	failureOutcome   string
}

func (s *schedulerCopilotStore) LoadCopilotCredential(context.Context, int64) ([]byte, error) {
	return append([]byte(nil), s.raw...), nil
}

func (s *schedulerCopilotStore) SaveCopilotCredential(_ context.Context, _ int64, credential []byte, _ time.Time) error {
	s.saved = append([]byte(nil), credential...)
	return nil
}

func (s *schedulerCopilotStore) RecordCopilotRefreshFailure(_ context.Context, accountID int64, outcome string, _ error) error {
	s.failureAccountID = accountID
	s.failureOutcome = outcome
	return nil
}

type schedulerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f schedulerRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
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
