package dispatcher

import (
	"testing"
	"time"
)

// An unconfigured manager (leaseDuration == 0) must resolve to the package
// DefaultLeaseDuration — this is the zero-config → zero-behavior-change path
// the Acquire insert relies on. Discriminating: if effectiveLeaseDuration's
// fallback `return DefaultLeaseDuration` were changed to `return m.leaseDuration`
// it would return 0 here and this fails (a 0-duration lease would expire
// immediately and be swept on the next tick).
func TestDBSlotManager_EffectiveLeaseDefault(t *testing.T) {
	m := NewDBSlotManager(nil) // nil pool ok: effectiveLeaseDuration is pure
	if got := m.effectiveLeaseDuration(); got != DefaultLeaseDuration {
		t.Fatalf("effectiveLeaseDuration()=%s want DefaultLeaseDuration=%s", got, DefaultLeaseDuration)
	}
}

// A configured override must be stored and returned verbatim. Discriminating:
// 120s differs from the 90s default, so if WithLeaseDuration dropped the value
// or effectiveLeaseDuration ignored the override branch, this fails.
func TestDBSlotManager_WithLeaseDurationOverride(t *testing.T) {
	const want = 120 * time.Second
	m := NewDBSlotManager(nil).WithLeaseDuration(want)
	if m.leaseDuration != want {
		t.Fatalf("stored leaseDuration=%s want %s (setter dropped value?)", m.leaseDuration, want)
	}
	if got := m.effectiveLeaseDuration(); got != want {
		t.Fatalf("effectiveLeaseDuration()=%s want %s (override branch missing?)", got, want)
	}
	if got := m.effectiveLeaseDuration(); got == DefaultLeaseDuration {
		t.Fatalf("override collapsed to DefaultLeaseDuration=%s", DefaultLeaseDuration)
	}
}

// Non-positive overrides must be ignored so the safe default stands — guards
// against a caller passing 0 / negative and silently killing the grace window.
// Discriminating on the stored field: if WithLeaseDuration's `if d > 0` guard
// were removed, leaseDuration would hold -5s / 0 here and the field assertion
// fails. The effective-value assertion additionally pins the DefaultLeaseDuration
// fallback for the negative case.
func TestDBSlotManager_WithLeaseDurationIgnoresNonPositive(t *testing.T) {
	for _, bad := range []time.Duration{0, -5 * time.Second} {
		m := NewDBSlotManager(nil).WithLeaseDuration(bad)
		if m.leaseDuration != 0 {
			t.Errorf("WithLeaseDuration(%s): stored leaseDuration=%s want 0 (guard removed?)", bad, m.leaseDuration)
		}
		if got := m.effectiveLeaseDuration(); got != DefaultLeaseDuration {
			t.Errorf("WithLeaseDuration(%s): effective=%s want DefaultLeaseDuration=%s", bad, got, DefaultLeaseDuration)
		}
	}
}
