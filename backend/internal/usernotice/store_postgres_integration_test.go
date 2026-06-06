//go:build integration_pg

package usernotice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestBroadcast_FansOutToTenantUsers(t *testing.T) {
	// MUTATION: drop tenant_id/status/deleted_at filter in fan-out INSERT; disabled/deleted/cross-tenant users receive rows.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openUserNoticePool(t, ctx)
	tenantA := seedUserNoticeTenant(t, ctx, pool, "notice-a")
	tenantB := seedUserNoticeTenant(t, ctx, pool, "notice-b")
	activeA1 := seedUserNoticeUser(t, ctx, pool, tenantA, "active-a1", "active", false)
	activeA2 := seedUserNoticeUser(t, ctx, pool, tenantA, "active-a2", "active", false)
	disabledA := seedUserNoticeUser(t, ctx, pool, tenantA, "disabled-a", "disabled", false)
	deletedA := seedUserNoticeUser(t, ctx, pool, tenantA, "deleted-a", "active", true)
	activeB := seedUserNoticeUser(t, ctx, pool, tenantB, "active-b", "active", false)
	cleanupUserNoticeRows(t, pool, tenantA, tenantB)

	svc := NewService(NewPostgresStore(pool))
	result, err := svc.Broadcast(ctx, BroadcastInput{TenantID: tenantA, Title: "Ops", Body: "Tenant A only", Severity: SeverityCritical})
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if result.Inserted != 2 {
		t.Fatalf("inserted=%d want 2 active tenant A users", result.Inserted)
	}

	assertUserNoticeCount(t, ctx, pool, tenantA, activeA1, 1)
	assertUserNoticeCount(t, ctx, pool, tenantA, activeA2, 1)
	assertUserNoticeCount(t, ctx, pool, tenantA, disabledA, 0)
	assertUserNoticeCount(t, ctx, pool, tenantA, deletedA, 0)
	assertUserNoticeCount(t, ctx, pool, tenantB, activeB, 0)
}

func TestListNotifications_SelfScopedUnreadFirst(t *testing.T) {
	// MUTATION: drop user_id scope or unread_only predicate from ListForUser; user A sees user B rows or read rows in unread-only mode.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openUserNoticePool(t, ctx)
	tenant := seedUserNoticeTenant(t, ctx, pool, "notice-list")
	userA := seedUserNoticeUser(t, ctx, pool, tenant, "user-a", "active", false)
	userB := seedUserNoticeUser(t, ctx, pool, tenant, "user-b", "active", false)
	cleanupUserNoticeRows(t, pool, tenant)

	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewPostgresStore(pool), WithClock(func() time.Time { return now }))
	if _, err := svc.Broadcast(ctx, BroadcastInput{TenantID: tenant, Title: "old", Body: "body"}); err != nil {
		t.Fatalf("Broadcast old: %v", err)
	}
	oldA := mustListPGNotices(t, svc, tenant, userA, false)[0]
	if _, err := svc.MarkRead(ctx, MarkReadInput{TenantID: tenant, UserID: userA, ID: oldA.ID}); err != nil {
		t.Fatalf("MarkRead old A: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := svc.Broadcast(ctx, BroadcastInput{TenantID: tenant, Title: "new", Body: "body"}); err != nil {
		t.Fatalf("Broadcast new: %v", err)
	}

	items, err := svc.ListForUser(ctx, ListInput{TenantID: tenant, UserID: userA, Limit: 50})
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(items) != 2 || items[0].Title != "new" || items[1].Title != "old" {
		t.Fatalf("items=%+v want user A rows newest first", items)
	}
	for _, item := range items {
		if item.UserID != userA {
			t.Fatalf("items=%+v include row outside user A; userB=%d", items, userB)
		}
	}
	unread, err := svc.ListForUser(ctx, ListInput{TenantID: tenant, UserID: userA, UnreadOnly: true, Limit: 50})
	if err != nil {
		t.Fatalf("ListForUser unread: %v", err)
	}
	if len(unread) != 1 || unread[0].Title != "new" || unread[0].ReadAt != nil {
		t.Fatalf("unread=%+v want only new unread row", unread)
	}
}

func TestMarkRead_OwnOnly(t *testing.T) {
	// MUTATION: drop user_id scope from MarkRead UPDATE; user A marks user B's notification read.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openUserNoticePool(t, ctx)
	tenant := seedUserNoticeTenant(t, ctx, pool, "notice-read")
	userA := seedUserNoticeUser(t, ctx, pool, tenant, "user-a", "active", false)
	userB := seedUserNoticeUser(t, ctx, pool, tenant, "user-b", "active", false)
	cleanupUserNoticeRows(t, pool, tenant)

	svc := NewService(NewPostgresStore(pool))
	if _, err := svc.Broadcast(ctx, BroadcastInput{TenantID: tenant, Title: "read me", Body: "body"}); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	rowB := mustListPGNotices(t, svc, tenant, userB, true)[0]
	if _, err := svc.MarkRead(ctx, MarkReadInput{TenantID: tenant, UserID: userA, ID: rowB.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkRead cross-user err=%v want ErrNotFound", err)
	}
	countB, err := svc.UnreadCount(ctx, tenant, userB)
	if err != nil {
		t.Fatalf("UnreadCount B: %v", err)
	}
	if countB != 1 {
		t.Fatalf("user B unread count=%d want 1 after user A cross-read attempt", countB)
	}
}

func TestMigration0104(t *testing.T) {
	// MUTATION: omit user_notifications table, severity CHECK, tenant/user/read index, composite FK, or down DROP; these schema probes fail.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openUserNoticePool(t, ctx)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	defer func() {
		c := context.Background()
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS user_notifications`)
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS users`)
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS tenants`)
		_, _ = conn.Exec(c, `RESET search_path`)
	}()

	upSQL := readUserNoticeMigration(t, "0104_user_notifications.up.sql")
	downSQL := readUserNoticeMigration(t, "0104_user_notifications.down.sql")
	if _, err := conn.Exec(ctx, `SET search_path TO pg_temp`); err != nil {
		t.Fatalf("set temp search_path: %v", err)
	}
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE tenants (id bigint PRIMARY KEY)`); err != nil {
		t.Fatalf("create temp tenants: %v", err)
	}
	if _, err := conn.Exec(ctx, `
CREATE TEMP TABLE users (
    id bigint PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    status text NOT NULL,
    deleted_at timestamptz,
    UNIQUE (tenant_id, id)
)`); err != nil {
		t.Fatalf("create temp users: %v", err)
	}
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("0104 up in temp schema: %v", err)
	}

	var tables int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relnamespace = pg_my_temp_schema() AND relname = 'user_notifications'`).Scan(&tables); err != nil {
		t.Fatalf("table probe: %v", err)
	}
	if tables != 1 {
		t.Fatalf("temp user_notifications tables=%d want 1", tables)
	}
	var severityChecks int
	if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM pg_constraint
WHERE conrelid = 'user_notifications'::regclass
  AND contype = 'c'
  AND pg_get_constraintdef(oid) ILIKE '%severity%'
  AND pg_get_constraintdef(oid) ILIKE '%critical%'`).Scan(&severityChecks); err != nil {
		t.Fatalf("severity check probe: %v", err)
	}
	if severityChecks == 0 {
		t.Fatal("user_notifications severity CHECK missing")
	}
	var indexes int
	if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM pg_indexes
WHERE schemaname LIKE 'pg_temp_%'
  AND tablename = 'user_notifications'
  AND indexdef ILIKE '%tenant_id%'
  AND indexdef ILIKE '%user_id%'
  AND indexdef ILIKE '%read_at%'`).Scan(&indexes); err != nil {
		t.Fatalf("index probe: %v", err)
	}
	if indexes == 0 {
		t.Fatal("user_notifications tenant/user/read_at index missing")
	}
	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("0104 down in temp schema: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relnamespace = pg_my_temp_schema() AND relname = 'user_notifications'`).Scan(&tables); err != nil {
		t.Fatalf("post-down table probe: %v", err)
	}
	if tables != 0 {
		t.Fatalf("temp user_notifications tables after down=%d want 0", tables)
	}
}

func openUserNoticePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedUserNoticeTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, prefix string) int64 {
	t.Helper()
	name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return id
}

func seedUserNoticeUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, displayName, status string, deleted bool) int64 {
	t.Helper()
	var deletedAt any
	if deleted {
		deletedAt = time.Now().UTC()
	}
	var id int64
	if err := pool.QueryRow(ctx, `
INSERT INTO users (tenant_id, display_name, status, deleted_at)
VALUES ($1, $2, $3, $4)
RETURNING id`, tenantID, displayName, status, deletedAt).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", displayName, err)
	}
	return id
}

func cleanupUserNoticeRows(t *testing.T, pool *pgxpool.Pool, tenantIDs ...int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, tenantID := range tenantIDs {
			_, _ = pool.Exec(ctx, `DELETE FROM user_notifications WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
		}
	})
}

func assertUserNoticeCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_notifications WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID).Scan(&count); err != nil {
		t.Fatalf("count user_notifications tenant=%d user=%d: %v", tenantID, userID, err)
	}
	if count != want {
		t.Fatalf("tenant=%d user=%d notices=%d want %d", tenantID, userID, count, want)
	}
}

func mustListPGNotices(t *testing.T, svc *Service, tenantID, userID int64, unreadOnly bool) []Notification {
	t.Helper()
	items, err := svc.ListForUser(context.Background(), ListInput{TenantID: tenantID, UserID: userID, UnreadOnly: unreadOnly, Limit: 50})
	if err != nil {
		t.Fatalf("ListForUser tenant=%d user=%d unread=%v: %v", tenantID, userID, unreadOnly, err)
	}
	return items
}

func readUserNoticeMigration(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "sql", "migrations", name))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(body)
}
