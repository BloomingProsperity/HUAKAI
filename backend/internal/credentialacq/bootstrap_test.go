package credentialacq

import (
	"testing"
	"time"
)

// 守护的缺陷：long_lived_requested 被存了下来，却从未改变 flow 的过期窗口。
// 变异：两个分支都返回 DefaultFlowTTL；下面的 long 分支必然失败，因为 10m 与 7d
// 被有意设得相距很远。
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
