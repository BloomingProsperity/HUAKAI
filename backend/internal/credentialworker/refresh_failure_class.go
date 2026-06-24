package credentialworker

import (
	"errors"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
)

type refreshFailureOutcomeError interface {
	RefreshFailureOutcome() string
}

func classifyModeRefreshError(err error) string {
	if errors.Is(err, adapters.ErrCodexOAuthConfigRequired) || errors.Is(err, adapters.ErrGeminiOAuthConfigRequired) ||
		errors.Is(err, ErrOperatorOAuthConfigMissing) || errors.Is(err, ErrProviderAdapterMissing) {
		return "operator_config_required"
	}
	if errors.Is(err, adapters.ErrInvalidCredentialMaterial) {
		return "payload_invalid"
	}
	var outcomeErr refreshFailureOutcomeError
	if errors.As(err, &outcomeErr) {
		if class := normalizeModeRefreshFailureClass(outcomeErr.RefreshFailureOutcome()); class != "" {
			return class
		}
	}
	if class := classifyModeRefreshFailureText(err.Error()); class != "" {
		return class
	}
	return "temporary"
}

func classifyModeRefreshFailureText(text string) string {
	tokens := refreshFailureTokens(text)
	tokenSet := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		tokenSet[token] = struct{}{}
	}
	switch {
	case hasRefreshFailureToken(tokenSet, "invalid_grant") || hasRefreshFailurePair(tokens, "invalid", "grant"):
		return "invalid_grant"
	case hasRefreshFailureToken(tokenSet, "rate_limit_exceeded", "rate_limit") ||
		hasRefreshFailurePair(tokens, "rate", "limit") ||
		hasRefreshFailureTriple(tokens, "too", "many", "requests") ||
		hasRefreshFailurePair(tokens, "status", "429"):
		return "rate_limit_exceeded"
	case hasRefreshFailureToken(tokenSet, "risk_control_triggered", "risk_control") ||
		hasRefreshFailurePair(tokens, "risk", "control"):
		return "risk_control_triggered"
	case hasRefreshFailureToken(tokenSet, "account_disabled") ||
		hasRefreshFailurePair(tokens, "account", "disabled") ||
		hasRefreshFailurePair(tokens, "disabled", "account"):
		return "account_disabled"
	case hasRefreshFailureToken(tokenSet, "decrypt", "payload", "json"):
		return "payload_invalid"
	default:
		return ""
	}
}

func normalizeModeRefreshFailureClass(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "invalid_grant", string(OutcomeAuthExpired):
		return "invalid_grant"
	case "rate_limit", "rate_limited", string(OutcomeRateLimit):
		return "rate_limit_exceeded"
	case string(OutcomeRiskControl):
		return "risk_control_triggered"
	case string(OutcomeAccountDisabled):
		return "account_disabled"
	case "payload_invalid":
		return "payload_invalid"
	case "operator_config_required":
		return "operator_config_required"
	case "temporary", string(OutcomeTransientError), string(OutcomeUnknown):
		return "temporary"
	default:
		return ""
	}
}

func refreshFailureTokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_'
	})
}

func hasRefreshFailureToken(tokens map[string]struct{}, wants ...string) bool {
	for _, want := range wants {
		if _, ok := tokens[want]; ok {
			return true
		}
	}
	return false
}

func hasRefreshFailurePair(tokens []string, first, second string) bool {
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] == first && tokens[i+1] == second {
			return true
		}
	}
	return false
}

func hasRefreshFailureTriple(tokens []string, first, second, third string) bool {
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i] == first && tokens[i+1] == second && tokens[i+2] == third {
			return true
		}
	}
	return false
}
