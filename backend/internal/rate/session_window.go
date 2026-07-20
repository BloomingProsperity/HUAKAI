package rate

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type codexWindowHeader struct {
	usedPercent *float64
	resetAfter  *time.Duration
	minutes     *int64
}

func applyCodexWindowHeaders(headers http.Header, now time.Time, update *SessionWindowUpdate) {
	if headers == nil || update == nil {
		return
	}
	primary := readCodexWindowHeader(headers, "primary")
	secondary := readCodexWindowHeader(headers, "secondary")
	if primary.empty() && secondary.empty() {
		return
	}

	short, long := classifyCodexWindows(primary, secondary)
	setCodexWindow(short, now, sessionWindow5hDuration,
		&update.Window5hStart, &update.Window5hEnd, &update.Window5hStatus, &update.Window5hUtilization)
	setCodexWindow(long, now, sessionWindow7dDuration,
		&update.Window7dStart, &update.Window7dEnd, &update.Window7dStatus, &update.Window7dUtilization)
}

func readCodexWindowHeader(headers http.Header, lane string) codexWindowHeader {
	prefix := "x-codex-" + lane + "-"
	return codexWindowHeader{
		usedPercent: parseBoundedPercent(headers.Get(prefix + "used-percent")),
		resetAfter:  parseResetAfter(headers.Get(prefix + "reset-after-seconds")),
		minutes:     parsePositiveInt64(headers.Get(prefix + "window-minutes")),
	}
}

func (w codexWindowHeader) empty() bool {
	return w.usedPercent == nil && w.resetAfter == nil && w.minutes == nil
}

func classifyCodexWindows(primary, secondary codexWindowHeader) (codexWindowHeader, codexWindowHeader) {
	if primary.minutes != nil && secondary.minutes != nil {
		if *primary.minutes < *secondary.minutes {
			return primary, secondary
		}
		return secondary, primary
	}
	if primary.minutes != nil {
		if *primary.minutes <= 360 {
			return primary, secondary
		}
		return secondary, primary
	}
	if secondary.minutes != nil {
		if *secondary.minutes <= 360 {
			return secondary, primary
		}
		return primary, secondary
	}
	// 上游省略窗口时长时按协议角色兜底：主窗口是短周期，次窗口是长周期。
	return primary, secondary
}

func setCodexWindow(window codexWindowHeader, now time.Time, duration time.Duration, start, end **time.Time, status **string, utilization **float64) {
	if window.usedPercent != nil {
		*utilization = window.usedPercent
	}
	if window.resetAfter == nil || *window.resetAfter < 0 || *window.resetAfter > sessionWindow7dDuration {
		return
	}
	windowEnd := now.Add(*window.resetAfter).UTC()
	windowStart := windowEnd.Add(-duration)
	windowStatus := "active"
	if window.usedPercent != nil && *window.usedPercent >= 100 {
		windowStatus = "exhausted"
	}
	*start = &windowStart
	*end = &windowEnd
	*status = &windowStatus
}

func parseBoundedPercent(raw string) *float64 {
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "%"))
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		return nil
	}
	return &value
}

func parseResetAfter(raw string) *time.Duration {
	seconds := parsePositiveInt64AllowZero(raw)
	if seconds == nil {
		return nil
	}
	duration := time.Duration(*seconds) * time.Second
	return &duration
}

func parsePositiveInt64(raw string) *int64 {
	value := parsePositiveInt64AllowZero(raw)
	if value == nil || *value <= 0 {
		return nil
	}
	return value
}

func parsePositiveInt64AllowZero(raw string) *int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return nil
	}
	return &value
}

func setSessionWindowFromHeaders(
	headers http.Header,
	now time.Time,
	statusHeaders, resetHeaders, utilizationHeaders []string,
	duration time.Duration,
	start, end **time.Time,
	status **string,
	utilization **float64,
) {
	parsedStatus := sessionWindowStatus(headers, statusHeaders)
	if windowEnd, ok := sessionWindowReset(headers, resetHeaders, now, duration); ok && parsedStatus != "" {
		windowStart := windowEnd.Add(-duration).UTC()
		windowEnd = windowEnd.UTC()
		*start = &windowStart
		*end = &windowEnd
		*status = &parsedStatus
	}
	*utilization = sessionWindowUtilization(headers, utilizationHeaders)
}

func sessionWindowStatus(headers http.Header, names []string) string {
	if headers == nil {
		return ""
	}
	for _, name := range names {
		status := strings.TrimSpace(headers.Get(name))
		if status == "" {
			continue
		}
		if len(status) > 64 {
			status = status[:64]
		}
		return status
	}
	return ""
}

func sessionWindowReset(headers http.Header, names []string, now time.Time, duration time.Duration) (time.Time, bool) {
	if headers == nil {
		return time.Time{}, false
	}
	var raw string
	for _, name := range names {
		raw = strings.TrimSpace(headers.Get(name))
		if raw != "" {
			break
		}
	}
	if raw == "" {
		return time.Time{}, false
	}
	resetUnix, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || resetUnix <= 0 {
		return time.Time{}, false
	}
	if resetUnix > 100_000_000_000 {
		resetUnix = resetUnix / 1000
	}
	resetAt := time.Unix(resetUnix, 0).UTC()
	if resetAt.Before(now.Add(-duration)) || resetAt.After(now.Add(sessionWindow7dDuration)) {
		return time.Time{}, false
	}
	return resetAt, true
}

func sessionWindowUtilization(headers http.Header, names []string) *float64 {
	if headers == nil {
		return nil
	}
	for _, name := range names {
		raw := strings.TrimSpace(headers.Get(name))
		if raw == "" {
			continue
		}
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "%"))
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			return nil
		}
		return &value
	}
	return nil
}

func (u SessionWindowUpdate) hasValues() bool {
	return u.Window5hStart != nil || u.Window5hEnd != nil || u.Window5hStatus != nil || u.Window5hUtilization != nil ||
		u.Window7dStart != nil || u.Window7dEnd != nil || u.Window7dStatus != nil || u.Window7dUtilization != nil
}

func (u SessionWindowUpdate) observationOutcome() string {
	complete := 0
	if u.Window5hEnd != nil && u.Window5hUtilization != nil {
		complete++
	}
	if u.Window7dEnd != nil && u.Window7dUtilization != nil {
		complete++
	}
	if complete == 2 {
		return QuotaSnapshotOutcomeSuccess
	}
	return QuotaSnapshotOutcomePartial
}

func (u SessionWindowUpdate) validateObservation() error {
	if u.ObservedAt == nil {
		if u.ObservationSource != "" || u.ObservationOutcome != "" || u.ObservationErrorClass != "" {
			return fmt.Errorf("quota snapshot observation timestamp is required")
		}
		return nil
	}
	if u.ObservedAt.IsZero() {
		return fmt.Errorf("quota snapshot observation timestamp is invalid")
	}
	if u.ObservationSource != QuotaSnapshotSourceUsageEndpoint && u.ObservationSource != QuotaSnapshotSourceResponseHeaders {
		return fmt.Errorf("quota snapshot observation source is invalid")
	}
	switch u.ObservationOutcome {
	case QuotaSnapshotOutcomeSuccess, QuotaSnapshotOutcomePartial:
		if u.ObservationErrorClass != "" {
			return fmt.Errorf("successful quota snapshot observation cannot carry an error class")
		}
	case QuotaSnapshotOutcomeFailed:
		if strings.TrimSpace(u.ObservationErrorClass) == "" {
			return fmt.Errorf("failed quota snapshot observation requires an error class")
		}
	default:
		return fmt.Errorf("quota snapshot observation outcome is invalid")
	}
	return nil
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}
