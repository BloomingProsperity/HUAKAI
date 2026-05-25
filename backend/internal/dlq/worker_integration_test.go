//go:build integration_pg

package dlq

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestWorkerFailureFiveRetriesOperatorReview(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openDLQPool(t, ctx)
	tenantID := seedDLQTenant(t, ctx, pool)
	store := NewStore(pool)
	_, err := store.Enqueue(ctx, Event{
		TenantID:       tenantID,
		EventKind:      EventKindMetrics,
		Lane:           LaneLow,
		Payload:        []byte(`{"metric":"lost"}`),
		FailureReason:  "handler_failure_seed",
		IdempotencyKey: "metrics:test-five-retry",
		SourceTable:    "metrics",
		SourceID:       1,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	service := NewService(store, WithPolicy(RetryPolicy{
		BaseBackoff: time.Second,
		CapBackoff:  time.Second,
		MaxAttempts: 5,
		DLQAfter:    15 * time.Minute,
	}))
	service.Register(EventKindMetrics, func(context.Context, Record) error {
		return errors.New("forced handler failure")
	})
	worker := NewWorker(service, WorkerConfig{LowWorkers: 1, LeaseTTL: time.Second})
	for i := 0; i < 5; i++ {
		processed, err := worker.RunOnce(ctx, LaneLow, "test-worker")
		if err != nil {
			t.Fatalf("run once %d: %v", i+1, err)
		}
		if !processed {
			t.Fatalf("run once %d did not claim event", i+1)
		}
		_, _ = pool.Exec(ctx, `UPDATE usage_record_dlq SET next_retry_at = now() - interval '1 second' WHERE tenant_id=$1`, tenantID)
	}

	var status string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT status, replay_attempts FROM usage_record_dlq WHERE tenant_id=$1 AND event_kind='metrics'`,
		tenantID,
	).Scan(&status, &attempts); err != nil {
		t.Fatalf("read DLQ status: %v", err)
	}
	if status != string(StatusOperatorReview) || attempts != 5 {
		t.Fatalf("status=%s attempts=%d; want operator_review/5", status, attempts)
	}
}

func TestReplicaDownFailureStaysDurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openDLQPool(t, ctx)
	tenantID := seedDLQTenant(t, ctx, pool)
	store := NewStore(pool)
	_, err := store.Enqueue(ctx, Event{
		TenantID:       tenantID,
		EventKind:      EventKindBillingEventReplica,
		Lane:           LaneHigh,
		Payload:        []byte(`{"billing_event_id":1}`),
		FailureReason:  "replica_pending",
		ReplicaTarget:  "replica-down-test",
		ReplicaStatus:  ReplicaStatusPending,
		IdempotencyKey: "billing_event_replica:test-down",
		SourceTable:    "billing_events",
		SourceID:       1,
	})
	if err != nil {
		t.Fatalf("enqueue replica: %v", err)
	}

	service := NewService(store, WithPolicy(RetryPolicy{
		BaseBackoff: time.Second,
		CapBackoff:  time.Second,
		MaxAttempts: 1,
		DLQAfter:    15 * time.Minute,
	}))
	service.Register(EventKindBillingEventReplica, func(context.Context, Record) error {
		return errors.New("replica unavailable")
	})
	worker := NewWorker(service, WorkerConfig{HighWorkers: 1, LeaseTTL: time.Second})
	processed, err := worker.RunOnce(ctx, LaneHigh, "replica-test-worker")
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !processed {
		t.Fatalf("replica event was not claimed")
	}

	var status, replicaStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status, replica_status FROM usage_record_dlq WHERE tenant_id=$1 AND event_kind='billing_event_replica'`,
		tenantID,
	).Scan(&status, &replicaStatus); err != nil {
		t.Fatalf("read replica status: %v", err)
	}
	if status != string(StatusOperatorReview) || replicaStatus != ReplicaStatusFailed {
		t.Fatalf("status=%s replica_status=%s; want operator_review/failed", status, replicaStatus)
	}
}

func openDLQPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedDLQTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "dlq-test-"+time.Now().Format("150405.000000000")).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM usage_record_dlq WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return tenantID
}
