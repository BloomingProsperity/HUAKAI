package credentialacq

import (
	"testing"
	"time"
)

// Defect guarded: long_lived_requested was stored but never changed the flow
// expiry window. Mutation: return DefaultFlowTTL for both branches; the long
// branch below must fail because 10m and 7d are intentionally far apart.
func TestSelectBootstrapTTLDefaults(t *testing.T) {
	short := SelectBootstrapTTL(false)
	long := SelectBootstrapTTL(true)
	if short != DefaultFlowTTL {
		t.Fatalf("short bootstrap ttl=%s want %s", short, DefaultFlowTTL)
	}
	if long != 7*24*time.Hour {
		t.Fatalf("long bootstrap ttl=%s want 168h", long)
	}
	if short == long {
		t.Fatalf("fixture is not discriminating: short and long TTL both=%s", short)
	}
}

func TestSelectBootstrapTTLWithDurationsUsesConfiguredValues(t *testing.T) {
	shortTTL := 30 * time.Minute
	longTTL := 48 * time.Hour
	if got := SelectBootstrapTTLWithDurations(false, shortTTL, longTTL); got != shortTTL {
		t.Fatalf("configured short ttl=%s want %s", got, shortTTL)
	}
	if got := SelectBootstrapTTLWithDurations(true, shortTTL, longTTL); got != longTTL {
		t.Fatalf("configured long ttl=%s want %s", got, longTTL)
	}
}
