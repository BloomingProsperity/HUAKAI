package credentialworker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// TestDefaultProviderAccountHealthPolicyMapsAuditOutcomes guards the three-way
// terminal / transient-cooldown / healthy taxonomy. The table is self-discriminating —
// the terminal classes (auth_expired, risk_control_triggered, account_disabled) MUST carry a
// nil HealthStateUntil so the eligibility SQL (health_state_until IS NOT NULL) and router gate
// (until.IsZero) refuse to auto-recover them, while the genuinely-transient rate_limit_exceeded
// MUST keep a finite future deadline so it DOES auto-recover.
//
// Mutation check: revert auth_expired (or risk_control_triggered) to now+RevokedCooldown and its
// wantUntil:nil assertion goes red; conversely if a fix blanket-nils every outcome, the
// rate_limit_exceeded wantUntil:+3m row goes red — proving the test distinguishes terminal from
// transient rather than rubber-stamping either extreme.
func TestDefaultProviderAccountHealthPolicyMapsAuditOutcomes(t *testing.T) {
	fixed := time.Date(2026, 5, 25, 9, 30, 0, 0, time.UTC)
	policy := DefaultProviderAccountHealthPolicy()

	for _, tc := range []struct {
		name      string
		outcome   auth.Outcome
		wantState string
		wantUntil *time.Time
		wantAlert bool
	}{
		{
			name:      "auth expired is terminal revoked with operator alert",
			outcome:   auth.RefreshAuditOutcome("auth_expired"),
			wantState: "revoked",
			wantUntil: nil,
			wantAlert: true,
		},
		{
			name:      "rate limit throttled cooldown",
			outcome:   auth.RefreshAuditOutcome("rate_limit_exceeded"),
			wantState: "throttled",
			wantUntil: timePtr(fixed.Add(3 * time.Minute)),
		},
		{
			name:      "risk control is terminal revoked with alert",
			outcome:   auth.RefreshAuditOutcome("risk_control_triggered"),
			wantState: "revoked",
			wantUntil: nil,
			wantAlert: true,
		},
		{
			name:      "account disabled permanent revoked",
			outcome:   auth.RefreshAuditOutcome("account_disabled"),
			wantState: "revoked",
			wantUntil: nil,
		},
		{
			name:      "refresh success resets healthy",
			outcome:   auth.OutcomeRefreshSucceeded,
			wantState: "healthy",
			wantUntil: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := policy.Transition(tc.outcome, fixed)
			if !ok {
				t.Fatalf("Transition(%q) returned no-op", tc.outcome)
			}
			if got.HealthState != tc.wantState {
				t.Fatalf("state=%q, want %q", got.HealthState, tc.wantState)
			}
			if !sameOptionalTime(got.HealthStateUntil, tc.wantUntil) {
				t.Fatalf("until=%v, want %v", got.HealthStateUntil, tc.wantUntil)
			}
			if got.Alert != tc.wantAlert {
				t.Fatalf("alert=%v, want %v", got.Alert, tc.wantAlert)
			}
		})
	}
}

func TestSchedulerAuthExpiredMarksProviderAccountRevoked(t *testing.T) {
	// Regression killed: scheduler audit outcome must also mutate
	// provider_accounts.health_state. Mutation self-check: deleting the health
	// update leaves health.entries empty and this test turns red.
	fixed := time.Date(2026, 5, 25, 9, 30, 0, 0, time.UTC)
	health := &healthStateStoreSpy{}
	audit := &auditSpy{}
	ref := &refresherSpy{errs: []error{
		auth.WithRefreshAuditOutcome(nonRetryableRefreshErr{}, "auth_expired"),
	}}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccount(31)}, &stormSpy{}, ref,
		withNow(func() time.Time { return fixed }),
		withProviderAccountHealthStore(health),
		withAuditWriter(audit),
		WithAuditLedger(&ledgerSpy{}),
	)

	err := s.RunOnce(context.Background())
	if err == nil {
		t.Fatalf("RunOnce must return classified refresh error")
	}
	if got := audit.lastOutcome(); got != auth.RefreshAuditOutcome("auth_expired") {
		t.Fatalf("audit outcome=%q, want auth_expired", got)
	}
	if len(health.entries) != 1 {
		t.Fatalf("health updates=%d, want 1", len(health.entries))
	}
	got := health.entries[0]
	if got.TenantID != 7 || got.ProviderAccountID != 31 {
		t.Fatalf("health update target=(tenant=%d account=%d), want (7,31)", got.TenantID, got.ProviderAccountID)
	}
	if got.HealthState != "revoked" {
		t.Fatalf("health state=%q, want revoked", got.HealthState)
	}
	// auth_expired is terminal — HealthStateUntil must be nil so neither the eligibility
	// SQL nor the router gate auto-recovers the account on a timer. Mutation check: restore the
	// now+cooldown deadline and this assertion goes red.
	if got.HealthStateUntil != nil {
		t.Fatalf("health until=%v, want nil (terminal)", got.HealthStateUntil)
	}
	if !got.Alert {
		t.Fatalf("auth_expired must raise an operator alert")
	}
}

type healthStateStoreSpy struct {
	entries []ProviderAccountHealthChange
	err     error
}

func (s *healthStateStoreSpy) UpdateProviderAccountHealth(_ context.Context, change ProviderAccountHealthChange) error {
	if s.err != nil {
		return s.err
	}
	s.entries = append(s.entries, change)
	return nil
}

func TestSchedulerHealthStateUpdateFailureFailsClosed(t *testing.T) {
	// Regression killed: health_state mutation failure must not be hidden
	// behind a successful audit write. Mutation self-check: swallowing the
	// health store error makes RunOnce return only the classified refresh error
	// without the sentinel below.
	healthErr := errors.New("health update rejected")
	health := &healthStateStoreSpy{err: healthErr}
	ref := &refresherSpy{errs: []error{
		auth.WithRefreshAuditOutcome(nonRetryableRefreshErr{}, "auth_expired"),
	}}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccount(32)}, &stormSpy{}, ref,
		withProviderAccountHealthStore(health),
		WithAuditLedger(&ledgerSpy{}),
	)

	err := s.RunOnce(context.Background())
	if !errors.Is(err, healthErr) {
		t.Fatalf("RunOnce err=%v, want health update error", err)
	}
}

// providerAccountDownSpy records every alert delivery the scheduler attempts so
// the tests can assert the (tenant, account, state, outcome) tuple and the call
// count. err lets a test simulate a failing notification pipeline.
type providerAccountDownSpy struct {
	deliveries []providerAccountDownDelivery
	err        error
}

type providerAccountDownDelivery struct {
	TenantID    int64
	AccountID   int64
	HealthState string
	Outcome     auth.Outcome
}

func (s *providerAccountDownSpy) DeliverProviderAccountDown(_ context.Context, change ProviderAccountHealthChange, outcome auth.Outcome) error {
	s.deliveries = append(s.deliveries, providerAccountDownDelivery{
		TenantID:    change.TenantID,
		AccountID:   change.ProviderAccountID,
		HealthState: change.HealthState,
		Outcome:     outcome,
	})
	return s.err
}

func syncAlertRunner(fn func()) { fn() }

// TestSchedulerProviderAccountDownDeliveredOnAuthExpired proves the alert
// deliverer fires exactly once, carrying the right (tenant, account, state)
// tuple, when a refresh classifies as auth_expired (Alert=true).
//
// Mutation check: delete the s.deliverProviderAccountDown call inside
// maybeLogProviderAccountHealthAlert and the spy stays empty -> red. The spy
// records the concrete (tenant=7, account=31, state=revoked) tuple, so a no-op
// or a wrong-target delivery is also caught, not merely "something fired".
func TestSchedulerProviderAccountDownDeliveredOnAuthExpired(t *testing.T) {
	fixed := time.Date(2026, 5, 25, 9, 30, 0, 0, time.UTC)
	alertSpy := &providerAccountDownSpy{}
	ref := &refresherSpy{errs: []error{
		auth.WithRefreshAuditOutcome(nonRetryableRefreshErr{}, "auth_expired"),
	}}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(31, "anthropic")}, &stormSpy{}, ref,
		withNow(func() time.Time { return fixed }),
		withProviderAccountHealthStore(&healthStateStoreSpy{}),
		WithProviderAccountDownDeliverer(alertSpy),
		withAlertAsync(syncAlertRunner),
	)

	_ = s.RunOnce(context.Background())

	if len(alertSpy.deliveries) != 1 {
		t.Fatalf("alert deliveries=%d, want 1; MUTATION: dropping the deliverer call leaves this 0", len(alertSpy.deliveries))
	}
	got := alertSpy.deliveries[0]
	if got.TenantID != 7 || got.AccountID != 31 {
		t.Fatalf("alert target=(tenant=%d account=%d), want (7,31)", got.TenantID, got.AccountID)
	}
	if got.HealthState != "revoked" {
		t.Fatalf("alert health state=%q, want revoked", got.HealthState)
	}
	if got.Outcome != auth.RefreshAuditOutcome("auth_expired") {
		t.Fatalf("alert outcome=%q, want auth_expired", got.Outcome)
	}
}

// TestSchedulerProviderAccountDownNotDeliveredWhenAlertFalse proves the Alert
// flag is the gate: account_disabled and rate_limit_exceeded both transition the
// account but currently carry Alert=false, so NO alert must be delivered.
//
// Mutation check: flip `if !change.Alert { return }` in
// maybeLogProviderAccountHealthAlert to deliver unconditionally and the spy gains
// deliveries -> red. This is the discriminating fixture for the gate itself, not
// just for "delivery happens".
func TestSchedulerProviderAccountDownNotDeliveredWhenAlertFalse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome string
	}{
		{name: "account_disabled", outcome: "account_disabled"},
		{name: "rate_limit_exceeded", outcome: "rate_limit_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixed := time.Date(2026, 5, 25, 9, 30, 0, 0, time.UTC)
			alertSpy := &providerAccountDownSpy{}
			ref := &refresherSpy{errs: []error{
				auth.WithRefreshAuditOutcome(nonRetryableRefreshErr{}, tc.outcome),
			}}
			s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(41, "anthropic")}, &stormSpy{}, ref,
				withNow(func() time.Time { return fixed }),
				withProviderAccountHealthStore(&healthStateStoreSpy{}),
				WithProviderAccountDownDeliverer(alertSpy),
				withAlertAsync(syncAlertRunner),
			)

			_ = s.RunOnce(context.Background())

			if len(alertSpy.deliveries) != 0 {
				t.Fatalf("alert deliveries=%d, want 0 for Alert=false outcome %q; MUTATION: removing the Alert gate makes this >0", len(alertSpy.deliveries), tc.outcome)
			}
		})
	}
}

// TestSchedulerProviderAccountDownDeliveryFailureNonFatal proves an alert send
// failure never breaks the credential worker: RunOnce's returned error must be
// exactly the classified refresh error and must NOT wrap the deliverer error.
//
// Mutation check: propagate the deliverer error into recordAudit's return and
// RunOnce would then wrap deliverErr -> errors.Is(err, deliverErr) becomes true
// -> red. Proves the non-fatal isolation (the core safety property).
func TestSchedulerProviderAccountDownDeliveryFailureNonFatal(t *testing.T) {
	fixed := time.Date(2026, 5, 25, 9, 30, 0, 0, time.UTC)
	deliverErr := errors.New("notification pipeline unavailable")
	alertSpy := &providerAccountDownSpy{err: deliverErr}
	health := &healthStateStoreSpy{}
	audit := &auditSpy{}
	ref := &refresherSpy{errs: []error{
		auth.WithRefreshAuditOutcome(nonRetryableRefreshErr{}, "auth_expired"),
	}}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(51, "anthropic")}, &stormSpy{}, ref,
		withNow(func() time.Time { return fixed }),
		withProviderAccountHealthStore(health),
		withAuditWriter(audit),
		WithAuditLedger(&ledgerSpy{}),
		WithProviderAccountDownDeliverer(alertSpy),
		withAlertAsync(syncAlertRunner),
	)

	err := s.RunOnce(context.Background())

	if errors.Is(err, deliverErr) {
		t.Fatalf("RunOnce wrapped the alert delivery error %v; MUTATION: propagating the deliverer error up makes this fail", err)
	}
	// The alert was still attempted (delivery ran, returned its error) and the
	// audit/health path still committed normally.
	if len(alertSpy.deliveries) != 1 {
		t.Fatalf("alert deliveries=%d, want 1 (attempted-then-failed)", len(alertSpy.deliveries))
	}
	if got := audit.lastOutcome(); got != auth.RefreshAuditOutcome("auth_expired") {
		t.Fatalf("audit outcome=%q, want auth_expired (audit path still succeeded)", got)
	}
	if len(health.entries) != 1 || health.entries[0].HealthState != "revoked" {
		t.Fatalf("health path did not commit revoked transition: %+v", health.entries)
	}
}

func timePtr(v time.Time) *time.Time {
	return &v
}

func sameOptionalTime(a, b *time.Time) bool {
	switch {
	case a == nil || b == nil:
		return a == nil && b == nil
	default:
		return a.Equal(*b)
	}
}
