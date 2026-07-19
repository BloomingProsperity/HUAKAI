//go:build integration_pg

package dlq

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestEphemeralSignalDiscardHandlerAcksRecord 守卫时效性信号的显式丢弃归宿:注册
// NewEphemeralSignalDiscardHandler 后,account_health / metrics 记录被处理为 delivered
// (确认丢弃),不再走 ErrNoHandler 隔离。变异:去掉注册(回到无 handler 状态)→ 记录
// 被 quarantined,「应 delivered」断言 RED——正是"两 kind 永久隔离堆积"的守卫。
func TestEphemeralSignalDiscardHandlerAcksRecord(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openDLQPool(t, ctx)
	tenantID := seedDLQTenant(t, ctx, pool)
	store := NewStore(pool)

	cases := []struct {
		kind EventKind
		lane Lane
	}{
		{EventKindAccountHealth, LaneMed},
		{EventKindMetrics, LaneLow},
	}
	for _, tc := range cases {
		if _, err := store.Enqueue(ctx, Event{
			TenantID:       tenantID,
			EventKind:      tc.kind,
			Lane:           tc.lane,
			Payload:        []byte(`{"event_id":"evt-discard"}`),
			FailureReason:  "handler_failure_seed",
			IdempotencyKey: fmt.Sprintf("discard:%s", tc.kind),
			SourceTable:    "async_processor_events",
			SourceID:       1,
			NextRetryAt:    time.Now().UTC().Add(-time.Minute),
		}); err != nil {
			t.Fatalf("enqueue %s: %v", tc.kind, err)
		}
	}

	service := NewService(store, WithPolicy(RetryPolicy{
		BaseBackoff: time.Second, CapBackoff: time.Second, MaxAttempts: 3, DLQAfter: time.Hour,
	}))
	// 与生产 buildDLQRuntime 相同的注册。
	service.Register(EventKindAccountHealth, NewEphemeralSignalDiscardHandler(EventKindAccountHealth))
	service.Register(EventKindMetrics, NewEphemeralSignalDiscardHandler(EventKindMetrics))
	worker := NewWorker(service, WorkerConfig{HighWorkers: 1, MediumWorkers: 1, LowWorkers: 1, LeaseTTL: time.Second})

	for _, tc := range cases {
		processed, err := worker.RunOnce(ctx, tc.lane, "discard-test-worker")
		if err != nil {
			t.Fatalf("RunOnce(%s): %v", tc.lane, err)
		}
		if !processed {
			t.Fatalf("RunOnce(%s) 未领取事件", tc.lane)
		}
	}

	rows, err := pool.Query(ctx, `SELECT event_kind, status FROM usage_record_dlq WHERE tenant_id=$1`, tenantID)
	if err != nil {
		t.Fatalf("查状态: %v", err)
	}
	defer rows.Close()
	statuses := map[string]string{}
	for rows.Next() {
		var kind, status string
		if err := rows.Scan(&kind, &status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		statuses[kind] = status
	}
	for _, tc := range cases {
		got := statuses[string(tc.kind)]
		if got != string(StatusDelivered) {
			t.Errorf("kind %s 应被确认丢弃为 %s(不隔离堆积),实为 %q", tc.kind, StatusDelivered, got)
		}
	}
}
