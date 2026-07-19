package credentialworker

import (
	"errors"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
)

func providerFailureCooldown(vendor string) time.Duration {
	switch normalizeProviderName(vendor) {
	case credentialstore.VendorGemini:
		return 0
	default:
		return time.Minute
	}
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
