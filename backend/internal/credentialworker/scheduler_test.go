package credentialworker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/copilot"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/cursor"
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

func TestSchedulerEndpointStormDenialSkipsRefreshAndAuditsScope(t *testing.T) {
	// Regression killed: an endpoint-scope storm denial must skip the
	// refresh, release the account slot, NOT consult the global scope, and audit
	// the denial under the "provider_endpoint" scope (not "account"). Mutation:
	// drop the endpoint acquire in processAccount, or mislabel it "account" → the
	// scope/skip/short-circuit assertions go red.
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
	// Regression killed: a global-scope denial must refund the already
	// consumed endpoint token (a never-run attempt must not waste the endpoint
	// budget), skip the refresh, and audit under the "global" scope. Mutation:
	// remove the endpointRefund() call on the global-deny branch → endpointRefunds
	// stays 0 → red; relabel the audit scope → the scope assertion goes red.
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
	// Regression killed: the happy path must consult all three scopes
	// (account, endpoint, global) exactly once and then refresh; a completed
	// attempt must NOT refund the endpoint token (its budget stays consumed so a
	// success cannot reopen the storm window). Mutation: skip the endpoint or
	// global acquire → its call counter drops to 0 → red.
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

func TestSchedulerHotPathUsesStormScopesAndVendorRefresher(t *testing.T) {
	// Mutation: wiring the gateway hot path directly to Refresher.Refresh skips
	// account/endpoint/global storm admission and routes through the wrong refresher.
	storm := &stormSpy{}
	defaultRef := &refresherSpy{}
	anthropicRef := &refresherSpy{}
	audit := &auditSpy{}
	lister := &listSpy{}
	s := newTestSchedulerWith(lister, storm, defaultRef,
		WithVendorRefresher("anthropic", anthropicRef),
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
	if len(anthropicRef.calls) != 1 || anthropicRef.calls[0] != 44 {
		t.Fatalf("anthropic refresher calls=%v want [44]", anthropicRef.calls)
	}
	if len(defaultRef.calls) != 0 {
		t.Fatalf("default refresher calls=%v want none for vendor-specific hot refresh", defaultRef.calls)
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

func TestSchedulerWithVendorRefresherRoutesCursorByVendorName(t *testing.T) {
	// Regression killed: cursor must route through the cursor-specific
	// refresher registered by WithVendorRefresher("cursor", ...). Mutation
	// self-check: changing the registration key or scheduler lookup to another
	// vendor leaves cursorRef unused and this test turns red.
	cursorRef := &refresherSpy{}
	windsurfRef := &refresherSpy{errs: []error{errors.New("cursor routed to windsurf")}}
	defaultRef := &refresherSpy{errs: []error{errors.New("cursor routed to default")}}
	audit := &auditSpy{}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(26, "cursor")}, &stormSpy{}, defaultRef,
		WithVendorRefresher("cursor", cursorRef),
		WithVendorRefresher("windsurf", windsurfRef),
		withAuditWriter(audit),
		WithAuditLedger(&ledgerSpy{}),
	)

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(cursorRef.calls) != 1 || cursorRef.calls[0] != 26 {
		t.Fatalf("cursor refresher calls=%v, want [26]", cursorRef.calls)
	}
	if len(windsurfRef.calls) != 0 {
		t.Fatalf("windsurf refresher must not receive cursor account: %v", windsurfRef.calls)
	}
	if len(defaultRef.calls) != 0 {
		t.Fatalf("default refresher must not receive configured cursor account: %v", defaultRef.calls)
	}
	if got := audit.lastOutcome(); got != auth.OutcomeRefreshSucceeded {
		t.Fatalf("audit outcome=%q, want refresh_succeeded", got)
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
	if got := audit.lastOutcome(); got != auth.RefreshAuditOutcome("auth_expired") {
		t.Fatalf("scheduler audit outcome=%q, want auth_expired", got)
	}
}

func TestSchedulerCursorVendorRefresherRecordsAuthExpiredOn401(t *testing.T) {
	// Regression killed: CursorRefresher must carry its 401 classification to
	// scheduler audit. Mutation self-check: returning the bare Cursor error
	// leaves no auth_expired sidecar, so scheduler writes permanent_disable.
	store := &schedulerCursorStore{rec: credentialstore.CredentialRecord{
		ID: 91, TenantID: 7, ProviderAccountID: 25,
		Vendor: "cursor", AuthMode: "oauth", CredentialVersion: 2,
		PlaintextPayload: []byte(`{"refresh_token":"expired-cursor-refresh"}`),
	}}
	client := &http.Client{Transport: schedulerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"bad_credentials"}`)),
		}, nil
	})}
	audit := &auditSpy{}
	refresher := &cursor.Refresher{
		Store: store,
		Adapter: cursor.RefreshAdapter{
			TokenURL:   "https://cursor-oauth.example.test/token",
			ClientID:   "cursor-client",
			HTTPClient: client,
			Now:        func() time.Time { return time.Date(2026, 5, 24, 10, 30, 0, 0, time.UTC) },
		},
		Now: func() time.Time { return time.Date(2026, 5, 24, 10, 30, 0, 0, time.UTC) },
	}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(25, "cursor")}, &stormSpy{}, &refresherSpy{},
		WithMaxAttempts(1),
		WithVendorRefresher("cursor", refresher),
		withAuditWriter(audit),
		WithAuditLedger(&ledgerSpy{}),
	)

	err := s.RunOnce(context.Background())
	if !errors.Is(err, cursor.ErrCursorAuthExpired) {
		t.Fatalf("RunOnce err=%v, want ErrCursorAuthExpired", err)
	}
	if store.failureAccountID != 25 || store.failureOutcome != "auth_expired" {
		t.Fatalf("cursor failure hook=(account=%d outcome=%q), want account 25 auth_expired", store.failureAccountID, store.failureOutcome)
	}
	if len(store.saved) != 0 {
		t.Fatalf("expired Cursor auth must not save credential: %s", string(store.saved))
	}
	if got := audit.lastOutcome(); got != auth.RefreshAuditOutcome("auth_expired") {
		t.Fatalf("scheduler audit outcome=%q, want auth_expired", got)
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

func TestSchedulerStopsBackoffLoopForNonRetryableRefreshError(t *testing.T) {
	// Regression killed: vendor refreshers that have already classified a
	// failure as terminal for the current tick must not be called again by the
	// generic retry loop. Mutation self-check: removing RetryableRefresh()
	// handling makes this test call the refresher three times.
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
	outcome         auth.Outcome // account-scope denial outcome ("" = admit)
	endpointOutcome auth.Outcome // provider-endpoint denial outcome ("" = admit)
	globalOutcome   auth.Outcome // global-scope denial outcome ("" = admit)
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

type schedulerCursorStore struct {
	rec              credentialstore.CredentialRecord
	saved            []byte
	failureAccountID int64
	failureOutcome   string
}

func (s *schedulerCursorStore) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	return cloneSchedulerCursorRecord(s.rec), nil
}

func (s *schedulerCursorStore) WithRefreshTransaction(_ context.Context, fn func(cursor.RefreshTxStore, db.DBTX) error) error {
	tx := &schedulerCursorTx{store: s}
	return fn(tx, tx)
}

type schedulerCursorTx struct {
	store *schedulerCursorStore
}

func (tx *schedulerCursorTx) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (tx *schedulerCursorTx) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (tx *schedulerCursorTx) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

func (tx *schedulerCursorTx) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	return cloneSchedulerCursorRecord(tx.store.rec), nil
}

func (tx *schedulerCursorTx) SaveRefreshSuccess(_ context.Context, rec credentialstore.CredentialRecord, credential []byte, _ time.Time, _ string) error {
	tx.store.saved = append([]byte(nil), credential...)
	return nil
}

func (tx *schedulerCursorTx) SaveRefreshFailure(_ context.Context, rec credentialstore.CredentialRecord, failureClass string, _ time.Time) error {
	tx.store.failureAccountID = rec.ProviderAccountID
	tx.store.failureOutcome = failureClass
	return nil
}

func cloneSchedulerCursorRecord(rec credentialstore.CredentialRecord) credentialstore.CredentialRecord {
	rec.PlaintextPayload = append([]byte(nil), rec.PlaintextPayload...)
	return rec
}

type schedulerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f schedulerRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type nonRetryableRefreshErr struct{}

func (nonRetryableRefreshErr) Error() string { return "rate_limit_exceeded" }

func (nonRetryableRefreshErr) RetryableRefresh() bool { return false }

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
