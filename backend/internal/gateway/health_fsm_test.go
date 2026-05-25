package gateway

import (
	"math"
	"testing"
	"time"
)

func a22Time() time.Time { return time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC) }

func ambiguousEvent(now time.Time) Event {
	return Event{
		Type: EventClassification,
		Classification: FSMClassification{
			RuleID:        "R-015",
			ErrorClass:    "upstream_5xx",
			FsmTransition: FsmTransitionDegraded,
			Tier:          TierAmbiguous,
			Severity:      1.0,
		},
		Health: HealthSnapshot{HealthScore: 0.80, LastScoredAt: now},
	}
}

func hasSE(effects []SideEffect, t SideEffectType) bool {
	for _, e := range effects {
		if e.Type == t {
			return true
		}
	}
	return false
}

func metricVal(t *testing.T, effects []SideEffect, metric string) float64 {
	t.Helper()
	for _, e := range effects {
		if e.Type == SideEffectEmitMetric && e.Metric == metric {
			return e.Value
		}
	}
	t.Fatalf("missing metric %s", metric)
	return 0
}

// AT-STATE-001 core: 3 recent ambiguous errors trigger normal → degraded.
func TestA22_NormalToDegraded_AfterThreeRecentAmbiguous(t *testing.T) {
	now := a22Time()
	ev := ambiguousEvent(now)
	ev.Health.RecentErrorTimes = []time.Time{now.Add(-20 * time.Second), now.Add(-10 * time.Second)}
	next, _ := Transition(HealthStateNormal, ev, now)
	if next != HealthStateDegraded {
		t.Fatalf("got %s; want degraded", next)
	}
}

// 2 recent + 0 = below threshold (3) → stay normal.
func TestA22_NormalStaysBelowThreshold(t *testing.T) {
	now := a22Time()
	ev := ambiguousEvent(now)
	ev.Health.RecentErrorTimes = []time.Time{now.Add(-10 * time.Second)} // 1 recent + 1 current = 2
	next, _ := Transition(HealthStateNormal, ev, now)
	if next != HealthStateNormal {
		t.Fatalf("got %s; want normal (2 < threshold 3)", next)
	}
}

// Iron-clad keyword 401 invalid_grant → disabled with notify.
func TestA22_NormalIronCladDisables(t *testing.T) {
	now := a22Time()
	ev := Event{
		Type: EventClassification,
		Classification: FSMClassification{
			RuleID:        "R-001",
			ErrorClass:    "oauth_invalid_grant",
			FsmTransition: FsmTransitionDisabled,
			Tier:          TierIronClad,
		},
		Health: HealthSnapshot{HealthScore: 1.0, LastScoredAt: now},
	}
	next, effects := Transition(HealthStateNormal, ev, now)
	if next != HealthStateDisabled {
		t.Fatalf("iron-clad must disable; got %s", next)
	}
	if !hasSE(effects, SideEffectNotifyOperator) {
		t.Fatal("iron-clad disable must notify operator")
	}
}

// Cooling trigger sets cooldown + probe.
func TestA22_NormalCoolingTrigger(t *testing.T) {
	now := a22Time()
	ev := Event{
		Type: EventClassification,
		Classification: FSMClassification{
			RuleID:        "R-013",
			ErrorClass:    "upstream_rate_limited",
			FsmTransition: FsmTransitionCooling,
			Tier:          TierAmbiguous,
			RetryAfter:    2 * time.Minute,
		},
		Health: HealthSnapshot{HealthScore: 0.8, LastScoredAt: now},
	}
	next, eff := Transition(HealthStateNormal, ev, now)
	if next != HealthStateCoolingDown {
		t.Fatalf("got %s; want cooling_down", next)
	}
	if !hasSE(eff, SideEffectSetCooldownUntil) || !hasSE(eff, SideEffectEnqueueProbe) {
		t.Fatal("cooling needs SetCooldownUntil + EnqueueProbe")
	}
}

// OAuth refresh path → needs_refresh.
func TestA22_NormalOAuthRefresh(t *testing.T) {
	now := a22Time()
	ev := Event{
		Type: EventClassification,
		Classification: FSMClassification{
			RuleID:        "R-OAUTH",
			ErrorClass:    "oauth_401",
			FsmTransition: FsmTransitionNoChange,
			Tier:          TierAmbiguous,
			NeedsRefresh:  true,
		},
		Health: HealthSnapshot{HealthScore: 0.8, LastScoredAt: now},
	}
	next, _ := Transition(HealthStateNormal, ev, now)
	if next != HealthStateNeedsRefresh {
		t.Fatalf("got %s; want needs_refresh", next)
	}
}

// AT-STATE-002 hysteresis gap: 9 successes is NOT enough.
func TestA22_DegradedNineCleanSuccessesNotEnough(t *testing.T) {
	now := a22Time()
	ev := Event{
		Type:        EventProbeResult,
		ProbeResult: ProbeResult{Clean: true, CleanSuccessStreak: 9},
		Health:      HealthSnapshot{HealthScore: 0.90, LastScoredAt: now},
	}
	next, _ := Transition(HealthStateDegraded, ev, now)
	if next != HealthStateDegraded {
		t.Fatalf("9 successes should NOT recover; got %s", next)
	}
}

// AT-STATE-002 hysteresis gap: 10 successes recovers.
func TestA22_DegradedTenCleanSuccessesRecovers(t *testing.T) {
	now := a22Time()
	ev := Event{
		Type:        EventProbeResult,
		ProbeResult: ProbeResult{Clean: true, CleanSuccessStreak: 10},
		Health:      HealthSnapshot{HealthScore: 0.85, LastScoredAt: now},
	}
	next, _ := Transition(HealthStateDegraded, ev, now)
	if next != HealthStateNormal {
		t.Fatalf("10 successes should recover; got %s", next)
	}
}

// Ambiguous count >= threshold → needs_manual_recovery.
func TestA22_DegradedAmbiguousThresholdEscalates(t *testing.T) {
	now := a22Time()
	ev := ambiguousEvent(now)
	ev.Health.AmbiguousErrorCount = DefaultManualRecoveryThreshold - 1
	next, eff := Transition(HealthStateDegraded, ev, now)
	if next != HealthStateNeedsManualRecovery {
		t.Fatalf("got %s; want needs_manual_recovery", next)
	}
	if !hasSE(eff, SideEffectNotifyOperator) {
		t.Fatal("threshold breach must notify operator")
	}
}

// Cooling trigger from degraded.
func TestA22_DegradedCoolingTrigger(t *testing.T) {
	now := a22Time()
	ev := Event{
		Type: EventClassification,
		Classification: FSMClassification{
			RuleID:        "R-014",
			ErrorClass:    "upstream_overloaded",
			FsmTransition: FsmTransitionCooling,
			Tier:          TierAmbiguous,
			CooldownUntil: now.Add(3 * time.Minute),
		},
		Health: HealthSnapshot{HealthScore: 0.65, LastScoredAt: now},
	}
	next, eff := Transition(HealthStateDegraded, ev, now)
	if next != HealthStateCoolingDown {
		t.Fatalf("got %s; want cooling_down", next)
	}
	if !hasSE(eff, SideEffectSetCooldownUntil) {
		t.Fatal("must set cooldown")
	}
}

// CoolingDown expired with low score: stay cooling, enqueue probe.
func TestA22_CoolingDownExpiredLowScoreStays(t *testing.T) {
	now := a22Time()
	ev := Event{
		Type:   EventClockTime,
		Health: HealthSnapshot{HealthScore: 0.20, LastScoredAt: now, CooldownUntil: now.Add(-time.Second)},
	}
	next, eff := Transition(HealthStateCoolingDown, ev, now)
	if next != HealthStateCoolingDown {
		t.Fatalf("low score should stay cooling; got %s", next)
	}
	if !hasSE(eff, SideEffectEnqueueProbe) {
		t.Fatal("must enqueue probe on expiry")
	}
}

// Clean probe after cooldown exits to degraded (not jumping to normal).
func TestA22_CoolingDownExitsToDegraded(t *testing.T) {
	now := a22Time()
	ev := Event{
		Type:        EventProbeResult,
		ProbeResult: ProbeResult{Clean: true, HealthScore: 0.45, HealthScoreKnown: true},
		Health:      HealthSnapshot{HealthScore: 0.20, LastScoredAt: now, CooldownUntil: now.Add(-time.Second)},
	}
	next, _ := Transition(HealthStateCoolingDown, ev, now)
	if next != HealthStateDegraded {
		t.Fatalf("must exit to degraded not normal; got %s", next)
	}
}

// Refresh success returns to normal.
func TestA22_NeedsRefreshSuccess(t *testing.T) {
	now := a22Time()
	ev := Event{
		Type:        EventProbeResult,
		ProbeResult: ProbeResult{RefreshSuccess: true},
		Health:      HealthSnapshot{HealthScore: 0.8, LastScoredAt: now},
	}
	next, _ := Transition(HealthStateNeedsRefresh, ev, now)
	if next != HealthStateNormal {
		t.Fatalf("got %s; want normal", next)
	}
}

// Refresh exhausted escalates to manual recovery.
func TestA22_NeedsRefreshExhausted(t *testing.T) {
	now := a22Time()
	ev := Event{
		Type:        EventProbeResult,
		ProbeResult: ProbeResult{RefreshExhausted: true},
		Health:      HealthSnapshot{HealthScore: 0.4, LastScoredAt: now},
	}
	next, eff := Transition(HealthStateNeedsRefresh, ev, now)
	if next != HealthStateNeedsManualRecovery {
		t.Fatalf("got %s; want needs_manual_recovery", next)
	}
	if !hasSE(eff, SideEffectNotifyOperator) {
		t.Fatal("must notify operator")
	}
}

// Operator clear from manual recovery → degraded.
func TestA22_ManualRecoveryOperatorClear(t *testing.T) {
	now := a22Time()
	ev := Event{
		Type:        EventProbeResult,
		ProbeResult: ProbeResult{OperatorClear: true},
		Health:      HealthSnapshot{HealthScore: 0.7, LastScoredAt: now},
	}
	next, _ := Transition(HealthStateNeedsManualRecovery, ev, now)
	if next != HealthStateDegraded {
		t.Fatalf("got %s; want degraded", next)
	}
}

// Operator re-enable from disabled → manual recovery.
func TestA22_DisabledOperatorReenable(t *testing.T) {
	now := a22Time()
	ev := Event{
		Type:        EventProbeResult,
		ProbeResult: ProbeResult{OperatorReenable: true},
		Health:      HealthSnapshot{HealthScore: 0.4, LastScoredAt: now},
	}
	next, eff := Transition(HealthStateDisabled, ev, now)
	if next != HealthStateNeedsManualRecovery {
		t.Fatalf("got %s; want needs_manual_recovery", next)
	}
	if !hasSE(eff, SideEffectNotifyOperator) {
		t.Fatal("must notify operator")
	}
}

// §6.6 HARD FLOOR INVARIANT: ambiguous tier never reaches disabled, from any state.
func TestA22_SixSixInvariant_AmbiguousNeverDisabled(t *testing.T) {
	now := a22Time()
	states := []HealthState{
		HealthStateNormal,
		HealthStateDegraded,
		HealthStateCoolingDown,
		HealthStateNeedsRefresh,
		HealthStateNeedsManualRecovery,
	}
	for _, st := range states {
		ev := Event{
			Type: EventClassification,
			Classification: FSMClassification{
				RuleID:        "R-AMBI",
				ErrorClass:    "upstream_5xx",
				FsmTransition: FsmTransitionDisabled, // attempt to disable
				Tier:          TierAmbiguous,         // but ambiguous → §6.6 must block
			},
			Health: HealthSnapshot{
				HealthScore:         0.50,
				LastScoredAt:        now,
				RecentErrorTimes:    []time.Time{now.Add(-20 * time.Second), now.Add(-10 * time.Second)},
				AmbiguousErrorCount: DefaultManualRecoveryThreshold - 1,
				CooldownUntil:       now.Add(-time.Second),
			},
		}
		next, _ := Transition(st, ev, now)
		if next == HealthStateDisabled {
			t.Fatalf("§6.6 violation: %s → disabled on ambiguous tier", st)
		}
	}
}

// Score decay 10-min half-life.
func TestA22_ScoreDecayHalfLife(t *testing.T) {
	now := a22Time()
	high := DecayHealthScore(1.0, now, now.Add(10*time.Minute))
	if math.Abs(high-0.75) > 0.0001 {
		t.Fatalf("high decay want 0.75; got %.4f", high)
	}
	low := DecayHealthScore(0.0, now, now.Add(10*time.Minute))
	if math.Abs(low-0.25) > 0.0001 {
		t.Fatalf("low decay want 0.25; got %.4f", low)
	}
}

// Score decay applies before classification penalty.
func TestA22_ScoreDecayBeforePenalty(t *testing.T) {
	now := a22Time()
	ev := ambiguousEvent(now.Add(10 * time.Minute))
	ev.Health = HealthSnapshot{HealthScore: 1.0, LastScoredAt: now}
	score := HealthScoreAfterEvent(ev, now.Add(10*time.Minute))
	if math.Abs(score-0.60) > 0.0001 {
		t.Fatalf("decay+penalty want 0.60; got %.4f", score)
	}
}

// ToFSMClassification bridge: gateway.Classification → FSMClassification.
func TestA22_ToFSMClassification_Bridge(t *testing.T) {
	gc := Classification{
		Class:         ErrorClassOAuthInvalidGrant,
		Confidence:    ConfidenceHigh,
		RuleID:        "R-001",
		RuleVersion:   1,
		Tier:          TierIronClad,
		RetryAction:   RetryActionPermanentDisable,
		FsmTransition: FsmTransitionDisabled,
		RetryAfterMs:  0,
	}
	fc := ToFSMClassification(gc)
	if fc.RuleID != "R-001" || fc.Tier != TierIronClad || fc.FsmTransition != FsmTransitionDisabled {
		t.Fatalf("bridge lost data: %+v", fc)
	}
	if fc.Severity != 5.0 {
		t.Fatalf("iron_clad severity want 5.0; got %.1f", fc.Severity)
	}
}

// ToFSMClassification: ambiguous tier severity 1.0.
func TestA22_ToFSMClassification_AmbiguousSeverity(t *testing.T) {
	gc := Classification{
		Class: ErrorClassRateLimited, Tier: TierAmbiguous,
		FsmTransition: FsmTransitionCooling, RetryAfterMs: 30_000,
	}
	fc := ToFSMClassification(gc)
	if fc.Severity != 1.0 {
		t.Fatalf("ambiguous severity want 1.0; got %.1f", fc.Severity)
	}
	if fc.RetryAfter != 30*time.Second {
		t.Fatalf("RetryAfter bridge: got %v", fc.RetryAfter)
	}
}

// Score clamping invariants.
func TestA22_ScoreClamping(t *testing.T) {
	if clamp01(-1) != 0 || clamp01(0.5) != 0.5 || clamp01(2) != 1 {
		t.Fatal("clamp01 broken")
	}
}
