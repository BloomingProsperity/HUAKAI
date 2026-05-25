package credentialworker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

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
			name:      "auth expired revoked cooldown",
			outcome:   auth.RefreshAuditOutcome("auth_expired"),
			wantState: "revoked",
			wantUntil: timePtr(fixed.Add(30 * time.Minute)),
		},
		{
			name:      "rate limit throttled cooldown",
			outcome:   auth.RefreshAuditOutcome("rate_limit_exceeded"),
			wantState: "throttled",
			wantUntil: timePtr(fixed.Add(3 * time.Minute)),
		},
		{
			name:      "risk control revoked cooldown with alert",
			outcome:   auth.RefreshAuditOutcome("risk_control_triggered"),
			wantState: "revoked",
			wantUntil: timePtr(fixed.Add(30 * time.Minute)),
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
	if got.HealthStateUntil == nil || !got.HealthStateUntil.Equal(fixed.Add(30*time.Minute)) {
		t.Fatalf("health until=%v, want %s", got.HealthStateUntil, fixed.Add(30*time.Minute))
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
