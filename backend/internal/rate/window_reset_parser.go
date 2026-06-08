package rate

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ParseMultiWindowReset extracts vendor multi-window reset hints for 429
// cooldown refinement. It returns ok=false when no exceeded window is proven.
func ParseMultiWindowReset(headers http.Header, now time.Time) (time.Time, Reason, bool) {
	if headers == nil {
		return time.Time{}, "", false
	}
	reset5h, ok5h := exceededWindowReset(headers, now, "anthropic-ratelimit-unified-5h")
	reset7d, ok7d := exceededWindowReset(headers, now, "anthropic-ratelimit-unified-7d")
	switch {
	case ok5h && ok7d:
		if reset7d.After(reset5h) {
			return reset7d, ReasonRateLimitBoth, true
		}
		return reset5h, ReasonRateLimitBoth, true
	case ok5h:
		return reset5h, ReasonRateLimit5h, true
	case ok7d:
		return reset7d, ReasonRateLimit7d, true
	default:
		return time.Time{}, "", false
	}
}

func exceededWindowReset(headers http.Header, now time.Time, prefix string) (time.Time, bool) {
	if !windowExceeded(headers, prefix) {
		return time.Time{}, false
	}
	raw := strings.TrimSpace(headers.Get(prefix + "-reset"))
	if raw == "" {
		return time.Time{}, false
	}
	resetUnix, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || resetUnix <= 0 {
		return time.Time{}, false
	}
	resetAt := time.Unix(resetUnix, 0).UTC()
	if !resetAt.After(now.UTC()) {
		return time.Time{}, false
	}
	return resetAt, true
}

func windowExceeded(headers http.Header, prefix string) bool {
	switch strings.ToLower(strings.TrimSpace(headers.Get(prefix + "-surpassed-threshold"))) {
	case "1", "true", "yes", "on":
		return true
	}
	rawUtilization := strings.TrimSpace(headers.Get(prefix + "-utilization"))
	if rawUtilization == "" {
		return false
	}
	rawUtilization = strings.TrimSuffix(rawUtilization, "%")
	utilization, err := strconv.ParseFloat(strings.TrimSpace(rawUtilization), 64)
	return err == nil && utilization >= 100
}
