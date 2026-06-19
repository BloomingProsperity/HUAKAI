package otelbridge

import (
	"context"
	"testing"
)

// TestRuntimeGaugesBridgedToAlertSnapshot verifies the live process runtime-resource
// gauges (heap-allocated bytes, goroutine count, uptime seconds) are exposed through the
// ExpvarMetricSource snapshot — the same metric map the alert engine evaluates rules
// against — so an operator can threshold the gateway's own footprint via the existing
// alert-rule CRUD (F-GW-003 Phase 2).
//
// MUTATION: drop any of the three runtime entries from bridgeCounters() → its key is absent
// from the snapshot → the assertion for that key goes RED. The live invariants (heap > 0,
// goroutines >= 1) cannot be satisfied by the map's zero value, and uptime is guarded by an
// explicit presence check (its >= 0 bound alone would pass the zero value).
func TestRuntimeGaugesBridgedToAlertSnapshot(t *testing.T) {
	snap, err := ExpvarMetricSource{}.Snapshot(context.Background(), 0)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	heap, ok := snap["huakai_runtime_heap_alloc_bytes"]
	if !ok || heap <= 0 {
		t.Errorf("huakai_runtime_heap_alloc_bytes present=%v value=%v; want present and >0 (live heap)", ok, heap)
	}

	goroutines, ok := snap["huakai_runtime_goroutines"]
	if !ok || goroutines < 1 {
		t.Errorf("huakai_runtime_goroutines present=%v value=%v; want present and >=1 (the test goroutine alone)", ok, goroutines)
	}

	if _, ok := snap["huakai_runtime_uptime_seconds"]; !ok {
		t.Error("huakai_runtime_uptime_seconds absent from snapshot; want present (uptime gauge missing from bridge)")
	}
}
