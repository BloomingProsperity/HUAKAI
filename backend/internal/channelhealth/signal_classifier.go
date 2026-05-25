package channelhealth

import (
	"strings"
)

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
	code := strings.ToLower(strings.TrimSpace(in.ErrorCode))
	safe := strings.ToLower(strings.TrimSpace(in.SafeErrorClass))
	raw := strings.ToLower(in.RawUpstreamText)

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
	if in.StatusCode >= 400 {
		return ClassifierResult{Class: SignalChannelError, Confidence: ConfidenceObserved}
	}
	if in.LatencyMS > 0 {
		return ClassifierResult{Class: SignalSuccess, Confidence: ConfidenceObserved}
	}
	return ClassifierResult{Class: SignalSuccess, Confidence: ConfidenceObserved}
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
