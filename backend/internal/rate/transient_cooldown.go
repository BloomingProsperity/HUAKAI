package rate

import (
	"net/http"
	"time"
)

// TransientCooldownConfig 控制对瞬态上游失败附加的短冷却。Duration 为零时
// 保留原有的无冷却行为。
type TransientCooldownConfig struct {
	Duration time.Duration
}

// TransientCooldown 对适用短冷却的上游状态码进行分类。
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
