package rate

import (
	"net/http"
	"time"
)

// TransientCooldownConfig controls additive short cooldowns for transient
// upstream failures. A zero Duration keeps the legacy no-cooldown behavior.
type TransientCooldownConfig struct {
	Duration time.Duration
}

// TransientCooldown classifies short-cooldown upstream statuses.
func TransientCooldown(statusCode int, cfg TransientCooldownConfig) (time.Duration, Reason, bool) {
	if cfg.Duration <= 0 {
		return 0, "", false
	}
	switch statusCode {
	case http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout:
		return cfg.Duration, ReasonUpstreamTransient, true
	case http.StatusServiceUnavailable:
		return cfg.Duration, ReasonOverloaded, true
	default:
		return 0, "", false
	}
}
