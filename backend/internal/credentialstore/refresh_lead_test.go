package credentialstore

import (
	"testing"
	"time"
)

// TestEffectiveRefreshLead_NullPreservesGlobal 证明 nil 的 per-account 覆盖值
// 产出的时长与全局 RefreshWindow 完全一致(DEFAULT SAFETY 要求)。
func TestEffectiveRefreshLead_NullPreservesGlobal(t *testing.T) {
	got := effectiveRefreshLead(nil, RefreshWindow)
	if got != RefreshWindow {
		t.Fatalf("effectiveRefreshLead(nil, RefreshWindow) = %v, want %v", got, RefreshWindow)
	}
}

// TestEffectiveRefreshLead_ZeroPreservesGlobal 证明零值(非 nil 但 <= 0)
// 同样回退到全局 window。
func TestEffectiveRefreshLead_ZeroPreservesGlobal(t *testing.T) {
	zero := int32(0)
	got := effectiveRefreshLead(&zero, RefreshWindow)
	if got != RefreshWindow {
		t.Fatalf("effectiveRefreshLead(&0, RefreshWindow) = %v, want %v", got, RefreshWindow)
	}
}

// TestEffectiveRefreshLead_PerAccountOverride 证明正的 per-account 值
// 会覆盖全局 window。
func TestEffectiveRefreshLead_PerAccountOverride(t *testing.T) {
	n := int32(300) // 5 分钟,以秒计
	got := effectiveRefreshLead(&n, RefreshWindow)
	want := 300 * time.Second
	if got != want {
		t.Fatalf("effectiveRefreshLead(&300, RefreshWindow) = %v, want %v", got, want)
	}
}

// TestEffectiveRefreshLead_NullRefreshBeforeAt 证明 NULL ->
// refresh_before_at == accessExpiresAt - RefreshWindow(与当前行为一致)。
func TestEffectiveRefreshLead_NullRefreshBeforeAt(t *testing.T) {
	expiry := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	lead := effectiveRefreshLead(nil, RefreshWindow)
	got := expiry.Add(-lead)
	want := expiry.Add(-RefreshWindow)
	if !got.Equal(want) {
		t.Fatalf("NULL lead: refreshBeforeAt = %v, want %v", got, want)
	}
}

// TestEffectiveRefreshLead_PerAccountRefreshBeforeAt 证明 per-account 的
// N 秒 -> refresh_before_at = expiry - N*秒。
func TestEffectiveRefreshLead_PerAccountRefreshBeforeAt(t *testing.T) {
	expiry := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	n := int32(600) // 10 分钟
	lead := effectiveRefreshLead(&n, RefreshWindow)
	got := expiry.Add(-lead)
	want := expiry.Add(-600 * time.Second)
	if !got.Equal(want) {
		t.Fatalf("per-account 600s lead: refreshBeforeAt = %v, want %v", got, want)
	}
	// 同时验证它与全局默认值不同
	globalResult := expiry.Add(-RefreshWindow)
	if got.Equal(globalResult) {
		t.Fatalf("per-account result should differ from global result when N != RefreshWindow")
	}
}
