package credentialstore

import (
	"testing"
	"time"
)

// TestEffectiveRefreshLead_NullPreservesGlobal proves that a nil per-account
// override produces exactly the same duration as the global RefreshWindow
// (DEFAULT SAFETY requirement).
func TestEffectiveRefreshLead_NullPreservesGlobal(t *testing.T) {
	got := effectiveRefreshLead(nil, RefreshWindow)
	if got != RefreshWindow {
		t.Fatalf("effectiveRefreshLead(nil, RefreshWindow) = %v, want %v", got, RefreshWindow)
	}
}

// TestEffectiveRefreshLead_ZeroPreservesGlobal proves that a zero value
// (non-nil but <= 0) also falls back to the global window.
func TestEffectiveRefreshLead_ZeroPreservesGlobal(t *testing.T) {
	zero := int32(0)
	got := effectiveRefreshLead(&zero, RefreshWindow)
	if got != RefreshWindow {
		t.Fatalf("effectiveRefreshLead(&0, RefreshWindow) = %v, want %v", got, RefreshWindow)
	}
}

// TestEffectiveRefreshLead_PerAccountOverride proves that a positive
// per-account value overrides the global window.
func TestEffectiveRefreshLead_PerAccountOverride(t *testing.T) {
	n := int32(300) // 5 minutes in seconds
	got := effectiveRefreshLead(&n, RefreshWindow)
	want := 300 * time.Second
	if got != want {
		t.Fatalf("effectiveRefreshLead(&300, RefreshWindow) = %v, want %v", got, want)
	}
}

// TestEffectiveRefreshLead_NullRefreshBeforeAt proves that NULL ->
// refresh_before_at == accessExpiresAt - RefreshWindow (identical to today).
func TestEffectiveRefreshLead_NullRefreshBeforeAt(t *testing.T) {
	expiry := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	lead := effectiveRefreshLead(nil, RefreshWindow)
	got := expiry.Add(-lead)
	want := expiry.Add(-RefreshWindow)
	if !got.Equal(want) {
		t.Fatalf("NULL lead: refreshBeforeAt = %v, want %v", got, want)
	}
}

// TestEffectiveRefreshLead_PerAccountRefreshBeforeAt proves that a per-account
// N seconds -> refresh_before_at = expiry - N*seconds.
func TestEffectiveRefreshLead_PerAccountRefreshBeforeAt(t *testing.T) {
	expiry := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	n := int32(600) // 10 minutes
	lead := effectiveRefreshLead(&n, RefreshWindow)
	got := expiry.Add(-lead)
	want := expiry.Add(-600 * time.Second)
	if !got.Equal(want) {
		t.Fatalf("per-account 600s lead: refreshBeforeAt = %v, want %v", got, want)
	}
	// also verify it differs from the global default
	globalResult := expiry.Add(-RefreshWindow)
	if got.Equal(globalResult) {
		t.Fatalf("per-account result should differ from global result when N != RefreshWindow")
	}
}
