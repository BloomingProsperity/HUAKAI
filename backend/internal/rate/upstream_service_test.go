package rate

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestUpstreamRateService429RetryAfterDeltaSetsCooldown(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	svc := NewUpstreamRateService(func() time.Time { return now }, time.Minute)
	headers := http.Header{"Retry-After": []string{"3600"}}

	dec, err := svc.HandleUpstreamError(context.Background(), 101, http.StatusTooManyRequests, headers, []byte(`{"error":"rate limited"}`))
	if err != nil {
		t.Fatalf("HandleUpstreamError: %v", err)
	}
	if dec.StateChange != StateRateLimited {
		t.Fatalf("StateChange=%v want StateRateLimited", dec.StateChange)
	}
	if dec.Reason != ReasonRateLimitRPM {
		t.Fatalf("Reason=%s want %s", dec.Reason, ReasonRateLimitRPM)
	}
	if dec.RetryAfterSeconds != 3600 {
		t.Fatalf("RetryAfterSeconds=%d want 3600", dec.RetryAfterSeconds)
	}
	if !dec.CooldownUntil.Equal(now.Add(time.Hour)) {
		t.Fatalf("CooldownUntil=%s want %s", dec.CooldownUntil, now.Add(time.Hour))
	}
	if !dec.ShouldFailover {
		t.Fatal("ShouldFailover=false want true")
	}
}

func TestUpstreamRateService529RetryAfterHTTPDateSetsOverloadCooldown(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	until := now.Add(2 * time.Minute).Truncate(time.Second)
	svc := NewUpstreamRateService(func() time.Time { return now }, time.Minute)
	headers := http.Header{"Retry-After": []string{until.Format(http.TimeFormat)}}

	dec, err := svc.HandleUpstreamError(context.Background(), 101, 529, headers, nil)
	if err != nil {
		t.Fatalf("HandleUpstreamError: %v", err)
	}
	if dec.StateChange != StateOverloaded {
		t.Fatalf("StateChange=%v want StateOverloaded", dec.StateChange)
	}
	if dec.Reason != ReasonOverloaded {
		t.Fatalf("Reason=%s want %s", dec.Reason, ReasonOverloaded)
	}
	if dec.RetryAfterSeconds != 120 {
		t.Fatalf("RetryAfterSeconds=%d want 120", dec.RetryAfterSeconds)
	}
	if !dec.CooldownUntil.Equal(until) {
		t.Fatalf("CooldownUntil=%s want %s", dec.CooldownUntil, until)
	}
}

func TestUpstreamRateServiceIgnoresNonRateLimitStatus(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	svc := NewUpstreamRateService(func() time.Time { return now }, time.Minute)
	headers := http.Header{"Retry-After": []string{"3600"}}

	dec, err := svc.HandleUpstreamError(context.Background(), 101, http.StatusInternalServerError, headers, nil)
	if err != nil {
		t.Fatalf("HandleUpstreamError: %v", err)
	}
	if dec != (Decision{}) {
		t.Fatalf("Decision=%+v want zero value for non-rate-limit status", dec)
	}
}
