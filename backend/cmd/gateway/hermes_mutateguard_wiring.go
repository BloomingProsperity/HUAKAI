package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops/mutateguard"
)

// This file parses + builds the S2 "bound the Hermes MUTATING path" guards
// (concurrency semaphore + tx deadline + per-operator-token rate limiter) so a
// burst of mutations cannot exhaust the shared pgxpool / advisory-lock slots and
// brown out the core gateway (audit B4/B5).
//
// Every knob is ADDITIVE and DEFAULT-CONSERVATIVE, and each carries a disable
// sentinel: with NO HUAKAI_HERMES_MUTATE_* env set, the parsed config is the
// built-in defaults; an explicit 0 disables that one guard; and an unset+disabled
// orchestrator is byte-for-byte the legacy unbounded, no-deadline behavior.

const (
	// hermesMutateMaxConcurrencyEnv caps concurrent mutating executions. Default 4
	// (pool MaxConns=16 => <=25% held by mutations). 0/negative disables it.
	hermesMutateMaxConcurrencyEnv = "HUAKAI_HERMES_MUTATE_MAX_CONCURRENCY"
	// hermesMutateAcquireWaitEnv bounds the wait for a concurrency slot before a
	// clean 429 busy. Default 2s.
	hermesMutateAcquireWaitEnv = "HUAKAI_HERMES_MUTATE_ACQUIRE_WAIT"
	// hermesMutateTxDeadlineEnv bounds a single mutation tx (client ctx deadline +
	// SET LOCAL statement_timeout). Default 90s — deliberately 3x the 30s
	// dlq_replay inner claim lease so a legitimately slow settlement completes,
	// while still capping a truly stuck handler. 0 disables it.
	hermesMutateTxDeadlineEnv = "HUAKAI_HERMES_MUTATE_TX_DEADLINE"
	// hermesMutateRatePerTokenEnv caps confirmed mutations per operator token per
	// window. Default 30. 0 disables it.
	hermesMutateRatePerTokenEnv = "HUAKAI_HERMES_MUTATE_RATE_PER_TOKEN"
	// hermesMutateRateWindowEnv is the per-token rate window. Default 1m.
	hermesMutateRateWindowEnv = "HUAKAI_HERMES_MUTATE_RATE_WINDOW"
)

const (
	defaultHermesMutateMaxConcurrency = 4
	defaultHermesMutateAcquireWait    = 2 * time.Second
	// 90s is load-bearing: dlq_replay re-runs Settler.Settle whose inner claim
	// lease is 30s; 90s = 3x headroom so a legit slow settlement is NOT cut while a
	// stuck handler still gets capped. Do NOT default tighter.
	defaultHermesMutateTxDeadline   = 90 * time.Second
	defaultHermesMutateRatePerToken = 30
	defaultHermesMutateRateWindow   = time.Minute
)

// hermesMutateGuardConfig is the parsed S2 guard configuration. The zero value
// (all knobs unset) carries the built-in defaults; an explicit 0 on a knob
// disables that single guard.
type hermesMutateGuardConfig struct {
	maxConcurrency int
	acquireWait    time.Duration
	txDeadline     time.Duration
	ratePerToken   int
	rateWindow     time.Duration
}

// hermesMutateGuardConfigFromEnv parses the five S2 knobs, fail-loud on a
// malformed value (never a silent fallback that would mis-bound the path). An
// UNSET knob takes its conservative default; an explicit 0 disables that guard.
func hermesMutateGuardConfigFromEnv() (hermesMutateGuardConfig, error) {
	cfg := hermesMutateGuardConfig{}
	var err error

	if cfg.maxConcurrency, err = envIntDisable0Default(hermesMutateMaxConcurrencyEnv, defaultHermesMutateMaxConcurrency); err != nil {
		return cfg, err
	}
	if cfg.acquireWait, err = envDurationPositiveDefault(hermesMutateAcquireWaitEnv, defaultHermesMutateAcquireWait); err != nil {
		return cfg, err
	}
	if cfg.txDeadline, err = envDurationDisable0Default(hermesMutateTxDeadlineEnv, defaultHermesMutateTxDeadline); err != nil {
		return cfg, err
	}
	if cfg.ratePerToken, err = envIntDisable0Default(hermesMutateRatePerTokenEnv, defaultHermesMutateRatePerToken); err != nil {
		return cfg, err
	}
	if cfg.rateWindow, err = envDurationPositiveDefault(hermesMutateRateWindowEnv, defaultHermesMutateRateWindow); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// orchestratorOptions returns the additive MutateOrchestrator options for this
// config: the concurrency guard (semaphore + acquire wait) and the tx deadline.
// A disabled (0) guard yields a no-op option, so an all-defaults-disabled config
// gives the legacy orchestrator.
func (c hermesMutateGuardConfig) orchestratorOptions() []hermesops.MutateOption {
	return []hermesops.MutateOption{
		hermesops.WithConcurrencyGuard(mutateguard.NewSemaphore(c.maxConcurrency), c.acquireWait),
		hermesops.WithTxDeadline(c.txDeadline),
	}
}

// newRateLimiter builds the per-operator-token sliding-window limiter (production
// clock). A ratePerToken of 0 yields a disabled limiter (Allow always true).
func (c hermesMutateGuardConfig) newRateLimiter() *mutateguard.RateLimiter {
	return mutateguard.NewRateLimiter(c.ratePerToken, c.rateWindow, 0, nil)
}

// --- fail-loud env helpers (0 = disable sentinel where noted) ----------------

// envIntDisable0Default parses an int knob: unset => fallback; an explicit 0 (the
// disable sentinel) is honored; a negative is clamped to 0 (disabled); a
// non-integer is a fail-loud boot error.
func envIntDisable0Default(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer (0 disables), got %q: %w", name, raw, err)
	}
	if v < 0 {
		v = 0
	}
	return v, nil
}

// envDurationDisable0Default parses a duration knob: unset => fallback; an
// explicit 0 (the disable sentinel) is honored; a malformed value is fail-loud.
func envDurationDisable0Default(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	d, err := parseDurationOrSeconds(name, raw)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		d = 0
	}
	return d, nil
}

// envDurationPositiveDefault parses a duration knob that must stay positive
// (acquire wait / rate window have no meaningful "disabled" form — they only
// matter when their paired guard is enabled). Unset => fallback; <=0 or malformed
// is fail-loud.
func envDurationPositiveDefault(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	d, err := parseDurationOrSeconds(name, raw)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration, got %q", name, raw)
	}
	return d, nil
}

// parseDurationOrSeconds accepts either a Go duration (90s, 1m) or a bare integer
// number of seconds, matching config.envPositiveDurationDefault's lenient style.
func parseDurationOrSeconds(name, raw string) (time.Duration, error) {
	if d, err := time.ParseDuration(raw); err == nil {
		return d, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration like 90s or seconds, got %q: %w", name, raw, err)
	}
	return time.Duration(seconds) * time.Second, nil
}
