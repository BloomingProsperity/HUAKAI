// Package auth implements F-AUTH-005: upstream Provider Account credential
// management (OAuth refresh, token cache, storm prevention, mimicry policy).
//
// NOT user-facing auth (that is F-AUTH-001..004). See
// docs/specs/upstream-credential-management.md for the released spec.
// Phase 3 skeleton ONLY per DR-008.
package auth

import "context"

// TokenProvider returns a valid access_token for an upstream Provider Account.
// Implementation runs the Phase A-H pipeline per spec.
type TokenProvider interface {
	// GetAccessToken either returns the cached token, refreshes, or returns
	// error if the Account is in temp-unsched / disabled state.
	GetAccessToken(ctx context.Context, tenantID, accountID int64) (string, error)
}

// MimicryEngine applies Claude Code mimicry per F-AUTH-005 §Phase H, ONLY
// when the per-Pool mimicry_policy.enabled = true AND legal_review_id present.
type MimicryEngine interface {
	// ApplyToBody returns the transformed request body + audit attributes.
	// 6-step transform: system rewrite + cache_control strip + breakpoints +
	// tool name obfuscation + metadata user_id injection + tools[-1] breakpoint.
	ApplyToBody(ctx context.Context, accountID int64, originalBody []byte) (
		transformed []byte, audit MimicryAudit, err error)
}

// MimicryAudit records what was applied for the audit-event row.
type MimicryAudit struct {
	ComponentsApplied   []string
	MimicryPolicyVersion string
}

// Outcome enumerates audit outcomes per spec §Phase E + H + storm budget.
type Outcome string

const (
	OutcomeCacheHit                  Outcome = "cache_hit"
	OutcomeRefreshLockHeld           Outcome = "refresh_lock_held"
	OutcomeRefreshSucceeded          Outcome = "refresh_succeeded"
	OutcomeRefreshTokenRotated       Outcome = "refresh_token_rotated"
	OutcomeDBVersionConflict         Outcome = "db_version_conflict"
	OutcomeInvalidGrantRaceRecovered Outcome = "invalid_grant_race_recovered"
	OutcomeStormBudgetExhausted      Outcome = "storm_budget_exhausted"
	OutcomeCASLost                   Outcome = "cas_lost"
	OutcomeTokenMalformed            Outcome = "token_malformed"
	OutcomeOAuth401ForceRefresh      Outcome = "oauth_401_force_refresh"
	OutcomePermanentDisable          Outcome = "permanent_disable"
	OutcomeMimicryApplied            Outcome = "mimicry_applied"
)

// TODO(phase-4): implement provider-neutral refresh state machine + per-provider
// adapters (Antigravity, OpenAI, Gemini, Anthropic) + 3-scope storm controller
// + CAS persistence + OAuth error sanitizer + mimicry engine.
