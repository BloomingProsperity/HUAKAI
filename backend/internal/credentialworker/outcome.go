package credentialworker

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

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
