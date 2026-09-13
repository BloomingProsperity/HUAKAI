package workerpulse

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestProjectDistinguishesExecutorFollowerAndStale(t *testing.T) {
	now := time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
	view := Project(now, []Pulse{
		{ReplicaID: "node-b", LastSeen: now.Add(-30 * time.Second), SuccessAt: now.Add(-time.Hour), Executor: false},
		{ReplicaID: "node-a", LastSeen: now.Add(-10 * time.Second), SuccessAt: now.Add(-10 * time.Second), LastError: "", Executor: true},
		{ReplicaID: "node-old", LastSeen: now.Add(-2 * time.Hour), SuccessAt: now.Add(-3 * time.Hour), Executor: true},
	}, 3*time.Minute)
	if !view.Running || view.ExecutorReplica != "node-a" || view.FreshReplicaCount != 2 || view.ReplicaCount != 3 {
		t.Fatalf("cluster=%+v", view)
	}
	if view.LastTickAt == nil || *view.LastTickAt != "2026-09-13T02:59:50Z" {
		t.Fatalf("last_tick=%v", view.LastTickAt)
	}
	if view.LastSuccessAt == nil || *view.LastSuccessAt != "2026-09-13T02:59:50Z" {
		t.Fatalf("last_success=%v", view.LastSuccessAt)
	}
	roles := map[string]string{}
	for _, item := range view.Replicas {
		roles[item.ReplicaID] = item.Role
	}
	if roles["node-a"] != RoleExecutor || roles["node-b"] != RoleFollower || roles["node-old"] != RoleStale {
		t.Fatalf("roles=%v", roles)
	}
}

func TestProjectEmptyMeansNotRunning(t *testing.T) {
	view := Project(time.Now().UTC(), nil, time.Minute)
	if view.Running || view.ExecutorReplica != "" || view.ReplicaCount != 0 || view.LastTickAt != nil {
		t.Fatalf("empty cluster should be not running: %+v", view)
	}
}

func TestProjectFollowerCannotBecomeExecutor(t *testing.T) {
	now := time.Now().UTC()
	view := Project(now, []Pulse{
		{ReplicaID: "follower", LastSeen: now, Executor: false, LastError: "should not win"},
	}, time.Minute)
	if view.Running || view.ExecutorReplica != "" || view.LastError != "" || view.LastTickAt != nil {
		t.Fatalf("follower impersonated running cluster: %+v", view)
	}
}

func TestLoadFailsClosedWhenReaderMissingOrBroken(t *testing.T) {
	if _, err := Load(context.Background(), nil, JobModelSync, time.Now().UTC()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil reader err=%v", err)
	}
	store := NewMemoryStore()
	store.Fail(errors.New("database unavailable"))
	if _, err := Load(context.Background(), store, JobModelSync, time.Now().UTC()); err == nil {
		t.Fatal("broken reader must fail")
	}
}

func TestReplicaIDPrefersEnv(t *testing.T) {
	t.Setenv(replicaEnv, "  replica-west-1  ")
	if got := ReplicaID(); got != "replica-west-1" {
		t.Fatalf("ReplicaID=%q", got)
	}
	t.Setenv(replicaEnv, "")
	if got := ReplicaID(); got == "" {
		t.Fatal("empty replica id")
	}
	_ = os.Getenv(replicaEnv)
}

func TestMemoryStoreKeepsPeerSuccessWhenFollowerWrites(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.Record(ctx, Record{JobKey: JobModelSync, ReplicaID: "a", SeenAt: now, SuccessAt: now, Executor: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, Record{JobKey: JobModelSync, ReplicaID: "b", SeenAt: now, Executor: false}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.List(ctx, JobModelSync)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Pulse{}
	for _, row := range rows {
		byID[row.ReplicaID] = row
	}
	if byID["a"].SuccessAt.IsZero() || !byID["a"].Executor || byID["b"].Executor {
		t.Fatalf("peer overwrite: %+v", byID)
	}
}

func TestProjectRunningRequiresFreshExecutor(t *testing.T) {
	now := time.Now().UTC()
	view := Project(now, []Pulse{
		{ReplicaID: "old-exec", LastSeen: now.Add(-2 * time.Hour), SuccessAt: now.Add(-2 * time.Hour), Executor: true},
		{ReplicaID: "follower", LastSeen: now, Executor: false},
	}, 3*time.Minute)
	if view.Running || view.ExecutorReplica != "" || view.FreshReplicaCount != 1 {
		t.Fatalf("stale executor plus follower looked running: %+v", view)
	}
	if view.LastTickAt == nil || view.LastSuccessAt == nil {
		t.Fatal("stale executor last tick/success should remain visible")
	}
}
