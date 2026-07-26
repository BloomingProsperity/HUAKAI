//go:build integration_pg

package dlq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresAdminListAndReplayDead(t *testing.T) {
	ctx := context.Background()
	pool := obsDLQTestPool(t)
	store := NewPostgresOutbox(pool)
	tenantID := obsDLQInsertTenant(t, ctx, pool, "obs-dlq-admin-"+fmt.Sprint(time.Now().UnixNano()))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	})

	email := obsDLQDeadEvent(t, ctx, store, tenantID, EventTypeEmailRetry)
	_ = obsDLQDeadEvent(t, ctx, store, tenantID, EventTypeAdminAlert)
	eventType := EventTypeEmailRetry
	rows, err := store.ListDead(ctx, AdminListFilter{TenantID: &tenantID, EventType: &eventType, Limit: 10})
	if err != nil {
		t.Fatalf("ListDead: %v", err)
	}
	if len(rows) != 1 || rows[0].OutboxEventID != email.ID || rows[0].EventType != EventTypeEmailRetry || rows[0].AttemptCount != 1 {
		t.Fatalf("rows=%+v, want only email dead row with joined attempt count", rows)
	}

	if _, err := store.ReplayDead(ctx, tenantID+1, rows[0].ID, "admin_token:wrong"); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("ReplayDead wrong tenant err=%v, want ErrReplayConflict", err)
	}
	result, err := store.ReplayDead(ctx, tenantID, rows[0].ID, "admin_token:7")
	if err != nil {
		t.Fatalf("ReplayDead first: %v", err)
	}
	if result.OutboxEventID != email.ID {
		t.Fatalf("replay result=%+v want outbox %s", result, email.ID)
	}
	var status Status
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT status, attempt_count FROM outbox_events WHERE id = $1`, email.ID).Scan(&status, &attempts); err != nil {
		t.Fatalf("read outbox after replay: %v", err)
	}
	if status != StatusPending || attempts != 0 {
		t.Fatalf("outbox after replay status=%s attempts=%d, want pending/0", status, attempts)
	}
	var actor string
	var replayedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT last_replay_actor, last_replay_at FROM dlq_events WHERE id = $1`, rows[0].ID).Scan(&actor, &replayedAt); err != nil {
		t.Fatalf("read replay actor: %v", err)
	}
	if actor != "admin_token:7" || replayedAt.IsZero() {
		t.Fatalf("replay actor/time=%q/%v", actor, replayedAt)
	}
	if _, err := store.ReplayDead(ctx, tenantID, rows[0].ID, "admin_token:7"); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("ReplayDead second err=%v, want ErrReplayConflict", err)
	}
}

func TestPostgresEnqueueSameIDIsIdempotentAndRejectsDifferentPayload(t *testing.T) {
	ctx := context.Background()
	pool := obsDLQTestPool(t)
	store := NewPostgresOutbox(pool)
	tenantID := obsDLQInsertTenant(t, ctx, pool, "obs-dlq-idempotent-"+fmt.Sprint(time.Now().UnixNano()))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	input := OutboxEvent{
		ID:       "obs-idempotent-" + fmt.Sprint(time.Now().UnixNano()),
		TenantID: tenantID, EventType: EventTypeSignupReward, Priority: PriorityHigh,
		Payload: json.RawMessage(`{"amount_cents":100}`),
	}
	first, err := store.Enqueue(ctx, input)
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if err := store.MarkCompleted(ctx, first.ID, ""); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	replayed, err := store.Enqueue(ctx, input)
	if err != nil {
		t.Fatalf("identical replay: %v", err)
	}
	if replayed.Status != StatusCompleted || replayed.ID != first.ID {
		t.Fatalf("replayed=%+v，期望返回原 completed 事件", replayed)
	}
	conflict := input
	conflict.Payload = json.RawMessage(`{"amount_cents":999}`)
	if _, err := store.Enqueue(ctx, conflict); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("same ID different payload err=%v want ErrEventConflict", err)
	}
}

func obsDLQTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func obsDLQInsertTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	return id
}

func obsDLQDeadEvent(t *testing.T, ctx context.Context, store *PostgresOutbox, tenantID int64, eventType string) OutboxEvent {
	t.Helper()
	ev, err := store.Enqueue(ctx, OutboxEvent{
		TenantID:  tenantID,
		EventType: eventType,
		Priority:  PriorityCritical,
		Payload:   json.RawMessage(`{"safe":"ok"}`),
	})
	if err != nil {
		t.Fatalf("enqueue %s: %v", eventType, err)
	}
	if err := store.MarkFailedDead(ctx, ev.ID, "", "dead reason"); err != nil {
		t.Fatalf("mark dead %s: %v", eventType, err)
	}
	return ev
}
