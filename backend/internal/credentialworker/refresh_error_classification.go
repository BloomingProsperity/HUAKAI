package credentialworker

import (
	"errors"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
)

func providerFailureCooldown(vendor string) time.Duration {
	return time.Minute
}

type refreshAttemptScheduler interface {
	NextRefreshAttempt(time.Time) time.Time
}

func nextModeRefreshAttempt(err error, vendor string, now time.Time) time.Time {
	var scheduler refreshAttemptScheduler
	if errors.As(err, &scheduler) {
		if next := scheduler.NextRefreshAttempt(now); next.After(now) {
			return next.UTC()
		}
	}
	return now.Add(providerFailureCooldown(vendor)).UTC()
}

func ClassifyRefreshErrorClass(err error) string {
	if err == nil {
		return ""
	}
	return classifyModeRefreshError(err)
}

func classifyModeRefreshError(err error) string {
	if errors.Is(err, adapters.ErrCodexOAuthConfigRequired) || errors.Is(err, adapters.ErrGeminiOAuthConfigRequired) ||
		errors.Is(err, ErrOperatorOAuthConfigMissing) || errors.Is(err, ErrProviderAdapterMissing) {
		return "operator_config_required"
	}
	if errors.Is(err, adapters.ErrInvalidCredentialMaterial) {
		return "payload_invalid"
	}
	if errors.Is(err, projectenrich.ErrProjectMetadataConflict) {
		return "project_metadata_conflict"
	}
	if errors.Is(err, projectenrich.ErrProjectMetadataUnavailable) {
		return "project_metadata_unavailable"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "invalid_grant"):
		return "invalid_grant"
	case strings.Contains(message, "rate_limit_exceeded") || strings.Contains(message, "rate limit") ||
		strings.Contains(message, "rate_limit") || strings.Contains(message, "too many requests") ||
		strings.Contains(message, "status 429"):
		return "rate_limit_exceeded"
	case strings.Contains(message, "risk_control_triggered") || strings.Contains(message, "risk control") ||
		strings.Contains(message, "risk_control"):
		return "risk_control_triggered"
	case strings.Contains(message, "account_disabled") || strings.Contains(message, "account disabled") ||
		strings.Contains(message, "disabled account"):
		return "account_disabled"
	case strings.Contains(message, "decrypt"), strings.Contains(message, "payload"), strings.Contains(message, "json"):
		return "payload_invalid"
	default:
		return "temporary"
	}
}

func withModeRefreshAuditOutcome(err error, failureClass string) error {
	if err == nil {
		return nil
	}
	var outcome string
	switch failureClass {
	case "invalid_grant", "auth_expired":
		outcome = string(auth.OutcomeAuthExpired)
	case "rate_limit_exceeded":
		outcome = string(auth.OutcomeRateLimit)
	case "risk_control_triggered":
		outcome = string(auth.OutcomeRiskControl)
	case "account_disabled":
		outcome = string(auth.OutcomeAccountDisabled)
	case "payload_invalid":
		outcome = string(auth.OutcomeTokenMalformed)
	case "operator_config_required":
		outcome = string(auth.OutcomePermanentDisable)
	case "project_metadata_conflict", "project_metadata_unavailable":
		outcome = string(auth.OutcomeOperatorAttention)
	default:
		outcome = string(auth.OutcomeTransientError)
	}
	return auth.WithRefreshAuditOutcome(err, outcome)
}
