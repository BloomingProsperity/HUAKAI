//go:build integration_pg

package dlq

import (
	"context"
	"database/sql"
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
		NextRetryAt:    time.Now().UTC().Add(-time.Minute),
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
		NextRetryAt:    time.Now().UTC().Add(-time.Minute),
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

func TestStoreMarkRequiresCurrentLeaseOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openDLQPool(t, ctx)
	tenantID := seedDLQTenant(t, ctx, pool)
	store := NewStore(pool)

	for _, tc := range []struct {
		name        string
		finalStatus Status
		mark        func(context.Context, *Store, Record) error
	}{
		{
			name:        "delivered",
			finalStatus: StatusDelivered,
			mark: func(ctx context.Context, store *Store, rec Record) error {
				return store.MarkDelivered(ctx, rec)
			},
		},
		{
			name:        "failed",
			finalStatus: StatusPending,
			mark: func(ctx context.Context, store *Store, rec Record) error {
				return store.MarkFailed(ctx, rec, "current owner failure", pendingRetryDecision(rec))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recA, recB := claimExpiredThenReclaimedDLQRecord(t, ctx, pool, store, tenantID, "mark-"+tc.name)
			err := tc.mark(ctx, store, recA)
			if !errors.Is(err, ErrStaleLease) {
				t.Fatalf("stale worker Mark%s err=%v; want stale lease error", tc.name, err)
			}
			status, owner := readDLQStatusAndOwner(t, ctx, pool, recB.ID)
			if status != string(StatusInflight) || !owner.Valid || owner.String != *recB.LeaseOwner {
				t.Fatalf("stale Mark%s changed row status=%s owner=%q; want inflight owned by %q", tc.name, status, owner.String, *recB.LeaseOwner)
			}
			if err := tc.mark(ctx, store, recB); err != nil {
				t.Fatalf("current owner Mark%s: %v", tc.name, err)
			}
			status, owner = readDLQStatusAndOwner(t, ctx, pool, recB.ID)
			if status != string(tc.finalStatus) || owner.Valid {
				t.Fatalf("current owner Mark%s status=%s owner_valid=%v; want %s with cleared owner", tc.name, status, owner.Valid, tc.finalStatus)
			}
		})
	}
}

func TestStoreMarkRequiresCurrentLeaseGenerationWhenOwnerReused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openDLQPool(t, ctx)
	tenantID := seedDLQTenant(t, ctx, pool)
	store := NewStore(pool)

	for _, tc := range []struct {
		name string
		mark func(context.Context, *Store, Record) error
	}{
		{
			name: "delivered",
			mark: func(ctx context.Context, store *Store, rec Record) error {
				return store.MarkDelivered(ctx, rec)
			},
		},
		{
			name: "failed",
			mark: func(ctx context.Context, store *Store, rec Record) error {
				return store.MarkFailed(ctx, rec, "stale worker failure", pendingRetryDecision(rec))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor := "worker-reused-" + tc.name
			recA, recB := claimExpiredThenReclaimedDLQRecordWithActors(t, ctx, pool, store, tenantID, "reused-"+tc.name, actor, actor)
			if recA.LeaseOwner == nil || recB.LeaseOwner == nil || *recA.LeaseOwner != *recB.LeaseOwner {
				t.Fatalf("test setup lease_owner A=%v B=%v; want reused owner", recA.LeaseOwner, recB.LeaseOwner)
			}
			if !recA.LeaseUntil.Valid || !recB.LeaseUntil.Valid || recA.LeaseUntil.Time.Equal(recB.LeaseUntil.Time) {
				t.Fatalf("test setup lease_until A=%v B=%v; want distinct lease generations", recA.LeaseUntil, recB.LeaseUntil)
			}
			err := tc.mark(ctx, store, recA)
			if !errors.Is(err, ErrStaleLease) {
				t.Fatalf("stale worker Mark%s with reused owner err=%v; want stale lease error", tc.name, err)
			}
			status, owner := readDLQStatusAndOwner(t, ctx, pool, recB.ID)
			if status != string(StatusInflight) || !owner.Valid || owner.String != *recB.LeaseOwner {
				t.Fatalf("stale Mark%s changed row status=%s owner=%q; want inflight owned by %q", tc.name, status, owner.String, *recB.LeaseOwner)
			}
		})
	}
}

func TestClaimByID_RefusesActiveLease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openDLQPool(t, ctx)
	tenantID := seedDLQTenant(t, ctx, pool)
	store := NewStore(pool)

	id, err := store.Enqueue(ctx, Event{
		TenantID:       tenantID,
		EventKind:      EventKindMetrics,
		Lane:           LaneHigh,
		Payload:        []byte(`{"metric":"active-lease"}`),
		FailureReason:  "active_lease_seed",
		IdempotencyKey: "metrics:active-lease-claim-by-id",
		SourceTable:    "metrics",
		SourceID:       1,
		NextRetryAt:    time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	activeOwner := "background-active-lease-worker"
	rec, err := store.Claim(ctx, LaneHigh, activeOwner, time.Minute)
	if err != nil {
		t.Fatalf("claim active lease: %v", err)
	}
	if rec == nil || rec.ID != id {
		t.Fatalf("claim active lease rec=%v; want id=%d", rec, id)
	}
	if rec.LeaseOwner == nil || *rec.LeaseOwner != activeOwner {
		t.Fatalf("active lease owner=%v; want %q", rec.LeaseOwner, activeOwner)
	}
	if !rec.LeaseUntil.Valid || !rec.LeaseUntil.Time.After(time.Now().UTC()) {
		t.Fatalf("active lease_until=%v; want future deadline", rec.LeaseUntil)
	}

	stolen, err := store.ClaimByID(ctx, id, "manual-replay", time.Minute)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ClaimByID active lease rec=%v err=%v; want ErrNotFound", stolen, err)
	}
	status, owner := readDLQStatusAndOwner(t, ctx, pool, id)
	if status != string(StatusInflight) || !owner.Valid || owner.String != activeOwner {
		t.Fatalf("ClaimByID changed active lease status=%s owner=%q; want inflight owned by %q", status, owner.String, activeOwner)
	}

	if _, err := pool.Exec(ctx, `UPDATE usage_record_dlq SET lease_until = now() - interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatalf("expire active lease: %v", err)
	}
	reclaimed, err := store.ClaimByID(ctx, id, "manual-replay", time.Minute)
	if err != nil {
		t.Fatalf("ClaimByID expired lease: %v", err)
	}
	if reclaimed == nil || reclaimed.LeaseOwner == nil || *reclaimed.LeaseOwner != "manual:manual-replay" {
		t.Fatalf("expired lease owner=%v; want manual:manual-replay", reclaimed)
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

func claimExpiredThenReclaimedDLQRecord(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *Store, tenantID int64, key string) (Record, Record) {
	t.Helper()
	return claimExpiredThenReclaimedDLQRecordWithActors(t, ctx, pool, store, tenantID, key, "worker-a-"+key, "worker-b-"+key)
}

func claimExpiredThenReclaimedDLQRecordWithActors(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *Store, tenantID int64, key string, actorA string, actorB string) (Record, Record) {
	t.Helper()
	id, err := store.Enqueue(ctx, Event{
		TenantID:       tenantID,
		EventKind:      EventKindMetrics,
		Lane:           LaneLow,
		Payload:        []byte(`{"metric":"lease-owner"}`),
		FailureReason:  "lease_owner_seed",
		IdempotencyKey: "metrics:" + key,
		SourceTable:    "metrics",
		SourceID:       1,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	recA, err := store.ClaimByID(ctx, id, actorA, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("claim worker A: %v", err)
	}
	if recA.LeaseOwner == nil || *recA.LeaseOwner != "manual:"+actorA {
		t.Fatalf("worker A lease_owner=%v; want manual:%s", recA.LeaseOwner, actorA)
	}
	if _, err := pool.Exec(ctx, `UPDATE usage_record_dlq SET lease_until = now() - interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatalf("expire worker A lease: %v", err)
	}
	recB, err := store.ClaimByID(ctx, id, actorB, time.Minute)
	if err != nil {
		t.Fatalf("claim worker B: %v", err)
	}
	if recB.LeaseOwner == nil || *recB.LeaseOwner != "manual:"+actorB {
		t.Fatalf("worker B lease_owner=%v; want manual:%s", recB.LeaseOwner, actorB)
	}
	return *recA, *recB
}

func readDLQStatusAndOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) (string, sql.NullString) {
	t.Helper()
	var status string
	var owner sql.NullString
	if err := pool.QueryRow(ctx, `SELECT status, lease_owner FROM usage_record_dlq WHERE id=$1`, id).Scan(&status, &owner); err != nil {
		t.Fatalf("read dlq row: %v", err)
	}
	return status, owner
}

func pendingRetryDecision(rec Record) RetryDecision {
	return RetryDecision{Status: StatusPending, NextRetryAt: time.Now().UTC().Add(time.Minute), Attempts: rec.ReplayAttempts + 1, Delay: time.Minute}
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
