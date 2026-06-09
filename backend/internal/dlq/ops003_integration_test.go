//go:build integration_pg

package dlq

import (
	"context"
	"expvar"
	"testing"
	"time"
)

// TestOPS003_CountPendingByLaneAndDepthGauge is the OPS-003 discriminating test.
// It seeds N pending rows across lanes, calls CountPendingByLane, and asserts:
//  1. Per-lane counts match the seeded values exactly.
//  2. UpdateDLQDepthGauge sets the dlq_depth expvar map entries to the correct values.
//
// MUTATION: if the WHERE status='pending' filter is removed from CountPendingByLane,
// delivered/inflight rows are included and counts are wrong → RED.
func TestOPS003_CountPendingByLaneAndDepthGauge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openDLQPool(t, ctx)
	tenantID := seedDLQTenant(t, ctx, pool)
	store := NewStore(pool)

	// Seed: 2 HIGH pending, 3 MED pending, 1 LOW pending.
	seedPending := func(lane Lane, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			_, err := store.Enqueue(ctx, Event{
				TenantID:       tenantID,
				EventKind:      EventKindUsageRecord,
				Lane:           lane,
				Payload:        []byte(`{"x":1}`),
				FailureReason:  "test_seed",
				IdempotencyKey: string(lane) + ":ops003:" + string(rune('a'+i)),
				SourceTable:    "usage_records",
				SourceID:       int64(i + 1),
				NextRetryAt:    time.Now().UTC().Add(-time.Minute),
			})
			if err != nil {
				t.Fatalf("enqueue %s[%d]: %v", lane, i, err)
			}
		}
	}
	seedPending(LaneHigh, 2)
	seedPending(LaneMed, 3)
	seedPending(LaneLow, 1)

	// Seed one delivered row in HIGH to verify it is NOT counted.
	idDel, err := store.Enqueue(ctx, Event{
		TenantID:       tenantID,
		EventKind:      EventKindUsageRecord,
		Lane:           LaneHigh,
		Payload:        []byte(`{"x":2}`),
		FailureReason:  "test_delivered_seed",
		IdempotencyKey: "HIGH:ops003:delivered",
		SourceTable:    "usage_records",
		SourceID:       99,
		NextRetryAt:    time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("enqueue delivered seed: %v", err)
	}
	recDel, err := store.ClaimByID(ctx, idDel, "ops003-deliver-worker", time.Minute)
	if err != nil || recDel == nil || recDel.ID != idDel {
		t.Fatalf("claim for delivery: rec=%v err=%v", recDel, err)
	}
	if err := store.MarkDelivered(ctx, *recDel); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}

	// --- Assert CountPendingByLane ---
	counts, err := store.CountPendingByLane(ctx)
	if err != nil {
		t.Fatalf("CountPendingByLane: %v", err)
	}
	byLane := map[Lane]int64{}
	for _, lc := range counts {
		byLane[lc.Lane] += lc.Count
	}
	wantByLane := map[Lane]int64{LaneHigh: 2, LaneMed: 3, LaneLow: 1}
	for lane, want := range wantByLane {
		if got := byLane[lane]; got != want {
			t.Errorf("OPS-003: CountPendingByLane lane=%s got=%d want=%d (delivered row must not be counted)", lane, got, want)
		}
	}

	// --- Assert UpdateDLQDepthGauge ---
	if err := store.UpdateDLQDepthGauge(ctx); err != nil {
		t.Fatalf("UpdateDLQDepthGauge: %v", err)
	}
	m, ok := expvar.Get("dlq_depth").(*expvar.Map)
	if !ok || m == nil {
		t.Fatal("OPS-003: dlq_depth expvar map not registered after UpdateDLQDepthGauge")
	}
	gaugeChecks := map[string]int64{
		"depth_HIGH": 2,
		"depth_MED":  3,
		"depth_LOW":  1,
	}
	for key, want := range gaugeChecks {
		iv, ok := m.Get(key).(*expvar.Int)
		if !ok || iv == nil {
			t.Errorf("OPS-003: dlq_depth.%s not found in expvar map", key)
			continue
		}
		if got := iv.Value(); got != want {
			t.Errorf("OPS-003: dlq_depth.%s=%d want %d", key, got, want)
		}
	}
}
