package main

import (
	"testing"
	"time"
)

// TestHermesMutateGuardConfig_UnsetIsDefaults proves that with NO
// HUAKAI_HERMES_MUTATE_* env set, the parsed config is the conservative built-in
// defaults — and that the orchestrator options + limiter built from it are
// ENABLED (the deployment default), NOT disabled. Mutation: change a default to 0
// (its disable sentinel) and the matching assertion goes RED.
func TestHermesMutateGuardConfig_UnsetIsDefaults(t *testing.T) {
	for _, env := range []string{
		hermesMutateMaxConcurrencyEnv, hermesMutateAcquireWaitEnv,
		hermesMutateTxDeadlineEnv, hermesMutateRatePerTokenEnv, hermesMutateRateWindowEnv,
	} {
		t.Setenv(env, "")
	}
	cfg, err := hermesMutateGuardConfigFromEnv()
	if err != nil {
		t.Fatalf("unset config err=%v want nil", err)
	}
	if cfg.maxConcurrency != defaultHermesMutateMaxConcurrency {
		t.Fatalf("maxConcurrency=%d want %d", cfg.maxConcurrency, defaultHermesMutateMaxConcurrency)
	}
	if cfg.acquireWait != defaultHermesMutateAcquireWait {
		t.Fatalf("acquireWait=%v want %v", cfg.acquireWait, defaultHermesMutateAcquireWait)
	}
	if cfg.txDeadline != defaultHermesMutateTxDeadline {
		t.Fatalf("txDeadline=%v want %v (90s, 3x the 30s dlq lease)", cfg.txDeadline, defaultHermesMutateTxDeadline)
	}
	if cfg.txDeadline != 90*time.Second {
		t.Fatalf("txDeadline default=%v want exactly 90s — do NOT tighten (load-bearing headroom)", cfg.txDeadline)
	}
	if cfg.ratePerToken != defaultHermesMutateRatePerToken {
		t.Fatalf("ratePerToken=%d want %d", cfg.ratePerToken, defaultHermesMutateRatePerToken)
	}
	if cfg.rateWindow != defaultHermesMutateRateWindow {
		t.Fatalf("rateWindow=%v want %v", cfg.rateWindow, defaultHermesMutateRateWindow)
	}
	if l := cfg.newRateLimiter(); !l.Enabled() {
		t.Fatalf("default rate limiter disabled — default must be ENABLED (per_token=30)")
	}
}

// TestHermesMutateGuardConfig_ZeroDisablesEachGuard proves the explicit-0 disable
// sentinel: an operator who sets a knob to 0 turns OFF exactly that guard.
func TestHermesMutateGuardConfig_ZeroDisablesEachGuard(t *testing.T) {
	t.Setenv(hermesMutateMaxConcurrencyEnv, "0")
	t.Setenv(hermesMutateTxDeadlineEnv, "0")
	t.Setenv(hermesMutateRatePerTokenEnv, "0")
	cfg, err := hermesMutateGuardConfigFromEnv()
	if err != nil {
		t.Fatalf("zero-knobs config err=%v want nil", err)
	}
	if cfg.maxConcurrency != 0 {
		t.Fatalf("maxConcurrency=%d want 0 (disabled)", cfg.maxConcurrency)
	}
	if cfg.txDeadline != 0 {
		t.Fatalf("txDeadline=%v want 0 (disabled)", cfg.txDeadline)
	}
	if cfg.ratePerToken != 0 {
		t.Fatalf("ratePerToken=%d want 0 (disabled)", cfg.ratePerToken)
	}
	if l := cfg.newRateLimiter(); l.Enabled() {
		t.Fatalf("rate limiter enabled with per_token=0 — 0 must disable it")
	}
}

// TestHermesMutateGuardConfig_FailsLoudOnGarbage proves a malformed knob is a
// boot error, never a silent fallback (which could mis-bound the path).
func TestHermesMutateGuardConfig_FailsLoudOnGarbage(t *testing.T) {
	t.Setenv(hermesMutateMaxConcurrencyEnv, "not-a-number")
	if _, err := hermesMutateGuardConfigFromEnv(); err == nil {
		t.Fatalf("malformed max_concurrency did not fail loud")
	}
	t.Setenv(hermesMutateMaxConcurrencyEnv, "4")
	t.Setenv(hermesMutateTxDeadlineEnv, "garbage")
	if _, err := hermesMutateGuardConfigFromEnv(); err == nil {
		t.Fatalf("malformed tx_deadline did not fail loud")
	}
}

// TestHermesMutateGuardConfig_ParsesDurationOrSeconds proves both forms parse.
func TestHermesMutateGuardConfig_ParsesDurationOrSeconds(t *testing.T) {
	t.Setenv(hermesMutateTxDeadlineEnv, "120s")
	t.Setenv(hermesMutateRateWindowEnv, "30") // bare seconds
	cfg, err := hermesMutateGuardConfigFromEnv()
	if err != nil {
		t.Fatalf("err=%v want nil", err)
	}
	if cfg.txDeadline != 120*time.Second {
		t.Fatalf("txDeadline=%v want 120s", cfg.txDeadline)
	}
	if cfg.rateWindow != 30*time.Second {
		t.Fatalf("rateWindow=%v want 30s", cfg.rateWindow)
	}
}
