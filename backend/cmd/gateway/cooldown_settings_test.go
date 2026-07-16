package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	ratelimit "github.com/BloomingProsperity/HUAKAI/internal/rate"
)

func TestPlatformCooldownSettingsDriveNew429And529Events(t *testing.T) {
	// 变异：把服务重新接回构造时死默认后，17 秒与 43 秒两个截止时间都会退回 5 分钟并变红。
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	settings := &fakeRuntimeSettings{values: map[platformsettings.SettingKey]platformsettings.StoredSetting{
		platformsettings.KeyCooldown429Seconds: {Key: platformsettings.KeyCooldown429Seconds, Value: "17", Source: platformsettings.SourceDB},
		platformsettings.KeyCooldown529Seconds: {Key: platformsettings.KeyCooldown529Seconds, Value: "43", Source: platformsettings.SourceDB},
	}}
	source := platformCooldownSource{settings: settings}
	svc := ratelimit.NewUpstreamRateService(func() time.Time { return now }, 5*time.Minute, ratelimit.WithCooldownSource(source))

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
}

func TestPlatformCooldownSettingChangeOnlyAffectsNextEvent(t *testing.T) {
	// 变异：若冷却值只在构造时读取，第二次事件仍会得到 17 秒而不是更新后的 29 秒。
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	settings := &fakeRuntimeSettings{values: map[platformsettings.SettingKey]platformsettings.StoredSetting{
		platformsettings.KeyCooldown429Seconds: {Key: platformsettings.KeyCooldown429Seconds, Value: "17", Source: platformsettings.SourceDB},
	}}
	svc := ratelimit.NewUpstreamRateService(func() time.Time { return now }, 5*time.Minute,
		ratelimit.WithCooldownSource(platformCooldownSource{settings: settings}))
	first, err := svc.HandleUpstreamError(context.Background(), 101, http.StatusTooManyRequests, nil, nil)
	if err != nil {
		t.Fatalf("first HandleUpstreamError: %v", err)
	}
	settings.values[platformsettings.KeyCooldown429Seconds] = platformsettings.StoredSetting{
		Key: platformsettings.KeyCooldown429Seconds, Value: "29", Source: platformsettings.SourceDB,
	}
	second, err := svc.HandleUpstreamError(context.Background(), 101, http.StatusTooManyRequests, nil, nil)
	if err != nil {
		t.Fatalf("second HandleUpstreamError: %v", err)
	}
	if !first.CooldownUntil.Equal(now.Add(17*time.Second)) || !second.CooldownUntil.Equal(now.Add(29*time.Second)) {
		t.Fatalf("first=%s second=%s, want now+17s / now+29s", first.CooldownUntil, second.CooldownUntil)
	}
}

func TestPlatformCooldownDefaultsKeepPreWiringFiveMinutes(t *testing.T) {
	// 防翻转守卫：运营未改设置时，429/529 都保持接线前的 5 分钟现实值。
	settings := &fakeRuntimeSettings{values: map[platformsettings.SettingKey]platformsettings.StoredSetting{
		platformsettings.KeyCooldown429Seconds: {Key: platformsettings.KeyCooldown429Seconds, Value: "300", Source: platformsettings.SourceDefault},
		platformsettings.KeyCooldown529Seconds: {Key: platformsettings.KeyCooldown529Seconds, Value: "300", Source: platformsettings.SourceDefault},
	}}
	source := platformCooldownSource{settings: settings}
	for _, status := range []int{http.StatusTooManyRequests, 529} {
		got, err := source.CooldownForStatus(context.Background(), status)
		if err != nil {
			t.Fatalf("status=%d CooldownForStatus: %v", status, err)
		}
		if got != 5*time.Minute {
			t.Fatalf("status=%d cooldown=%v, want pre-wiring 5m", status, got)
		}
	}
}
