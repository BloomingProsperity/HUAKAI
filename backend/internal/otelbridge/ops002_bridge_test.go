package otelbridge

import (
	"testing"
)

// TestOPS002_BridgeCountersContainProviderHealthMetrics verifies that both
// huakai_provider_error_total and huakai_provider_degraded_total are present
// in bridgeCounters() and that their read functions return values from the
// "provider_health" expvar map.
//
// MUTATION: removing either entry from bridgeCounters() → found[name]=false → RED.
func TestOPS002_BridgeCountersContainProviderHealthMetrics(t *testing.T) {
	want := map[string]bool{
		"huakai_provider_error_total":    false,
		"huakai_provider_degraded_total": false,
	}
	for _, bc := range bridgeCounters() {
		if _, ok := want[bc.name]; ok {
			want[bc.name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("OPS-002: bridgeCounters() missing %s", name)
		}
	}
}

// TestOPS002_BridgeReadsProviderHealthExpvar verifies that the bridge read
// function for huakai_provider_error_total reads from the expvar map.
//
// MUTATION: wiring the read to a different map or hard-coding 0 → value stays 0
// even after the channelhealth counter is incremented → RED.
func TestOPS002_BridgeReadsProviderHealthExpvar(t *testing.T) {
	// Set the expvar map entry to a known sentinel value.
	setExpvarMapInt(t, "provider_health", "error_total", 42)

	for _, bc := range bridgeCounters() {
		if bc.name == "huakai_provider_error_total" {
			if got := bc.read(); got != 42 {
				t.Fatalf("OPS-002: bridge read() for huakai_provider_error_total=%d; want 42 (expvar not wired)", got)
			}
			return
		}
	}
	t.Fatal("OPS-002: huakai_provider_error_total not found in bridgeCounters()")
}
