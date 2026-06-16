package channelhealth

import (
	"reflect"
	"testing"
	"time"
)

// Empty/"{}" override must reproduce DefaultPolicy() exactly. Guards against a
// merge that accidentally zeroes unset fields.
func TestPolicyOverride_EmptyEqualsDefault(t *testing.T) {
	o, err := ParsePolicyOverride([]byte("{}"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := o.Apply(DefaultPolicy())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !reflect.DeepEqual(got, DefaultPolicy()) {
		t.Fatalf("empty override must equal DefaultPolicy; diff present: got %+v", got)
	}
}

// Set fields must override; unset fields must stay default; locked safety fields
// must be untouched. Discriminating: chosen values differ from the defaults
// (50 / 30000 / 24h), so an Apply that drops a field fails this test.
func TestPolicyOverride_AppliesSetFieldsOnly(t *testing.T) {
	raw := []byte(`{"error_rate_threshold_pct":80,"latency_p99_threshold_ms":15000,"ban_signal_min_cooldown":"1h","ban_signal_max_cooldown":"2h"}`)
	o, err := ParsePolicyOverride(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := o.Apply(DefaultPolicy())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	def := DefaultPolicy()
	if def.ErrorRateThresholdPct == 80 || def.LatencyP99ThresholdMS == 15000 {
		t.Fatalf("fixture not discriminating: chosen values equal defaults")
	}
	if got.ErrorRateThresholdPct != 80 {
		t.Errorf("ErrorRateThresholdPct = %v, want 80 (override ignored?)", got.ErrorRateThresholdPct)
	}
	if got.LatencyP99ThresholdMS != 15000 {
		t.Errorf("LatencyP99ThresholdMS = %d, want 15000", got.LatencyP99ThresholdMS)
	}
	if got.BanSignalMinCooldown != time.Hour {
		t.Errorf("BanSignalMinCooldown = %s, want 1h", got.BanSignalMinCooldown)
	}
	if got.RateLimitHitRateThresholdPct != def.RateLimitHitRateThresholdPct {
		t.Errorf("unset RateLimitHitRateThresholdPct changed: %v != default %v", got.RateLimitHitRateThresholdPct, def.RateLimitHitRateThresholdPct)
	}
	// Locked safety fields are not exposed by PolicyOverride; confirm they survive.
	if got.ManualOverrideRequiresReason != def.ManualOverrideRequiresReason {
		t.Errorf("locked ManualOverrideRequiresReason changed to %v", got.ManualOverrideRequiresReason)
	}
	if got.AutomaticPostBanRamp != def.AutomaticPostBanRamp {
		t.Errorf("locked AutomaticPostBanRamp changed to %v", got.AutomaticPostBanRamp)
	}
}

// Every out-of-range / malformed override must be rejected (Apply error), so a bad
// operator value falls back to the safe default at load time instead of installing
// a broken policy. Guards Validate + duration parsing.
func TestPolicyOverride_RejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"pct over 100":       `{"error_rate_threshold_pct":150}`,
		"negative pct":       `{"upstream_5xx_rate_threshold_pct":-1}`,
		"non-positive latcy": `{"latency_p99_threshold_ms":0}`,
		"zero window":        `{"error_rate_window":"0s"}`,
		"ban max below min":  `{"ban_signal_min_cooldown":"72h","ban_signal_max_cooldown":"1h"}`,
		"unparseable dur":    `{"latency_cooldown":"not-a-duration"}`,
		"zero sample count":  `{"min_sample_count":0}`,
	}
	for name, raw := range cases {
		o, err := ParsePolicyOverride([]byte(raw))
		if err != nil {
			// some malformed inputs are caught at parse; that is also a rejection
			continue
		}
		if _, err := o.Apply(DefaultPolicy()); err == nil {
			t.Errorf("%s: Apply must reject, got nil error (raw=%s)", name, raw)
		}
	}
}

// A mistyped knob name must be rejected, not silently ignored — otherwise an
// operator thinks they changed a threshold that never took effect.
func TestParsePolicyOverride_RejectsUnknownField(t *testing.T) {
	if _, err := ParsePolicyOverride([]byte(`{"eror_rate_threshold_pct":80}`)); err == nil {
		t.Fatalf("unknown/mistyped field must be rejected")
	}
}

// DefaultPolicy() must always satisfy Validate — a regression in the defaults
// would otherwise brick the gateway at load.
func TestPolicy_Validate_DefaultPasses(t *testing.T) {
	if err := DefaultPolicy().Validate(); err != nil {
		t.Fatalf("DefaultPolicy must validate, got %v", err)
	}
}
