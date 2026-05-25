//go:build integration_pg

package admin

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	rootdb "github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openAdminAuditIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	p, err := rootdb.Open(ctx, rootdb.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestInsertAdminAuditEventPoolGroupCheckConstraints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	actorID := "admin-audit-pool-group-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	targetID := int64(77)
	requestID := actorID + "-request"

	_, err := q.InsertAdminAuditEvent(ctx, InsertAdminAuditEventParams{
		ActorID:    actorID,
		ActorRole:  "platform_admin",
		Action:     "create_pool_group",
		TargetType: "pool_group",
		TargetID:   &targetID,
		RequestID:  &requestID,
		Payload:    []byte(`{"source":"integration_pg"}`),
	})
	if err != nil {
		t.Fatalf("insert pool_group audit event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_audit_events WHERE actor_id = $1`, actorID)
	})

	_, err = q.InsertAdminAuditEvent(ctx, InsertAdminAuditEventParams{
		ActorID:    actorID,
		ActorRole:  "platform_admin",
		Action:     "bogus_pool_group_action",
		TargetType: "pool_group",
		TargetID:   &targetID,
		RequestID:  &requestID,
		Payload:    []byte(`{"source":"integration_pg"}`),
	})
	if err == nil {
		t.Fatalf("bogus action was accepted")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("bogus action returned non-Postgres error: %T %v", err, err)
	}
	if pgErr.Code != "23514" || pgErr.ConstraintName != "admin_audit_events_action_check" {
		t.Fatalf("bogus action error code=%s constraint=%s want CHECK admin_audit_events_action_check",
			pgErr.Code, pgErr.ConstraintName)
	}
}
