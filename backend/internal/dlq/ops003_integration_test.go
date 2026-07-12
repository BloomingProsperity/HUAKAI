//go:build integration_pg

package dlq

import (
	"context"
	"expvar"
	"testing"
	"time"
)

// TestOPS003_CountPendingByLaneAndDepthGauge 是 OPS-003 的区分性测试。
// 它在各个 lane 上播种 N 条 pending 行,调用 CountPendingByLane,并断言:
//  1. 每个 lane 的计数与播种值完全一致。
//  2. UpdateDLQDepthGauge 把 dlq_depth expvar map 的条目设为正确的值。
//
// 变异:如果从 CountPendingByLane 移除 WHERE status='pending' 过滤,
// delivered/inflight 行会被计入,计数就错 → RED。
func TestOPS003_CountPendingByLaneAndDepthGauge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openDLQPool(t, ctx)
	tenantID := seedDLQTenant(t, ctx, pool)
	store := NewStore(pool)
	baselineCounts, err := store.CountPendingByLane(ctx)
	if err != nil {
		t.Fatalf("read baseline lane counts: %v", err)
	}
	baselineByLane := map[Lane]int64{}
	for _, count := range baselineCounts {
		baselineByLane[count.Lane] += count.Count
	}
	baselineUnsettled, err := store.DeliveredUnsettled(ctx)
	if err != nil {
		t.Fatalf("read baseline delivered unsettled: %v", err)
	}

	// 播种:2 条 HIGH pending、3 条 MED pending、1 条 LOW pending。
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

	// 在 HIGH 播种一条 delivered 行,以验证它不会被计入。
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

	// 交付后结算观测只统计未闭合行，并公开最老未结算年龄。
	unsettledID, err := store.Enqueue(ctx, Event{
		TenantID: tenantID, EventKind: EventKindPostDeliverySettlement, Lane: LaneHigh,
		Payload: []byte(`{"source":"test"}`), FailureReason: "settlement_failed",
		IdempotencyKey: "post-delivery:ops003:pending", SourceTable: "billing_ledger_claims", SourceID: 101,
	})
	if err != nil {
		t.Fatalf("enqueue delivered unsettled seed: %v", err)
	}
	settledID, err := store.Enqueue(ctx, Event{
		TenantID: tenantID, EventKind: EventKindPostDeliverySettlement, Lane: LaneHigh,
		Payload: []byte(`{"source":"test"}`), FailureReason: "settlement_failed",
		IdempotencyKey: "post-delivery:ops003:delivered", SourceTable: "billing_ledger_claims", SourceID: 102,
	})
	if err != nil {
		t.Fatalf("enqueue delivered settlement seed: %v", err)
	}
	settledRec, err := store.ClaimByID(ctx, settledID, "ops003-settlement-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim delivered settlement seed: %v", err)
	}
	if err := store.MarkDelivered(ctx, *settledRec); err != nil {
		t.Fatalf("mark settlement delivered: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE usage_record_dlq SET failure_at=now()-interval '2 minutes' WHERE id=$1`, unsettledID); err != nil {
		t.Fatalf("age delivered unsettled seed: %v", err)
	}

	// --- 断言 CountPendingByLane ---
	counts, err := store.CountPendingByLane(ctx)
	if err != nil {
		t.Fatalf("CountPendingByLane: %v", err)
	}
	byLane := map[Lane]int64{}
	for _, lc := range counts {
		byLane[lc.Lane] += lc.Count
	}
	wantByLane := map[Lane]int64{
		LaneHigh: baselineByLane[LaneHigh] + 3,
		LaneMed:  baselineByLane[LaneMed] + 3,
		LaneLow:  baselineByLane[LaneLow] + 1,
	}
	for lane, want := range wantByLane {
		if got := byLane[lane]; got != want {
			t.Errorf("OPS-003: CountPendingByLane lane=%s got=%d want=%d (delivered row must not be counted)", lane, got, want)
		}
	}

	// --- 断言 UpdateDLQDepthGauge ---
	if err := store.UpdateDLQDepthGauge(ctx); err != nil {
		t.Fatalf("UpdateDLQDepthGauge: %v", err)
	}
	m, ok := expvar.Get("dlq_depth").(*expvar.Map)
	if !ok || m == nil {
		t.Fatal("OPS-003: dlq_depth expvar map not registered after UpdateDLQDepthGauge")
	}
	gaugeChecks := map[string]int64{
		"depth_HIGH":                baselineByLane[LaneHigh] + 3,
		"depth_MED":                 baselineByLane[LaneMed] + 3,
		"depth_LOW":                 baselineByLane[LaneLow] + 1,
		"delivered_unsettled_count": baselineUnsettled.Count + 1,
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
	ageGauge, ok := m.Get("delivered_unsettled_age_seconds").(*expvar.Int)
	if !ok || ageGauge == nil || ageGauge.Value() < 119 {
		t.Errorf("OPS-003: delivered_unsettled_age_seconds=%v want >=119", m.Get("delivered_unsettled_age_seconds"))
	}
}

func TestPostDeliverySettlementOperatorReviewIsAutomaticallyReclaimed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openDLQPool(t, ctx)
	tenantID := seedDLQTenant(t, ctx, pool)
	store := NewStore(pool)
	id, err := store.Enqueue(ctx, Event{
		TenantID: tenantID, EventKind: EventKindPostDeliverySettlement, Lane: LaneHigh,
		Payload: []byte(`{"source":"test"}`), FailureReason: "settlement_failed",
		IdempotencyKey: "post-delivery:operator-review", SourceTable: "billing_ledger_claims", SourceID: 201,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE usage_record_dlq SET status='operator_review', next_retry_at=now()-interval '100 years' WHERE id=$1`, id,
	); err != nil {
		t.Fatalf("seed prior terminal state: %v", err)
	}
	rec, err := store.Claim(ctx, LaneHigh, "continuous-retry-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if rec == nil || rec.ID != id || rec.Status != StatusInflight {
		t.Fatalf("claimed=%+v want id=%d inflight", rec, id)
	}
}
