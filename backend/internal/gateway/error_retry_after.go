package gateway

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// retryAfterMillis 解析 Retry-After 的秒数或 HTTP 日期格式。
func retryAfterMillis(headers http.Header) int64 {
	if headers == nil {
		return 0
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return int64(seconds * 1000)
	}
	if when, err := http.ParseTime(raw); err == nil {
		delta := time.Until(when)
		if delta <= 0 {
			return 0
		}
		return delta.Milliseconds()
	}
	return 0
}

// retryAfterFromBody 从上游错误体提取可恢复时间，避免退化成固定冷却。
func retryAfterFromBody(body []byte, now time.Time) int64 {
	if len(body) == 0 {
		return 0
	}
	var parsed struct {
		Error struct {
			ResetsAt        *int64   `json:"resets_at"`
			ResetsInSeconds *float64 `json:"resets_in_seconds"`
			Details         []struct {
				Type       string          `json:"@type"`
				RetryDelay json.RawMessage `json:"retryDelay"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0
	}
	if parsed.Error.ResetsInSeconds != nil && *parsed.Error.ResetsInSeconds > 0 {
		return int64(*parsed.Error.ResetsInSeconds * 1000)
	}
	if parsed.Error.ResetsAt != nil && *parsed.Error.ResetsAt > 0 {
		if delta := time.Unix(*parsed.Error.ResetsAt, 0).Sub(now); delta > 0 {
			return delta.Milliseconds()
		}
	}
	for _, detail := range parsed.Error.Details {
		if !strings.Contains(detail.Type, "google.rpc.RetryInfo") {
			continue
		}
		if delay, ok := parseGoogleRetryDelay(detail.RetryDelay); ok {
			return delay.Milliseconds()
		}
	}
	return 0
}

const maximumBodyRetryDelay = 30 * 24 * time.Hour

func parseGoogleRetryDelay(raw json.RawMessage) (time.Duration, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		delay, err := time.ParseDuration(strings.TrimSpace(text))
		return boundedBodyRetryDelay(delay, err == nil)
	}
	var value struct {
		Seconds json.RawMessage `json:"seconds"`
		Nanos   int64           `json:"nanos"`
	}
	if err := json.Unmarshal(raw, &value); err != nil || value.Nanos < 0 || value.Nanos >= int64(time.Second) {
		return 0, false
	}
	seconds, ok := parseJSONInt64(value.Seconds)
	if !ok || seconds < 0 || seconds > int64(maximumBodyRetryDelay/time.Second) {
		return 0, false
	}
	return boundedBodyRetryDelay(time.Duration(seconds)*time.Second+time.Duration(value.Nanos), true)
}

func parseJSONInt64(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, true
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		value, parseErr := strconv.ParseInt(number.String(), 10, 64)
		return value, parseErr == nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	return value, err == nil
}

func boundedBodyRetryDelay(delay time.Duration, parsed bool) (time.Duration, bool) {
	if !parsed || delay <= 0 {
		return 0, false
	}
	if delay > maximumBodyRetryDelay {
		return maximumBodyRetryDelay, true
	}
	return delay, true
}
