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

	result, err := store.ReplayDead(ctx, rows[0].ID)
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
	if _, err := store.ReplayDead(ctx, rows[0].ID); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("ReplayDead second err=%v, want ErrReplayConflict", err)
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
