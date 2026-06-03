package rate

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultUpstreamCooldown = 5 * time.Minute

type upstreamRateService struct {
	now             func() time.Time
	defaultCooldown time.Duration
}

func NewUpstreamRateService(clock func() time.Time, defaultCooldown time.Duration) Service {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	if defaultCooldown <= 0 {
		defaultCooldown = defaultUpstreamCooldown
	}
	return &upstreamRateService{now: clock, defaultCooldown: defaultCooldown}
}

func (s *upstreamRateService) HandleUpstreamError(ctx context.Context, accountID int64, statusCode int, respHeaders http.Header, respBody []byte) (Decision, error) {
	_ = ctx
	_ = accountID
	_ = respBody
	if statusCode != http.StatusTooManyRequests && statusCode != 529 {
		return Decision{}, nil
	}
	now := s.now().UTC()
	until, retryAfterSeconds, ok := retryAfterCooldown(respHeaders, now)
	if !ok {
		retryAfterSeconds = durationSeconds(s.defaultCooldown)
		until = now.Add(time.Duration(retryAfterSeconds) * time.Second)
	}
	dec := Decision{
		CooldownUntil:     until.UTC(),
		ShouldFailover:    true,
		RetryAfterSeconds: retryAfterSeconds,
	}
	if statusCode == 529 {
		dec.StateChange = StateOverloaded
		dec.Reason = ReasonOverloaded
		return dec, nil
	}
	dec.StateChange = StateRateLimited
	dec.Reason = ReasonRateLimitRPM
	return dec, nil
}

func (s *upstreamRateService) ClearCascade(ctx context.Context, accountID int64, actorID string) error {
	_ = ctx
	_ = accountID
	_ = actorID
	return nil
}

func (s *upstreamRateService) UpdateSessionWindow(ctx context.Context, accountID int64, headers http.Header) error {
	_ = ctx
	_ = accountID
	_ = headers
	return nil
}

func retryAfterCooldown(headers http.Header, now time.Time) (time.Time, int, bool) {
	if headers == nil {
		return time.Time{}, 0, false
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return time.Time{}, 0, false
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds <= 0 {
			return time.Time{}, 0, false
		}
		return now.Add(time.Duration(seconds) * time.Second).UTC(), seconds, true
	}
	when, err := http.ParseTime(raw)
	if err != nil {
		return time.Time{}, 0, false
	}
	delta := when.Sub(now)
	if delta <= 0 {
		return time.Time{}, 0, false
	}
	return when.UTC(), durationSeconds(delta), true
}

func durationSeconds(d time.Duration) int {
	seconds := int(d / time.Second)
	if d%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

var _ Service = (*upstreamRateService)(nil)
