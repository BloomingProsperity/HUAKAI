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

func TestDisableCooling(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	headers := http.Header{"Retry-After": []string{"3600"}}
	svc := NewUpstreamRateService(func() time.Time { return now }, time.Minute, WithDisableCooling(true))

	dec, err := svc.HandleUpstreamError(context.Background(), 101, http.StatusTooManyRequests, headers, nil)
	if err != nil {
		t.Fatalf("HandleUpstreamError: %v", err)
	}
	if !dec.ShouldFailover {
		t.Fatal("ShouldFailover=false want true")
	}
	if dec.StateChange != StateRateLimited {
		t.Fatalf("StateChange=%v want StateRateLimited", dec.StateChange)
	}
	if dec.Reason != ReasonRateLimitRPM {
		t.Fatalf("Reason=%s want %s", dec.Reason, ReasonRateLimitRPM)
	}
	// 变异:在 disable-cooling 生效时仍设置 CooldownUntil 必然转红。
	if !dec.CooldownUntil.IsZero() {
		t.Fatalf("CooldownUntil=%s want zero when cooling disabled", dec.CooldownUntil)
	}

	// 守卫:默认 false 保持现有冷却行为不变。
	defaultSvc := NewUpstreamRateService(func() time.Time { return now }, time.Minute)
	defaultDec, err := defaultSvc.HandleUpstreamError(context.Background(), 101, http.StatusTooManyRequests, headers, nil)
	if err != nil {
		t.Fatalf("default HandleUpstreamError: %v", err)
	}
	if !defaultDec.CooldownUntil.Equal(now.Add(time.Hour)) {
		t.Fatalf("default CooldownUntil=%s want %s", defaultDec.CooldownUntil, now.Add(time.Hour))
	}
}

type fakeSessionWindowStore struct {
	calls  int
	update SessionWindowUpdate
}

func (f *fakeSessionWindowStore) UpdateProviderAccountSessionWindows(_ context.Context, update SessionWindowUpdate) error {
	f.calls++
	f.update = update
	return nil
}

func TestSessionWindowParseBothWindowsAndUtilization(t *testing.T) {
	// 变异：7d 前缀若误写成 5h，下面 7d 的四个精确断言会同时转红。
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	reset5h := now.Add(time.Hour)
	reset7d := now.Add(6 * 24 * time.Hour)
	store := &fakeSessionWindowStore{}
	svc := NewUpstreamRateServiceWithSessionWindowStore(func() time.Time { return now }, time.Minute, store)
	headers := http.Header{}
	headers.Set(sessionWindow5hPrefix+"-status", "allowed")
	headers.Set(sessionWindow5hPrefix+"-reset", strconv.FormatInt(reset5h.Unix(), 10))
	headers.Set(sessionWindow5hPrefix+"-utilization", "37.5")
	headers.Set(sessionWindow7dPrefix+"-status", "allowed")
	headers.Set(sessionWindow7dPrefix+"-reset", strconv.FormatInt(reset7d.Unix(), 10))
	headers.Set(sessionWindow7dPrefix+"-utilization", "37.5")

	if err := svc.UpdateSessionWindow(context.Background(), 101, headers); err != nil {
		t.Fatalf("UpdateSessionWindow: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("writes=%d, want 1", store.calls)
	}
	if store.update.ProviderAccountID != 101 {
		t.Fatalf("provider_account_id=%d, want 101", store.update.ProviderAccountID)
	}
	if store.update.Window5hEnd == nil || !store.update.Window5hEnd.Equal(reset5h) {
		t.Fatalf("window_5h_end=%v, want %s", store.update.Window5hEnd, reset5h)
	}
	if store.update.Window5hStart == nil || !store.update.Window5hStart.Equal(reset5h.Add(-sessionWindow5hDuration)) {
		t.Fatalf("window_5h_start=%v, want %s", store.update.Window5hStart, reset5h.Add(-sessionWindow5hDuration))
	}
	if store.update.Window5hStatus == nil || *store.update.Window5hStatus != "allowed" {
		t.Fatalf("window_5h_status=%v, want allowed", store.update.Window5hStatus)
	}
	if store.update.Window5hUtilization == nil || *store.update.Window5hUtilization != 37.5 {
		t.Fatalf("window_5h_utilization=%v, want 37.5", store.update.Window5hUtilization)
	}
	if store.update.Window7dEnd == nil || !store.update.Window7dEnd.Equal(reset7d) {
		t.Fatalf("window_7d_end=%v, want %s", store.update.Window7dEnd, reset7d)
	}
	if store.update.Window7dStart == nil || !store.update.Window7dStart.Equal(reset7d.Add(-sessionWindow7dDuration)) {
		t.Fatalf("window_7d_start=%v, want %s", store.update.Window7dStart, reset7d.Add(-sessionWindow7dDuration))
	}
	if store.update.Window7dStatus == nil || *store.update.Window7dStatus != "allowed" {
		t.Fatalf("window_7d_status=%v, want allowed", store.update.Window7dStatus)
	}
	if store.update.Window7dUtilization == nil || *store.update.Window7dUtilization != 37.5 {
		t.Fatalf("window_7d_utilization=%v, want 37.5", store.update.Window7dUtilization)
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

func TestSessionWindowInvalidUtilizationIsNotPersisted(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	resetAt := strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
	for _, invalid := range []string{"100.01", "-0.01", "不是数字"} {
		t.Run(invalid, func(t *testing.T) {
			store := &fakeSessionWindowStore{}
			svc := NewUpstreamRateServiceWithSessionWindowStore(func() time.Time { return now }, time.Minute, store)
			headers := http.Header{}
			headers.Set(sessionWindow5hPrefix+"-status", "allowed")
			headers.Set(sessionWindow5hPrefix+"-reset", resetAt)
			headers.Set(sessionWindow5hPrefix+"-utilization", invalid)
			headers.Set(sessionWindow7dPrefix+"-status", "allowed")
			headers.Set(sessionWindow7dPrefix+"-reset", resetAt)
			headers.Set(sessionWindow7dPrefix+"-utilization", invalid)

			if err := svc.UpdateSessionWindow(context.Background(), 101, headers); err != nil {
				t.Fatalf("UpdateSessionWindow: %v", err)
			}
			if store.calls != 1 {
				t.Fatalf("writes=%d, want 1", store.calls)
			}
			if store.update.Window5hUtilization != nil || store.update.Window7dUtilization != nil {
				t.Fatalf("非法利用率不得被归零写入：5h=%v 7d=%v", store.update.Window5hUtilization, store.update.Window7dUtilization)
			}
		})
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
