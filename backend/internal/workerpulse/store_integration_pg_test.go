//go:build integration_pg

package workerpulse

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func openPulsePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("HUAKAI_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL 或 HUAKAI_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接 PostgreSQL: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("探测 PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestStoreFollowerWriteDoesNotCoverPeerSuccess(t *testing.T) {
	pool := openPulsePool(t)
	store := NewStore(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	job := JobModelSync + "-itest"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM worker_job_pulses WHERE job_key=$1`, job)
	})
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Record(ctx, Record{JobKey: job, ReplicaID: "leader", SeenAt: now, SuccessAt: now, Executor: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, Record{JobKey: job, ReplicaID: "follower", SeenAt: now.Add(time.Second), Executor: false, LastError: ""}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.List(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d", len(rows))
	}
	view := Project(now.Add(2*time.Second), rows, time.Minute)
	if !view.Running || view.ExecutorReplica != "leader" || view.LastSuccessAt == nil {
		t.Fatalf("cluster=%+v", view)
	}
}

func TestStoreNilPoolFailsClosed(t *testing.T) {
	store := NewStore(nil)
	if err := store.Record(context.Background(), Record{JobKey: JobModelSync, ReplicaID: "a"}); err == nil {
		t.Fatal("nil pool record must fail")
	}
	if _, err := store.List(context.Background(), JobModelSync); err == nil {
		t.Fatal("nil pool list must fail")
	}
}
