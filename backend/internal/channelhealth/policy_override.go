package channelhealth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// PolicyOverride is the operator-facing, JSON-friendly partial override of Policy.
// It is persisted in platform_settings under key "channel_health_policy" and lets
// operators tune channel-health thresholds/cooldowns without a code change
// (Owner directive 2026-06-16: contested policy = operator switch, safe default).
//
// Unset (nil) fields keep the DefaultPolicy() value. Duration fields are strings
// parsed by time.ParseDuration ("5m", "30s", "24h").
//
// Two Policy safety fields are deliberately NOT exposed here and stay at their
// hard-coded safe defaults (Owner decision: lock safety knobs in v1):
// ManualOverrideRequiresReason and AutomaticPostBanRamp. The ramp internals are
// also kept off the operator surface to avoid destabilizing recovery.
type PolicyOverride struct {
	MinSampleCount               *int     `json:"min_sample_count,omitempty"`
	MinObservation               *string  `json:"min_observation,omitempty"`
	ErrorRateThresholdPct        *float64 `json:"error_rate_threshold_pct,omitempty"`
	ErrorRateWindow              *string  `json:"error_rate_window,omitempty"`
	ErrorRateCooldown            *string  `json:"error_rate_cooldown,omitempty"`
	LatencyP99ThresholdMS        *int64   `json:"latency_p99_threshold_ms,omitempty"`
	LatencyWindow                *string  `json:"latency_window,omitempty"`
	LatencyCooldown              *string  `json:"latency_cooldown,omitempty"`
	RateLimitHitRateThresholdPct *float64 `json:"rate_limit_hit_rate_threshold_pct,omitempty"`
	RateLimitWindow              *string  `json:"rate_limit_window,omitempty"`
	DefaultRateLimitCooldown     *string  `json:"default_rate_limit_cooldown,omitempty"`
	Upstream5xxRateThresholdPct  *float64 `json:"upstream_5xx_rate_threshold_pct,omitempty"`
	Upstream5xxWindow            *string  `json:"upstream_5xx_window,omitempty"`
	Upstream5xxCooldown          *string  `json:"upstream_5xx_cooldown,omitempty"`
	BanSignalMinCooldown         *string  `json:"ban_signal_min_cooldown,omitempty"`
	BanSignalMaxCooldown         *string  `json:"ban_signal_max_cooldown,omitempty"`
}

// ParsePolicyOverride parses an operator JSON blob. Blank input or "{}" yields a
// zero override (keep every default). Unknown fields are rejected so a mistyped
// knob surfaces as an error instead of being silently dropped.
func ParsePolicyOverride(raw []byte) (PolicyOverride, error) {
	var o PolicyOverride
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "{}" {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return PolicyOverride{}, fmt.Errorf("channelhealth: parse policy override: %w", err)
	}
	return o, nil
}

// Apply layers the set fields over base and validates the result. A zero override
// returns base unchanged (still validated). Any out-of-range value or unparseable
// duration is rejected, so a bad override can never install a nonsensical policy.
func (o PolicyOverride) Apply(base Policy) (Policy, error) {
	p := base
	if o.MinSampleCount != nil {
		p.MinSampleCount = *o.MinSampleCount
	}
	if err := applyDur("min_observation", o.MinObservation, &p.MinObservation); err != nil {
		return Policy{}, err
	}
	if o.ErrorRateThresholdPct != nil {
		p.ErrorRateThresholdPct = *o.ErrorRateThresholdPct
	}
	if err := applyDur("error_rate_window", o.ErrorRateWindow, &p.ErrorRateWindow); err != nil {
		return Policy{}, err
	}
	if err := applyDur("error_rate_cooldown", o.ErrorRateCooldown, &p.ErrorRateCooldown); err != nil {
		return Policy{}, err
	}
	if o.LatencyP99ThresholdMS != nil {
		p.LatencyP99ThresholdMS = *o.LatencyP99ThresholdMS
	}
	if err := applyDur("latency_window", o.LatencyWindow, &p.LatencyWindow); err != nil {
		return Policy{}, err
	}
	if err := applyDur("latency_cooldown", o.LatencyCooldown, &p.LatencyCooldown); err != nil {
		return Policy{}, err
	}
	if o.RateLimitHitRateThresholdPct != nil {
		p.RateLimitHitRateThresholdPct = *o.RateLimitHitRateThresholdPct
	}
	if err := applyDur("rate_limit_window", o.RateLimitWindow, &p.RateLimitWindow); err != nil {
		return Policy{}, err
	}
	if err := applyDur("default_rate_limit_cooldown", o.DefaultRateLimitCooldown, &p.DefaultRateLimitCooldown); err != nil {
		return Policy{}, err
	}
	if o.Upstream5xxRateThresholdPct != nil {
		p.Upstream5xxRateThresholdPct = *o.Upstream5xxRateThresholdPct
	}
	if err := applyDur("upstream_5xx_window", o.Upstream5xxWindow, &p.Upstream5xxWindow); err != nil {
		return Policy{}, err
	}
	if err := applyDur("upstream_5xx_cooldown", o.Upstream5xxCooldown, &p.Upstream5xxCooldown); err != nil {
		return Policy{}, err
	}
	if err := applyDur("ban_signal_min_cooldown", o.BanSignalMinCooldown, &p.BanSignalMinCooldown); err != nil {
		return Policy{}, err
	}
	if err := applyDur("ban_signal_max_cooldown", o.BanSignalMaxCooldown, &p.BanSignalMaxCooldown); err != nil {
		return Policy{}, err
	}
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

func applyDur(name string, s *string, dst *time.Duration) error {
	if s == nil {
		return nil
	}
	d, err := time.ParseDuration(*s)
	if err != nil {
		return fmt.Errorf("channelhealth: %s: invalid duration %q: %w", name, *s, err)
	}
	*dst = d
	return nil
}

// Validate enforces sane ranges on a Policy so neither a bad operator override nor
// a future edit can install a nonsensical health policy. Safe to call on
// DefaultPolicy(); called by Apply.
func (p Policy) Validate() error {
	pct := func(name string, v float64) error {
		if v < 0 || v > 100 {
			return fmt.Errorf("channelhealth: %s must be within 0-100, got %v", name, v)
		}
		return nil
	}
	if err := pct("error_rate_threshold_pct", p.ErrorRateThresholdPct); err != nil {
		return err
	}
	if err := pct("rate_limit_hit_rate_threshold_pct", p.RateLimitHitRateThresholdPct); err != nil {
		return err
	}
	if err := pct("upstream_5xx_rate_threshold_pct", p.Upstream5xxRateThresholdPct); err != nil {
		return err
	}
	if p.LatencyP99ThresholdMS <= 0 {
		return fmt.Errorf("channelhealth: latency_p99_threshold_ms must be > 0, got %d", p.LatencyP99ThresholdMS)
	}
	if p.MinSampleCount <= 0 {
		return fmt.Errorf("channelhealth: min_sample_count must be > 0, got %d", p.MinSampleCount)
	}
	durations := []struct {
		name string
		d    time.Duration
	}{
		{"min_observation", p.MinObservation},
		{"error_rate_window", p.ErrorRateWindow},
		{"error_rate_cooldown", p.ErrorRateCooldown},
		{"latency_window", p.LatencyWindow},
		{"latency_cooldown", p.LatencyCooldown},
		{"rate_limit_window", p.RateLimitWindow},
		{"default_rate_limit_cooldown", p.DefaultRateLimitCooldown},
		{"upstream_5xx_window", p.Upstream5xxWindow},
		{"upstream_5xx_cooldown", p.Upstream5xxCooldown},
		{"ban_signal_min_cooldown", p.BanSignalMinCooldown},
		{"ban_signal_max_cooldown", p.BanSignalMaxCooldown},
	}
	for _, x := range durations {
		if x.d <= 0 {
			return fmt.Errorf("channelhealth: %s must be > 0, got %s", x.name, x.d)
		}
	}
	if p.BanSignalMaxCooldown < p.BanSignalMinCooldown {
		return fmt.Errorf("channelhealth: ban_signal_max_cooldown (%s) must be >= ban_signal_min_cooldown (%s)", p.BanSignalMaxCooldown, p.BanSignalMinCooldown)
	}
	return nil
}
