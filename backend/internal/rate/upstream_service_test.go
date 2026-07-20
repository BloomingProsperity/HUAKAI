package rate

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"
)

type fakeStatusCooldownSource struct {
	values map[int]time.Duration
	err    error
	calls  []int
}

func (s *fakeStatusCooldownSource) CooldownForStatus(_ context.Context, status int) (time.Duration, error) {
	s.calls = append(s.calls, status)
	if s.err != nil {
		return 0, s.err
	}
	return s.values[status], nil
}

func TestUpstreamRateServiceUsesStatusSpecificFallbackCooldown(t *testing.T) {
	// 变异：删除 CooldownSource 读取后，429/529 都会退回构造时的 1 分钟，本测试两个精确截止时间同时变红。
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	source := &fakeStatusCooldownSource{values: map[int]time.Duration{
		http.StatusTooManyRequests: 17 * time.Second,
		529:                        43 * time.Second,
	}}
	svc := NewUpstreamRateService(func() time.Time { return now }, time.Minute, WithCooldownSource(source))

	for _, tc := range []struct {
		status int
		want   time.Duration
	}{
		{status: http.StatusTooManyRequests, want: 17 * time.Second},
		{status: 529, want: 43 * time.Second},
	} {
		dec, err := svc.HandleUpstreamError(context.Background(), 101, tc.status, nil, nil)
		if err != nil {
			t.Fatalf("status=%d HandleUpstreamError: %v", tc.status, err)
		}
		if !dec.CooldownUntil.Equal(now.Add(tc.want)) {
			t.Fatalf("status=%d cooldown_until=%s, want %s", tc.status, dec.CooldownUntil, now.Add(tc.want))
		}
	}
	if len(source.calls) != 2 || source.calls[0] != http.StatusTooManyRequests || source.calls[1] != 529 {
		t.Fatalf("设置源调用=%v, want [429 529]", source.calls)
	}
}

func TestUpstreamRateServiceCooldownSourceFailureKeepsRealityDefault(t *testing.T) {
	// 变异：设置读取失败时把冷却清零，会使精确的 5 分钟默认守卫变红。
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	source := &fakeStatusCooldownSource{err: errors.New("settings unavailable")}
	svc := NewUpstreamRateService(func() time.Time { return now }, defaultUpstreamCooldown, WithCooldownSource(source))
	dec, err := svc.HandleUpstreamError(context.Background(), 101, http.StatusTooManyRequests, nil, nil)
	if err != nil {
		t.Fatalf("HandleUpstreamError: %v", err)
	}
	if !dec.CooldownUntil.Equal(now.Add(defaultUpstreamCooldown)) {
		t.Fatalf("cooldown_until=%s, want reality default %s", dec.CooldownUntil, now.Add(defaultUpstreamCooldown))
	}
}

func TestUpstreamRateService429RetryAfterDeltaSetsCooldown(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	source := &fakeStatusCooldownSource{values: map[int]time.Duration{http.StatusTooManyRequests: 17 * time.Second}}
	svc := NewUpstreamRateService(func() time.Time { return now }, time.Minute, WithCooldownSource(source))
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
	if len(source.calls) != 0 {
		t.Fatalf("Retry-After 已给出时不应读取默认冷却源，calls=%v", source.calls)
	}
}

func TestUpstreamRateService529RetryAfterHTTPDateSetsOverloadCooldown(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	until := now.Add(2 * time.Minute).Truncate(time.Second)
	source := &fakeStatusCooldownSource{values: map[int]time.Duration{529: 43 * time.Second}}
	svc := NewUpstreamRateService(func() time.Time { return now }, time.Minute, WithCooldownSource(source))
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
	if len(source.calls) != 0 {
		t.Fatalf("Retry-After 已给出时不应读取默认过载冷却源，calls=%v", source.calls)
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

func TestSessionWindowNormalizesCodexWindowsByReportedDuration(t *testing.T) {
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	store := &fakeSessionWindowStore{}
	svc := NewUpstreamRateServiceWithSessionWindowStore(func() time.Time { return now }, time.Minute, store)
	headers := http.Header{}
	// 上游的 primary/secondary 不是固定的 7d/5h 名称，必须看实际窗口时长。
	headers.Set("x-codex-primary-used-percent", "42")
	headers.Set("x-codex-primary-reset-after-seconds", "7200")
	headers.Set("x-codex-primary-window-minutes", "300")
	headers.Set("x-codex-secondary-used-percent", "88")
	headers.Set("x-codex-secondary-reset-after-seconds", "500000")
	headers.Set("x-codex-secondary-window-minutes", "10080")

	if err := svc.UpdateSessionWindow(context.Background(), 202, headers); err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 || store.update.ObservationOutcome != QuotaSnapshotOutcomeSuccess {
		t.Fatalf("写入=%d outcome=%q", store.calls, store.update.ObservationOutcome)
	}
	if store.update.Window5hUtilization == nil || *store.update.Window5hUtilization != 42 ||
		store.update.Window5hEnd == nil || !store.update.Window5hEnd.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("5h 窗口归一错误：%+v", store.update)
	}
	if store.update.Window7dUtilization == nil || *store.update.Window7dUtilization != 88 ||
		store.update.Window7dEnd == nil || !store.update.Window7dEnd.Equal(now.Add(500000*time.Second)) {
		t.Fatalf("7d 窗口归一错误：%+v", store.update)
	}
}

func TestSessionWindowCodexMissingDurationKeepsPrimaryAsShortWindow(t *testing.T) {
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	store := &fakeSessionWindowStore{}
	svc := NewUpstreamRateServiceWithSessionWindowStore(func() time.Time { return now }, time.Minute, store)
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "21")
	headers.Set("x-codex-primary-reset-after-seconds", "3600")
	headers.Set("x-codex-secondary-used-percent", "73")
	headers.Set("x-codex-secondary-reset-after-seconds", "500000")

	if err := svc.UpdateSessionWindow(context.Background(), 204, headers); err != nil {
		t.Fatal(err)
	}
	if store.update.Window5hUtilization == nil || *store.update.Window5hUtilization != 21 ||
		store.update.Window7dUtilization == nil || *store.update.Window7dUtilization != 73 {
		t.Fatalf("缺少窗口时长时不得颠倒主次窗口：%+v", store.update)
	}
}

func TestSessionWindowCodexHeadersRejectInvalidValues(t *testing.T) {
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	store := &fakeSessionWindowStore{}
	svc := NewUpstreamRateServiceWithSessionWindowStore(func() time.Time { return now }, time.Minute, store)
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100.01")
	headers.Set("x-codex-primary-reset-after-seconds", "999999999")
	headers.Set("x-codex-primary-window-minutes", "10080")

	if err := svc.UpdateSessionWindow(context.Background(), 203, headers); err != nil {
		t.Fatal(err)
	}
	if store.calls != 0 {
		t.Fatalf("非法 Codex 窗口不得写库：calls=%d update=%+v", store.calls, store.update)
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
