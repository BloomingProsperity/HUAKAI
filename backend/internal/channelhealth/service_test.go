package channelhealth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time      { return c.now }
func (c *fixedClock) Add(d time.Duration) { c.now = c.now.Add(d) }

func testPolicy() Policy {
	p := DefaultPolicy()
	p.MinSampleCount = 3
	p.MinObservation = time.Millisecond
	p.ErrorRateThresholdPct = 50
	p.ErrorRateCooldown = time.Minute
	p.RateLimitHitRateThresholdPct = 50
	p.DefaultRateLimitCooldown = time.Minute
	p.Upstream5xxRateThresholdPct = 50
	p.Upstream5xxCooldown = time.Minute
	p.LatencyP99ThresholdMS = 100
	p.LatencyCooldown = time.Minute
	p.RampStageMinDuration = time.Millisecond
	p.RampStageMinSamples = 1
	p.RampErrorThresholdPct = 40
	p.RepeatedRampRollbackAlertThreshold = 2
	return p
}

func testKey() ChannelKey {
	return ChannelKey{
		TenantID: 7, Vendor: "openai", ProviderAccountID: 101,
		AccountCredentialID: 9001, CredentialVersion: 1,
	}
}

func TestChannelHealth_AT001_DefaultActiveSubject(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 5, 16, 8, 0, 0, 0, time.UTC)}
	svc := NewService(store, testPolicy(), clock)

	rec, err := svc.EnsureDefaultActive(ctx, testKey())
	if err != nil {
		t.Fatalf("EnsureDefaultActive: %v", err)
	}
	if rec.State != StateActive || rec.Key.StableChannelID() == "" {
		t.Fatalf("default record mismatch: %+v", rec)
	}
	if len(store.Audits()) != 0 {
		t.Fatalf("default active should not emit degradation audit")
	}
}

func TestChannelHealth_AT002_ErrorRateCooldownAndAudit(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError}); err != nil {
			t.Fatalf("ApplySignal: %v", err)
		}
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateCoolingDown || rec.CooldownUntil == nil {
		t.Fatalf("state=%s cooldown=%v want cooling_down with cooldown", rec.State, rec.CooldownUntil)
	}
	events := auditTypes(store.Audits())
	if !hasAudit(events, EventDegraded) || !hasAudit(events, EventDisabled) {
		t.Fatalf("events=%v want degraded and disabled", events)
	}
}

func TestChannelHealth_AT003_Upstream5xxDegradesBeforeCooldownAndLocal5xxIgnored(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalLocalGateway5xx}); err != nil {
			t.Fatalf("local ApplySignal: %v", err)
		}
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateActive {
		t.Fatalf("local gateway 5xx changed state to %s", rec.State)
	}
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalUpstream5xx}); err != nil {
			t.Fatalf("upstream ApplySignal: %v", err)
		}
	}
	rec, _ = store.Get(ctx, key)
	if rec.State != StateDegraded || rec.ReasonClass != SignalUpstream5xx {
		t.Fatalf("first upstream breach = %+v, want degraded/upstream_5xx", rec)
	}
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalUpstream5xx}); err != nil {
			t.Fatalf("second upstream ApplySignal: %v", err)
		}
	}
	rec, _ = store.Get(ctx, key)
	if rec.State != StateCoolingDown {
		t.Fatalf("repeated upstream breach state=%s want cooling_down", rec.State)
	}
}

func TestChannelHealth_AT004_RateLimitCooldownUsesResetTimestamp(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	reset := clock.Now().Add(17 * time.Minute)
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalRateLimit, RateLimitResetAt: &reset}); err != nil {
			t.Fatalf("ApplySignal: %v", err)
		}
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateCoolingDown || rec.CooldownUntil == nil || !rec.CooldownUntil.Equal(reset) {
		t.Fatalf("cooldown_until=%v want reset %v", rec.CooldownUntil, reset)
	}
}

func TestChannelHealth_AT005_BanSignalDisablesAndAlertsWithoutRawText(t *testing.T) {
	ctx, svc, store, _ := testService()
	key := testKey()
	_, err := svc.ApplySignal(ctx, Signal{
		Key: key, Class: SignalTokenRevoked,
		RawUpstreamText: "token hk_live_secret should never be stored",
	})
	if err != nil {
		t.Fatalf("ApplySignal: %v", err)
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateDisabled || rec.CooldownUntil == nil {
		t.Fatalf("ban state=%s cooldown=%v", rec.State, rec.CooldownUntil)
	}
	if len(store.Alerts()) != 1 || store.Alerts()[0].Type != AlertBanSignal {
		t.Fatalf("alerts=%+v want ban alert", store.Alerts())
	}
	for _, ev := range store.Audits() {
		raw, _ := json.Marshal(ev)
		if strings.Contains(string(raw), "hk_live_secret") || strings.Contains(string(raw), "should never be stored") {
			t.Fatalf("audit leaked raw upstream text: %s", raw)
		}
	}
}

func TestChannelHealth_AT007_AT008_RampRecoveryAndRollback(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	rec, _ := svc.EnsureDefaultActive(ctx, key)
	cooldownUntil := clock.Now().Add(-time.Second)
	rec.State = StateCoolingDown
	rec.CooldownUntil = &cooldownUntil
	rec, _ = store.UpsertRecord(ctx, rec)

	rec, err := svc.MaybeStartRamp(ctx, key)
	if err != nil {
		t.Fatalf("MaybeStartRamp: %v", err)
	}
	if rec.State != StateRamping || rec.RampStagePct != 1 {
		t.Fatalf("ramp start=%+v want 1%%", rec)
	}
	for _, want := range []int{10, 50, 100, 0} {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalSuccess}); err != nil {
			t.Fatalf("clean signal: %v", err)
		}
		rec, err = svc.AdvanceRamp(ctx, key)
		if err != nil {
			t.Fatalf("AdvanceRamp: %v", err)
		}
		if want == 0 {
			if rec.State != StateActive {
				t.Fatalf("final ramp state=%s want active", rec.State)
			}
		} else if rec.RampStagePct != want {
			t.Fatalf("ramp stage=%d want %d", rec.RampStagePct, want)
		}
	}

	rec.State = StateRamping
	rec.RampStagePct = 10
	start := clock.Now().Add(-time.Second)
	rec.RampStartedAt = &start
	rec.SampleWindow = WindowSummary{}
	rec.RampFailureCount = 1
	rec, _ = store.UpsertRecord(ctx, rec)
	clock.Add(time.Millisecond)
	_, _ = svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError})
	rec, err = svc.AdvanceRamp(ctx, key)
	if err != nil {
		t.Fatalf("AdvanceRamp rollback: %v", err)
	}
	if rec.State != StateCoolingDown || rec.RampFailureCount != 2 {
		t.Fatalf("rollback rec=%+v", rec)
	}
	if got := store.Alerts(); len(got) == 0 || got[len(got)-1].Type != AlertRepeatedRampRollback {
		t.Fatalf("expected repeated rollback alert, got %+v", got)
	}
}

func TestChannelHealth_AT009_IsolatesVendorAndCredentialVersion(t *testing.T) {
	ctx, svc, store, _ := testService()
	key := testKey()
	otherVersion := key
	otherVersion.CredentialVersion = 2
	otherVendor := key
	otherVendor.Vendor = "anthropic"

	for i := 0; i < 3; i++ {
		_, _ = svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError})
	}
	for _, untouched := range []ChannelKey{otherVersion, otherVendor} {
		rec, err := svc.EnsureDefaultActive(ctx, untouched)
		if err != nil {
			t.Fatalf("ensure untouched: %v", err)
		}
		if rec.State != StateActive {
			t.Fatalf("untouched %+v state=%s", untouched, rec.State)
		}
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateCoolingDown {
		t.Fatalf("target state=%s want cooling_down", rec.State)
	}
}

func TestChannelHealth_AT010_ManualOverridesAudit(t *testing.T) {
	ctx, svc, store, _ := testService()
	key := testKey()
	if _, err := svc.ManualPause(ctx, key, "11", "ops pause"); err != nil {
		t.Fatalf("ManualPause: %v", err)
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateManualPaused || rec.ManualPauseReason != "ops pause" {
		t.Fatalf("pause rec=%+v", rec)
	}
	if _, err := svc.ManualResume(ctx, key, "11", "resume ramp"); err != nil {
		t.Fatalf("ManualResume: %v", err)
	}
	if _, err := svc.ForceActive(ctx, key, "11", "break glass"); err != nil {
		t.Fatalf("ForceActive: %v", err)
	}
	if !hasAudit(auditTypes(store.Audits()), EventManualOverride) {
		t.Fatalf("manual override audit missing: %+v", store.Audits())
	}
	if got := store.Alerts(); len(got) == 0 || got[len(got)-1].Type != AlertManualForceActive {
		t.Fatalf("force-active alert missing: %+v", got)
	}
	if _, err := svc.ForceActive(ctx, key, "11", ""); err == nil {
		t.Fatalf("force-active without reason should fail")
	}
}

func TestChannelHealth_AT013_LatencyCooldownThenRecoveryWithoutDisable(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		_, _ = svc.ApplySignal(ctx, Signal{Key: key, Class: SignalSuccess, LatencyMS: 500})
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateDegraded || rec.ReasonClass != SignalLatencyP99 {
		t.Fatalf("latency first state=%s reason=%s", rec.State, rec.ReasonClass)
	}
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		_, _ = svc.ApplySignal(ctx, Signal{Key: key, Class: SignalSuccess, LatencyMS: 600})
	}
	rec, _ = store.Get(ctx, key)
	if rec.State != StateCoolingDown || rec.ReasonClass != SignalLatencyP99 {
		t.Fatalf("latency repeated rec=%+v", rec)
	}
	if rec.State == StateDisabled {
		t.Fatalf("latency-only path must not disable")
	}
	expired := clock.Now().Add(-time.Second)
	rec.CooldownUntil = &expired
	rec.SampleWindow = WindowSummary{}
	rec, _ = store.UpsertRecord(ctx, rec)
	rec, _ = svc.MaybeStartRamp(ctx, key)
	if rec.State != StateRamping {
		t.Fatalf("latency recovery state=%s want ramping", rec.State)
	}
}

func TestChannelHealth_AuditFailureRollsBackStateMutation(t *testing.T) {
	ctx := context.Background()
	store := &failingAuditStore{MemoryStore: NewMemoryStore()}
	clock := &fixedClock{now: time.Date(2026, 5, 16, 8, 0, 0, 0, time.UTC)}
	svc := NewService(store, testPolicy(), clock)
	key := testKey()
	if _, err := svc.EnsureDefaultActive(ctx, key); err != nil {
		t.Fatalf("EnsureDefaultActive: %v", err)
	}
	for i := 0; i < 2; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError}); err != nil {
			t.Fatalf("warmup ApplySignal: %v", err)
		}
	}
	before, _ := store.Get(ctx, key)
	store.failAudit = true
	clock.Add(time.Millisecond)
	if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError}); err == nil {
		t.Fatal("ApplySignal should fail when audit append fails")
	}
	after, _ := store.Get(ctx, key)
	if after.State != before.State || after.SampleWindow.TotalAttempts != before.SampleWindow.TotalAttempts {
		t.Fatalf("state/window changed despite rollback: before=%+v after=%+v", before, after)
	}
	if len(store.Audits()) != 0 {
		t.Fatalf("audit half-write remained after rollback: %+v", store.Audits())
	}
}

func testService() (context.Context, *Service, *MemoryStore, *fixedClock) {
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 5, 16, 8, 0, 0, 0, time.UTC)}
	return ctx, NewService(store, testPolicy(), clock), store, clock
}

func auditTypes(events []AuditEvent) []AuditEventType {
	out := make([]AuditEventType, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

func hasAudit(events []AuditEventType, want AuditEventType) bool {
	for _, got := range events {
		if got == want {
			return true
		}
	}
	return false
}

type failingAuditStore struct {
	*MemoryStore
	failAudit bool
}

func (s *failingAuditStore) AppendAudit(ctx context.Context, ev AuditEvent) error {
	if s.failAudit {
		return errors.New("simulated audit failure")
	}
	return s.MemoryStore.AppendAudit(ctx, ev)
}

func (s *failingAuditStore) WithTx(ctx context.Context, fn func(Store) error) error {
	s.mu.RLock()
	records := make(map[string]Record, len(s.records))
	for k, v := range s.records {
		records[k] = v
	}
	audits := append([]AuditEvent(nil), s.audits...)
	alerts := append([]Alert(nil), s.alerts...)
	s.mu.RUnlock()
	if err := fn(s); err != nil {
		s.mu.Lock()
		s.records = records
		s.audits = audits
		s.alerts = alerts
		s.mu.Unlock()
		return err
	}
	_ = ctx
	return nil
}
