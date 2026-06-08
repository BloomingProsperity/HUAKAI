package rate

import (
	"context"
	"net/http"
	"strconv"
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

type fakeSessionWindowStore struct {
	calls  int
	update SessionWindowUpdate
}

func (f *fakeSessionWindowStore) UpdateProviderAccountSessionWindow5h(_ context.Context, update SessionWindowUpdate) error {
	f.calls++
	f.update = update
	return nil
}

func TestSessionWindowParse(t *testing.T) {
	// MUTATION: UpdateSessionWindow 不写列(no-op) -> fake.calls 仍为 0 -> RED.
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	resetAt := now.Add(time.Hour)
	store := &fakeSessionWindowStore{}
	svc := NewUpstreamRateServiceWithSessionWindowStore(func() time.Time { return now }, time.Minute, store)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(resetAt.Unix(), 10))

	if err := svc.UpdateSessionWindow(context.Background(), 101, headers); err != nil {
		t.Fatalf("UpdateSessionWindow: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("writes=%d, want 1", store.calls)
	}
	if store.update.ProviderAccountID != 101 {
		t.Fatalf("provider_account_id=%d, want 101", store.update.ProviderAccountID)
	}
	if !store.update.WindowEnd.Equal(resetAt) {
		t.Fatalf("window_end=%s, want %s", store.update.WindowEnd, resetAt)
	}
	if !store.update.WindowStart.Equal(resetAt.Add(-5 * time.Hour)) {
		t.Fatalf("window_start=%s, want %s", store.update.WindowStart, resetAt.Add(-5*time.Hour))
	}
	if store.update.Status != "allowed" {
		t.Fatalf("status=%q, want allowed", store.update.Status)
	}

	guardHeaders := http.Header{}
	guardHeaders.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	if err := svc.UpdateSessionWindow(context.Background(), 101, guardHeaders); err != nil {
		t.Fatalf("UpdateSessionWindow without reset: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("writes after no-reset guard=%d, want unchanged 1", store.calls)
	}
}

func TestSessionWindowRejectsBadReset(t *testing.T) {
	// MUTATION: 不做范围校验 -> 过去超过 5h 的脏值会写入 -> RED.
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	store := &fakeSessionWindowStore{}
	svc := NewUpstreamRateServiceWithSessionWindowStore(func() time.Time { return now }, time.Minute, store)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "rejected")
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(now.Add(-99999*time.Second).Unix(), 10))

	if err := svc.UpdateSessionWindow(context.Background(), 101, headers); err != nil {
		t.Fatalf("UpdateSessionWindow: %v", err)
	}
	if store.calls != 0 {
		t.Fatalf("writes=%d, want 0 for out-of-range reset", store.calls)
	}
}
