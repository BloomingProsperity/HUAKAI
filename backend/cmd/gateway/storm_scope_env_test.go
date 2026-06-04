package main

import "testing"

// TestValidateStormScopePairRejectsHalfConfig proves a half-configured
// refresh-storm scope is a boot error, not a silently-disabled throttle: setting
// only a rate (burst forgotten), only a burst, or a sub-unit burst must fail loud
// so a typo cannot let a cross-account stampede through unthrottled. Both-unset
// (scope intentionally off) and a full rate+burst pair must pass.
//
// Mutation: return nil for the half-configured default branch → the three
// rejection assertions go red.
func TestValidateStormScopePairRejectsHalfConfig(t *testing.T) {
	if err := validateStormScopePair("endpoint", 5, 0); err == nil {
		t.Fatal("rate-only config must be rejected (burst missing)")
	}
	if err := validateStormScopePair("endpoint", 0, 5); err == nil {
		t.Fatal("burst-only config must be rejected (rate missing)")
	}
	if err := validateStormScopePair("endpoint", 5, 0.5); err == nil {
		t.Fatal("sub-unit burst must be rejected (cannot admit a whole token)")
	}
	if err := validateStormScopePair("endpoint", 0, 0); err != nil {
		t.Fatalf("both-unset must be allowed (scope off): %v", err)
	}
	if err := validateStormScopePair("endpoint", 5, 2); err != nil {
		t.Fatalf("fully-configured pair must be allowed: %v", err)
	}
}

// TestLoadStormScopeConfigFromEnvParsesAndFailsLoud proves the env loader
// parses a valid pair and fails loud on a half-configured scope. Mutation: drop the
// validateStormScopePair calls in loadStormScopeConfigFromEnv → the half-config case
// returns nil error → red.
func TestLoadStormScopeConfigFromEnvParsesAndFailsLoud(t *testing.T) {
	// Hermetic: the loader reads all four storm vars, so pin every one — clearing
	// the global pair to "" — instead of inheriting whatever the ambient/CI env
	// happens to have set.
	t.Setenv("HUAKAI_STORM_ENDPOINT_RATE", "3")
	t.Setenv("HUAKAI_STORM_ENDPOINT_BURST", "6")
	t.Setenv("HUAKAI_STORM_GLOBAL_RATE", "")
	t.Setenv("HUAKAI_STORM_GLOBAL_BURST", "")
	cfg, err := loadStormScopeConfigFromEnv()
	if err != nil {
		t.Fatalf("valid endpoint config load: %v", err)
	}
	if cfg.PerEndpointRate != 3 || cfg.PerEndpointBurst != 6 {
		t.Fatalf("parsed cfg=%+v, want endpoint rate/burst 3/6", cfg)
	}

	// Global rate set but burst left unset → half-configured → must fail loud.
	t.Setenv("HUAKAI_STORM_GLOBAL_RATE", "10")
	if _, err := loadStormScopeConfigFromEnv(); err == nil {
		t.Fatal("half-configured global scope (rate without burst) must fail loud at load")
	}
}

// TestParseStormFloatEnvRejectsGarbage proves a malformed budget fails loud rather
// than degrading to 0 (silent disable) or booting an unbounded throttle. The Inf
// and NaN cases are the subtle ones: strconv.ParseFloat accepts them WITHOUT error.
// Mutation: drop the math.IsInf/IsNaN guard → the Inf/NaN cases return nil error → red.
func TestParseStormFloatEnvRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"not-a-number", "-2", "Inf", "+Inf", "NaN"} {
		t.Setenv("HUAKAI_STORM_ENDPOINT_RATE", bad)
		if _, err := parseStormFloatEnv("HUAKAI_STORM_ENDPOINT_RATE"); err == nil {
			t.Fatalf("storm budget %q must be rejected, got nil error", bad)
		}
	}
}
