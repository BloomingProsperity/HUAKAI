package channelhealth

import (
	"expvar"
	"testing"
	"time"
)

// TestOPS002_ProviderErrorAndDegradedCountersIncrement is the OPS-002 discriminating test.
// It drives real error/degraded state-machine transitions through channelhealth.Service
// and asserts the expvar "provider_health" map's counters increment at the right points.
//
// MUTATION: if either incProviderError() or incProviderDegraded() is removed the
// corresponding counter stays 0 and this test goes RED.
func TestOPS002_ProviderErrorAndDegradedCountersIncrement(t *testing.T) {
	// Force the expvar map to be initialized in this process.
	m := getProviderHealthMetrics()
	if m == nil {
		t.Fatal("getProviderHealthMetrics() returned nil")
	}

	// Read baseline values (tests may run in-process with shared expvar state).
	baselineDegraded := readProviderHealthInt("degraded_total")
	baselineError := readProviderHealthInt("error_total")

	ctx, svc, _, clock := testService()
	key := testKey()

	// --- Degraded transition ---
	// upstream_5xx first hit → StateDegraded (per upstream5xxDecision, first breach
	// transitions active→degraded; second breach transitions degraded→cooling_down).
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalUpstream5xx}); err != nil {
			t.Fatalf("ApplySignal upstream5xx[%d]: %v", i, err)
		}
	}
	gotDegraded := readProviderHealthInt("degraded_total") - baselineDegraded
	if gotDegraded < 1 {
		t.Fatalf("OPS-002: degraded_total did not increment after upstream_5xx degraded transition; delta=%d", gotDegraded)
	}

	// --- Error (cooling_down) transition ---
	// Drive more upstream_5xx signals while already degraded → cooling_down.
	baselineError2 := readProviderHealthInt("error_total")
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalUpstream5xx}); err != nil {
			t.Fatalf("ApplySignal upstream5xx2[%d]: %v", i, err)
		}
	}
	gotError := readProviderHealthInt("error_total") - baselineError2
	if gotError < 1 {
		t.Fatalf("OPS-002: error_total did not increment after cooling_down transition; delta=%d", gotError)
	}

	_ = baselineError // suppress unused warning
}

// TestOPS002_ProviderHealthExpvarReadable verifies the expvar map exists and both
// keys are readable by name — the same read path used by otelbridge.bridgeCounters().
//
// MUTATION: removing getProviderHealthMetrics() init → map nil → read returns 0 →
// bridgeCounters() bridge emits 0 even after real transitions.
func TestOPS002_ProviderHealthExpvarReadable(t *testing.T) {
	// Ensure map is initialized.
	_ = getProviderHealthMetrics()

	for _, key := range []string{"error_total", "degraded_total"} {
		m, ok := expvar.Get("provider_health").(*expvar.Map)
		if !ok || m == nil {
			t.Fatalf("expvar 'provider_health' map not found after init; key=%s", key)
		}
		if m.Get(key) == nil {
			t.Fatalf("expvar 'provider_health'.%s key not registered", key)
		}
	}
}

func readProviderHealthInt(key string) int64 {
	m, ok := expvar.Get("provider_health").(*expvar.Map)
	if !ok || m == nil {
		return 0
	}
	v, ok := m.Get(key).(*expvar.Int)
	if !ok || v == nil {
		return 0
	}
	return v.Value()
}
