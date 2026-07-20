package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type AuditWriter interface {
	WriteRefreshAudit(ctx context.Context, entry *RefreshAuditEntry) error
}

type RefreshAuditEntry struct {
	TenantID                   int64
	ProviderAccountID          int64
	Outcome                    Outcome
	StormScope                 string
	OldRefreshTokenFingerprint string
	NewRefreshTokenFingerprint string
	ComponentsApplied          []string
	RequestID                  string
	ClientProtocol             string
	Model                      string
	ErrorClass                 string
	ErrorMessageRedacted       string
	OccurredAt                 time.Time
}

type NoopAuditWriter struct{}

func (NoopAuditWriter) WriteRefreshAudit(context.Context, *RefreshAuditEntry) error {
	return nil
}

type RefreshOutcome string

const (
	OutcomeSuccess         RefreshOutcome = "success"
	OutcomeAuthExpired     RefreshOutcome = "auth_expired"
	OutcomeRateLimit       RefreshOutcome = "rate_limit_exceeded"
	OutcomeRiskControl     RefreshOutcome = "risk_control_triggered"
	OutcomeAccountDisabled RefreshOutcome = "account_disabled"
	OutcomeTransientError  RefreshOutcome = "transient_error"
	OutcomeUnknown         RefreshOutcome = "unknown"
)

func ClassifyRefreshError(err error, vendor string, statusCode int) RefreshOutcome {
	if err == nil {
		if statusCode == 0 || (statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices) {
			return OutcomeSuccess
		}
	}

	msg := refreshErrorMessage(err)
	if statusCode == 0 {
		statusCode = statusCodeFromMessage(msg)
	}
	vendor = normalizeProviderName(vendor)

	if statusCode >= http.StatusInternalServerError && statusCode <= 599 {
		return OutcomeTransientError
	}
	if isAccountDisabledMessage(msg) {
		return OutcomeAccountDisabled
	}
	if statusCode == http.StatusTooManyRequests || isRateLimitMessage(msg) {
		return OutcomeRateLimit
	}
	if statusCode == http.StatusForbidden && isRiskControlMessage(msg) {
		return OutcomeRiskControl
	}
	if isAuthExpiredStatus(vendor, statusCode, msg) || isAuthExpiredMessage(msg) {
		return OutcomeAuthExpired
	}
	return OutcomeUnknown
}

func WithRefreshAuditOutcome(err error, outcome string) error {
	if err == nil {
		return nil
	}
	normalized := string(RefreshAuditOutcome(outcome))
	if !IsRefreshAuditOutcome(normalized) {
		return err
	}
	return refreshAuditOutcomeError{err: err, outcome: normalized}
}

func RefreshAuditOutcomeFromError(err error) string {
	var carrier interface{ RefreshAuditOutcome() string }
	if errors.As(err, &carrier) {
		return strings.TrimSpace(carrier.RefreshAuditOutcome())
	}
	return ""
}

func RefreshAuditOutcome(outcome string) Outcome {
	normalized := strings.TrimSpace(outcome)
	switch normalized {
	case string(OutcomeSuccess):
		return OutcomeRefreshSucceeded
	default:
		return Outcome(normalized)
	}
}

func IsRefreshAuditOutcome(outcome string) bool {
	switch strings.TrimSpace(outcome) {
	case string(OutcomeCacheHit),
		string(OutcomeRefreshLockHeld),
		string(OutcomeRefreshSucceeded),
		string(OutcomeRefreshTokenRotated),
		string(OutcomeDBVersionConflict),
		string(OutcomeInvalidGrantRaceRecovered),
		string(OutcomeStormBudgetExhausted),
		string(OutcomeCASLost),
		string(OutcomeTokenMalformed),
		string(OutcomeOAuth401ForceRefresh),
		string(OutcomePermanentDisable),
		string(OutcomeOperatorAttention),
		string(OutcomeMimicryApplied),
		string(OutcomeAuthExpired),
		string(OutcomeRateLimit),
		string(OutcomeRiskControl),
		string(OutcomeAccountDisabled),
		string(OutcomeTransientError):
		return true
	default:
		return false
	}
}

type refreshAuditOutcomeError struct {
	err     error
	outcome string
}

func (e refreshAuditOutcomeError) Error() string {
	return e.err.Error()
}

func (e refreshAuditOutcomeError) Unwrap() error {
	return e.err
}

func (e refreshAuditOutcomeError) RefreshAuditOutcome() string {
	return e.outcome
}

func refreshErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	var parts []string
	for cur := err; cur != nil; cur = errors.Unwrap(cur) {
		parts = append(parts, cur.Error())
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func statusCodeFromMessage(msg string) int {
	for _, code := range []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		if strings.Contains(msg, "status "+strconv.Itoa(code)) ||
			strings.Contains(msg, "returned "+strconv.Itoa(code)) ||
			strings.Contains(msg, " "+strconv.Itoa(code)+" ") {
			return code
		}
	}
	return 0
}

func isAuthExpiredStatus(vendor string, statusCode int, msg string) bool {
	if statusCode != http.StatusUnauthorized {
		return false
	}
	if isKnownRefreshVendor(vendor) {
		return true
	}
	return strings.Contains(msg, "invalid_grant")
}

func isKnownRefreshVendor(vendor string) bool {
	switch vendor {
	case "anthropic", "antigravity", "codex", "copilot", "cursor", "gemini", "google", "kiro", "openai", "windsurf":
		return true
	default:
		return false
	}
}

func isAuthExpiredMessage(msg string) bool {
	for _, needle := range []string{
		"invalid_grant",
		"invalid grant",
		"auth expired",
		"authorization expired",
		"token expired",
		"refresh token expired",
		"refresh token revoked",
		"bad credentials",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func isRateLimitMessage(msg string) bool {
	for _, needle := range []string{
		"rate_limit",
		"rate limit",
		"rate-limit",
		"too many requests",
		"quota exceeded",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func isRiskControlMessage(msg string) bool {
	for _, needle := range []string{
		"risk",
		"risk_control",
		"risk control",
		"abuse",
		"safety",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func isAccountDisabledMessage(msg string) bool {
	for _, needle := range []string{
		"account disabled",
		"account_disabled",
		"account suspended",
		"account deactivated",
		"user disabled",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func normalizeProviderName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
