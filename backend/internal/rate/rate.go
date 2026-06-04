// Package rate implements F-RATE-001: upstream rate-limit + cooldown.
//
// See docs/specs/rate-limiting.md for the released spec.
// This package defines the rate/cooldown contract and provider-specific
// classifier surface.
package rate

import (
	"context"
	"net/http"
	"time"
)

// Service runs the ordered upstream error decision tree.
type Service interface {
	// HandleUpstreamError applies the layered decision tree:
	// pool-mode → custom-codes → temp-unsched rules → status branches.
	HandleUpstreamError(ctx context.Context, accountID int64, statusCode int,
		respHeaders http.Header, respBody []byte) (Decision, error)

	// ClearCascade atomically clears all cooldown state:
	// rate_limit, overload, temp_unsched, model_rate_limits, openai_403_counter.
	ClearCascade(ctx context.Context, accountID int64, actorID string) error

	// UpdateSessionWindow applies the recovery-signal handler.
	UpdateSessionWindow(ctx context.Context, accountID int64, headers http.Header) error
}

// Decision is the outcome of HandleUpstreamError.
type Decision struct {
	StateChange         StateChange
	CooldownUntil       time.Time
	Reason              Reason
	ShouldFailover      bool
	RetryAfterSeconds   int
}

// StateChange classifies the Account-state mutation.
type StateChange int

const (
	StateNoChange StateChange = iota
	StateRateLimited
	StateOverloaded
	StateTempUnsched
	StateModelRateLimited
	StatePermanentDisable
)

// Reason is the structured rate_limit_reason enum per spec §Failure Path.
type Reason string

const (
	ReasonRateLimit5h          Reason = "rate_limit_5h_exceeded"
	ReasonRateLimit7d          Reason = "rate_limit_7d_exceeded"
	ReasonRateLimitBoth        Reason = "rate_limit_both_windows"
	ReasonRateLimitRPM         Reason = "rate_limit_rpm"
	ReasonRateLimitTPM         Reason = "rate_limit_tpm"
	ReasonExtraUsageRequired   Reason = "extra_usage_required"
	ReasonOverloaded           Reason = "overloaded"
	ReasonTokenRefreshRequired Reason = "token_refresh_required"
	ReasonTokenRevoked         Reason = "token_permanently_revoked"
	ReasonKYCRequired          Reason = "kyc_required"
	ReasonOrgDisabled          Reason = "org_disabled"
	ReasonCreditExhausted      Reason = "credit_exhausted"
	ReasonWorkspaceDeactivated Reason = "workspace_deactivated"
	ReasonModelLimitExceeded   Reason = "model_limit_exceeded"
	ReasonTempUnschedRule      Reason = "temp_unsched_rule_matched"
	ReasonOpenAI403Counted     Reason = "openai_403_counted"
	ReasonOpenAI403Disabled    Reason = "openai_403_disabled"
	ReasonAntigravityValidation Reason = "antigravity_403_validation"
	ReasonCustomErrorCode      Reason = "custom_error_code"
)
