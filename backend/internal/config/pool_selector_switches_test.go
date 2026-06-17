package config

import (
	"errors"
	"testing"
	"time"
)

// HUAKAI_PASR_LOAD_CAP must parse into cfg.LoadCap. Discriminating: 0.5 differs
// from both the 0-unset and the selector's 0.95 default, so dropping the parse
// block leaves LoadCap=0 and this fails.
func TestLoadPoolSelector_LoadCapEnv(t *testing.T) {
	t.Setenv("HUAKAI_PASR_LOAD_CAP", "0.5")
	cfg, err := LoadPoolSelector()
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if cfg.LoadCap != 0.5 {
		t.Fatalf("LoadCap=%v want 0.5 (env not parsed?)", cfg.LoadCap)
	}
}

// Unset -> 0, which the selector turns into its 0.95 default (zero behavior change).
func TestLoadPoolSelector_LoadCapUnsetIsZero(t *testing.T) {
	t.Setenv("HUAKAI_PASR_LOAD_CAP", "")
	cfg, err := LoadPoolSelector()
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if cfg.LoadCap != 0 {
		t.Fatalf("LoadCap=%v want 0 when unset", cfg.LoadCap)
	}
}

// Out-of-range / unparseable load caps must fail-fast at startup with the typed
// sentinel, never silently degrade on the hot path. Guards parse + Validate range.
func TestLoadPoolSelector_LoadCapInvalidRejected(t *testing.T) {
	for _, bad := range []string{"1.5", "-0.1", "abc"} {
		t.Setenv("HUAKAI_PASR_LOAD_CAP", bad)
		_, err := LoadPoolSelector()
		if !errors.Is(err, ErrInvalidLoadCap) {
			t.Errorf("LoadCap=%q: want ErrInvalidLoadCap, got %v", bad, err)
		}
	}
}

// HUAKAI_POOL_STICKY_BINDING_TTL_SECONDS parses seconds into a Duration.
// Discriminating: 30s differs from the 0-unset and the store's 1h default.
func TestLoadPoolSelector_StickyTTLEnv(t *testing.T) {
	t.Setenv("HUAKAI_POOL_STICKY_BINDING_TTL_SECONDS", "30")
	cfg, err := LoadPoolSelector()
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if cfg.StickyTTL != 30*time.Second {
		t.Fatalf("StickyTTL=%s want 30s (env not parsed?)", cfg.StickyTTL)
	}
}

// Negative / unparseable sticky TTL must fail-fast with the typed sentinel.
func TestLoadPoolSelector_StickyTTLInvalidRejected(t *testing.T) {
	for _, bad := range []string{"-1", "abc"} {
		t.Setenv("HUAKAI_POOL_STICKY_BINDING_TTL_SECONDS", bad)
		_, err := LoadPoolSelector()
		if !errors.Is(err, ErrInvalidStickyTTL) {
			t.Errorf("StickyTTL=%q: want ErrInvalidStickyTTL, got %v", bad, err)
		}
	}
}

// HUAKAI_POOL_SLOT_LEASE_DURATION_SECONDS parses seconds into a Duration.
// Discriminating: 120s differs from the 0-unset AND the slot manager's 90s
// default, so dropping the parse block (leaving 0) makes this fail.
func TestLoadPoolSelector_SlotLeaseEnv(t *testing.T) {
	t.Setenv("HUAKAI_POOL_SLOT_LEASE_DURATION_SECONDS", "120")
	cfg, err := LoadPoolSelector()
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if cfg.SlotLeaseDuration != 120*time.Second {
		t.Fatalf("SlotLeaseDuration=%s want 120s (env not parsed?)", cfg.SlotLeaseDuration)
	}
}

// Unset -> 0, which the slot manager turns into its 90s default (zero behavior change).
func TestLoadPoolSelector_SlotLeaseUnsetIsZero(t *testing.T) {
	t.Setenv("HUAKAI_POOL_SLOT_LEASE_DURATION_SECONDS", "")
	cfg, err := LoadPoolSelector()
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if cfg.SlotLeaseDuration != 0 {
		t.Fatalf("SlotLeaseDuration=%s want 0 when unset", cfg.SlotLeaseDuration)
	}
}

// Negative / unparseable slot lease must fail-fast with the typed sentinel.
func TestLoadPoolSelector_SlotLeaseInvalidRejected(t *testing.T) {
	for _, bad := range []string{"-1", "abc"} {
		t.Setenv("HUAKAI_POOL_SLOT_LEASE_DURATION_SECONDS", bad)
		_, err := LoadPoolSelector()
		if !errors.Is(err, ErrInvalidSlotLease) {
			t.Errorf("SlotLease=%q: want ErrInvalidSlotLease, got %v", bad, err)
		}
	}
}

// SAFETY FLOOR — the discriminating test for the orphan-recovery race guard.
// A lease at or below the 30s orphan-sweep cadence must be rejected (a slot
// could be reclaimed mid-request → premature Abort + in_flight corruption);
// one second above must be accepted. The 30s-rejected / 31s-accepted pair is
// what goes red if the floor clause in Validate is removed (then 30 & 10
// parse clean) OR if the floor is made inclusive at >= (then 31 would be
// wrongly rejected). Both boundary directions are asserted so neither mutation
// survives.
func TestLoadPoolSelector_SlotLeaseBelowFloorRejected(t *testing.T) {
	for _, bad := range []string{"30", "10", "1"} {
		t.Setenv("HUAKAI_POOL_SLOT_LEASE_DURATION_SECONDS", bad)
		_, err := LoadPoolSelector()
		if !errors.Is(err, ErrInvalidSlotLease) {
			t.Errorf("SlotLease=%ss (<= 30s floor): want ErrInvalidSlotLease, got %v", bad, err)
		}
	}
	// Strictly above the floor is the operator's valid (advanced) band.
	t.Setenv("HUAKAI_POOL_SLOT_LEASE_DURATION_SECONDS", "31")
	cfg, err := LoadPoolSelector()
	if err != nil {
		t.Fatalf("SlotLease=31s (> 30s floor): want accepted, got %v", err)
	}
	if cfg.SlotLeaseDuration != 31*time.Second {
		t.Fatalf("SlotLeaseDuration=%s want 31s", cfg.SlotLeaseDuration)
	}
}
