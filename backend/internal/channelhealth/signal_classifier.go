package channelhealth

import (
	"strconv"
	"strings"
)

type ClassifierConfig struct {
	DisableKeywords     []string
	DisableStatusRanges []string
}

type ClassifierInput struct {
	StatusCode        int
	ErrorCode         string
	SafeErrorClass    string
	RawUpstreamText   string
	LocalGatewayError bool
	LatencyMS         int64
}

type ClassifierResult struct {
	Class      SignalClass
	Confidence ConfidenceTier
}

func Classify(in ClassifierInput) ClassifierResult {
	return ClassifyWithConfig(in, ClassifierConfig{})
}

func ClassifyWithConfig(in ClassifierInput, cfg ClassifierConfig) ClassifierResult {
	code := strings.ToLower(strings.TrimSpace(in.ErrorCode))
	safe := strings.ToLower(strings.TrimSpace(in.SafeErrorClass))
	raw := strings.ToLower(in.RawUpstreamText)

	base := classifyDefault(in, code, safe, raw)
	if isBanSignal(base.Class) {
		return base
	}
	if matchesDisableKeyword(code, safe, cfg.DisableKeywords) || matchesDisableStatusRange(in.StatusCode, cfg.DisableStatusRanges) {
		return ClassifierResult{Class: SignalPolicyAutoDisabled, Confidence: ConfidenceObserved}
	}
	return base
}

func classifyDefault(in ClassifierInput, code, safe, raw string) ClassifierResult {
	switch {
	case containsAny(code, safe, raw, "account_suspended", "account suspended", "suspended account"):
		return ClassifierResult{Class: SignalAccountSuspended, Confidence: ConfidenceObserved}
	case containsAny(code, safe, raw, "token_revoked", "invalid_token", "token revoked", "revoked token"):
		return ClassifierResult{Class: SignalTokenRevoked, Confidence: ConfidenceObserved}
	case containsAny(code, safe, raw, "credential_revoked", "api key revoked", "key revoked"):
		return ClassifierResult{Class: SignalCredentialRevoked, Confidence: ConfidenceObserved}
	case containsAny(code, safe, raw, "account_disabled", "disabled account", "account disabled"):
		return ClassifierResult{Class: SignalAccountDisabled, Confidence: ConfidenceObserved}
	case containsAny(code, safe, raw, "workspace_disabled", "subscription_disabled", "workspace disabled", "subscription disabled"):
		return ClassifierResult{Class: SignalSubscriptionOrWorkspaceDisabled, Confidence: ConfidenceObserved}
	}

	if in.LocalGatewayError && in.StatusCode >= 500 {
		return ClassifierResult{Class: SignalLocalGateway5xx, Confidence: ConfidenceObserved}
	}
	if in.StatusCode == 429 || safe == "rate_limit" || code == "rate_limit" {
		return ClassifierResult{Class: SignalRateLimit, Confidence: ConfidenceObserved}
	}
	if in.StatusCode == 403 && containsAny(code, safe, raw, "rate", "quota", "limit") {
		return ClassifierResult{Class: SignalRateLimit, Confidence: ConfidenceInferred}
	}
	if in.StatusCode >= 500 {
		return ClassifierResult{Class: SignalUpstream5xx, Confidence: ConfidenceObserved}
	}
	if isClientMalformed4xx(in.StatusCode, code, safe, raw) {
		return ClassifierResult{Class: SignalClientMalformed, Confidence: ConfidenceObserved}
	}
	if in.StatusCode >= 400 {
		return ClassifierResult{Class: SignalChannelError, Confidence: ConfidenceObserved}
	}
	if in.LatencyMS > 0 {
		return ClassifierResult{Class: SignalSuccess, Confidence: ConfidenceObserved}
	}
	return ClassifierResult{Class: SignalSuccess, Confidence: ConfidenceObserved}
}

func isClientMalformed4xx(statusCode int, code, safe, raw string) bool {
	switch statusCode {
	case 413, 422:
		return true
	case 400:
		return containsAny(code, safe, raw,
			"request_too_large",
			"invalid_request",
			"invalid request",
			"context_length_exceeded",
			"context length",
			"too many tokens",
			"maximum context",
			"malformed",
			"bad request",
		)
	default:
		return false
	}
}

func matchesDisableKeyword(code, safe string, keywords []string) bool {
	for _, keyword := range keywords {
		needle := strings.ToLower(strings.TrimSpace(keyword))
		if needle == "" {
			continue
		}
		if strings.Contains(code, needle) || strings.Contains(safe, needle) {
			return true
		}
	}
	return false
}

func matchesDisableStatusRange(statusCode int, ranges []string) bool {
	if statusCode <= 0 {
		return false
	}
	for _, raw := range ranges {
		min, max, ok := parseStatusRange(raw)
		if !ok {
			continue
		}
		if statusCode >= min && statusCode <= max {
			return true
		}
	}
	return false
}

func parseStatusRange(raw string) (int, int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, false
	}
	if !strings.Contains(raw, "-") {
		status, err := strconv.Atoi(raw)
		if err != nil || status < 100 || status > 599 {
			return 0, 0, false
		}
		return status, status, true
	}
	parts := strings.SplitN(raw, "-", 2)
	min, minErr := strconv.Atoi(strings.TrimSpace(parts[0]))
	max, maxErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if minErr != nil || maxErr != nil || min < 100 || max > 599 || min > max {
		return 0, 0, false
	}
	return min, max, true
}

func containsAny(values ...string) bool {
	if len(values) < 2 {
		return false
	}
	haystacks := values[:3]
	needles := values[3:]
	for _, haystack := range haystacks {
		for _, needle := range needles {
			if needle != "" && strings.Contains(haystack, needle) {
				return true
			}
		}
	}
	return false
}
