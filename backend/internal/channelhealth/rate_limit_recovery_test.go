package channelhealth

import (
	"context"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
)

type rateLimitRecoveryAuthLaneSpy struct {
	clearCalls int
}

func (*rateLimitRecoveryAuthLaneSpy) Suspend(context.Context, int64, authcooldown.FailureClass, int, time.Time) {
}

func (s *rateLimitRecoveryAuthLaneSpy) Clear(context.Context, int64, string) {
	s.clearCalls++
}

func (*rateLimitRecoveryAuthLaneSpy) Eligible(int64, time.Time) (bool, bool) {
	return false, true
}

func TestClearRateLimitByProviderAccountPreservesOtherHealthEvidenceAndAuthLane(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC)}
	authLane := &rateLimitRecoveryAuthLaneSpy{}
	svc := NewService(store, testPolicy(), clock, WithAuthCooldownLane(authLane))
	key := testKey()
	rateAt := clock.Now().Add(-3 * time.Minute)
	upstreamAt := clock.Now().Add(-2 * time.Minute)
	rec := Record{
		Key: key, State: StateCoolingDown, ReasonClass: SignalClass("rate_limit_rpm"),
		Confidence: ConfidenceObserved, CooldownUntil: timePtr(clock.Now().Add(time.Hour)),
		SampleWindow: summarizeSamples([]SignalSample{
			{At: rateAt, Class: SignalRateLimit, StatusCode: 429},
			{At: upstreamAt, Class: SignalUpstream5xx, StatusCode: 503},
			{At: clock.Now().Add(-time.Minute), Class: SignalRateLimit, StatusCode: 429},
		}),
		LastSignalClass: SignalRateLimit, LastSignalAt: timePtr(clock.Now().Add(-time.Minute)),
		StateEnteredAt: clock.Now().Add(-time.Minute), LastTransitionAt: clock.Now().Add(-time.Minute),
		PolicyVersion: testPolicy().Version, CreatedAt: clock.Now().Add(-time.Hour), UpdatedAt: clock.Now().Add(-time.Minute),
	}
	if _, err := store.UpsertRecord(ctx, rec); err != nil {
		t.Fatalf("seed record: %v", err)
	}

	got, changed, err := svc.ClearRateLimitByProviderAccount(ctx, key.TenantID, key.ProviderAccountID, "operator:7")
	if err != nil {
		t.Fatalf("ClearRateLimitByProviderAccount: %v", err)
	}
	if !changed {
		t.Fatal("rate-limit cooling record must be changed")
	}
	if got.State != StateRamping || got.RampStagePct != 1 || got.CooldownUntil != nil {
		t.Fatalf("recovered state=%s ramp=%d cooldown=%v", got.State, got.RampStagePct, got.CooldownUntil)
	}
	if got.ReasonClass != SignalNone {
		t.Fatalf("reason_class=%s want none", got.ReasonClass)
	}
	if got.SampleWindow.RateLimitHits != 0 || got.SampleWindow.Upstream5xxHits != 1 ||
		got.SampleWindow.TotalAttempts != 1 || got.SampleWindow.FailedAttempts != 1 {
		t.Fatalf("sample window was not selectively cleared: %+v", got.SampleWindow)
	}
	if got.LastSignalClass != SignalUpstream5xx || got.LastSignalAt == nil || !got.LastSignalAt.Equal(upstreamAt) {
		t.Fatalf("last signal=%s at=%v want upstream_5xx at %s", got.LastSignalClass, got.LastSignalAt, upstreamAt)
	}
	if authLane.clearCalls != 0 {
		t.Fatalf("rate-limit recovery must not clear auth lane, calls=%d", authLane.clearCalls)
	}
	audits := store.Audits()
	if len(audits) != 2 || audits[0].Type != EventManualOverride || audits[1].Type != EventRampStarted {
		t.Fatalf("recovery audits=%+v", audits)
	}
	for _, event := range audits {
		if event.ActorID != "operator:7" {
			t.Fatalf("audit actor=%q want operator:7", event.ActorID)
		}
	}
}

func TestClearRateLimitByProviderAccountLeavesNonRateLimitStateUntouched(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC)}
	svc := NewService(store, testPolicy(), clock)
	key := testKey()
	rec := Record{
		Key: key, State: StateDisabled, ReasonClass: SignalCredentialRevoked,
		CooldownUntil: timePtr(clock.Now().Add(time.Hour)),
		SampleWindow:  summarizeSamples([]SignalSample{{At: clock.Now(), Class: SignalCredentialRevoked}}),
		CreatedAt:     clock.Now(), UpdatedAt: clock.Now(),
	}
	if _, err := store.UpsertRecord(ctx, rec); err != nil {
		t.Fatalf("seed record: %v", err)
	}

	got, changed, err := svc.ClearRateLimitByProviderAccount(ctx, key.TenantID, key.ProviderAccountID, "operator:7")
	if err != nil {
		t.Fatalf("ClearRateLimitByProviderAccount: %v", err)
	}
	if changed || got.State != StateDisabled || got.ReasonClass != SignalCredentialRevoked || got.CooldownUntil == nil {
		t.Fatalf("non-rate-limit state changed: changed=%v record=%+v", changed, got)
	}
	if len(store.Audits()) != 0 {
		t.Fatalf("no-op must not write audit: %+v", store.Audits())
	}
}

func TestClearRateLimitByProviderAccountMissingRecordIsExplicit(t *testing.T) {
	svc := NewService(NewMemoryStore(), testPolicy(), nil)
	if _, _, err := svc.ClearRateLimitByProviderAccount(context.Background(), 7, 101, "operator:7"); err != ErrNotFound {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func timePtr(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}
