//go:build integration_pg

package announcement

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestPostgresStoreListActiveFiltersAndTenantScope(t *testing.T) {
	// MUTATION: remove active/expires_at/published_at or tenant_id SQL predicates; hidden or cross-tenant rows appear in the active list.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openAnnouncementPool(t, ctx)
	tenantA := seedAnnouncementTenant(t, ctx, pool, "ann-a")
	tenantB := seedAnnouncementTenant(t, ctx, pool, "ann-b")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM announcements WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1,$2)`, tenantA, tenantB)
	})
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewPostgresStore(pool), WithClock(func() time.Time { return now }))

	mustCreatePGAnnouncement(t, ctx, svc, CreateInput{TenantID: tenantA, Title: "active-a", Body: "visible", PublishedAt: ptrTime(now.Add(-time.Hour))})
	mustCreatePGAnnouncement(t, ctx, svc, CreateInput{TenantID: tenantA, Title: "inactive-a", Body: "hidden", Active: ptrBool(false), PublishedAt: ptrTime(now.Add(-time.Hour))})
	mustCreatePGAnnouncement(t, ctx, svc, CreateInput{TenantID: tenantA, Title: "expired-a", Body: "hidden", PublishedAt: ptrTime(now.Add(-2 * time.Hour)), ExpiresAt: ptrTime(now.Add(-time.Minute))})
	mustCreatePGAnnouncement(t, ctx, svc, CreateInput{TenantID: tenantA, Title: "future-a", Body: "hidden", PublishedAt: ptrTime(now.Add(time.Hour))})
	mustCreatePGAnnouncement(t, ctx, svc, CreateInput{TenantID: tenantB, Title: "active-b", Body: "hidden", PublishedAt: ptrTime(now.Add(-time.Hour))})

	items, err := svc.ListActive(ctx, ListActiveInput{TenantID: tenantA, Now: now, Limit: 50})
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(items) != 1 || items[0].Title != "active-a" || items[0].TenantID != tenantA {
		t.Fatalf("items=%+v want only tenant A active visible row", items)
	}
}

func TestMigration0102(t *testing.T) {
	// MUTATION: omit announcements table, severity CHECK, active published index, or down DROP; these schema probes fail.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openAnnouncementPool(t, ctx)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	defer func() {
		c := context.Background()
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS announcements`)
		_, _ = conn.Exec(c, `DROP TABLE IF EXISTS tenants`)
		_, _ = conn.Exec(c, `RESET search_path`)
	}()

	upSQL := readAnnouncementMigration(t, "0102_announcements.up.sql")
	downSQL := readAnnouncementMigration(t, "0102_announcements.down.sql")
	if _, err := conn.Exec(ctx, `SET search_path TO pg_temp`); err != nil {
		t.Fatalf("set temp search_path: %v", err)
	}
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE tenants (id bigint PRIMARY KEY)`); err != nil {
		t.Fatalf("create temp tenants: %v", err)
	}
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("0102 up in temp schema: %v", err)
	}

	var tables int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relnamespace = pg_my_temp_schema() AND relname = 'announcements'`).Scan(&tables); err != nil {
		t.Fatalf("table probe: %v", err)
	}
	if tables != 1 {
		t.Fatalf("temp announcements tables=%d want 1", tables)
	}
	var severityChecks int
	if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM pg_constraint
WHERE conrelid = 'announcements'::regclass
  AND contype = 'c'
  AND pg_get_constraintdef(oid) ILIKE '%severity%'
  AND pg_get_constraintdef(oid) ILIKE '%critical%'`).Scan(&severityChecks); err != nil {
		t.Fatalf("severity check probe: %v", err)
	}
	if severityChecks == 0 {
		t.Fatal("announcements severity CHECK missing")
	}
	var indexes int
	if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM pg_indexes
WHERE schemaname LIKE 'pg_temp_%'
  AND tablename = 'announcements'
  AND indexdef ILIKE '%tenant_id%'
  AND indexdef ILIKE '%active%'
  AND indexdef ILIKE '%published_at%'`).Scan(&indexes); err != nil {
		t.Fatalf("index probe: %v", err)
	}
	if indexes == 0 {
		t.Fatal("announcements tenant/active/published index missing")
	}
	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("0102 down in temp schema: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relnamespace = pg_my_temp_schema() AND relname = 'announcements'`).Scan(&tables); err != nil {
		t.Fatalf("post-down table probe: %v", err)
	}
	if tables != 0 {
		t.Fatalf("temp announcements tables after down=%d want 0", tables)
	}
}

func openAnnouncementPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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

func seedAnnouncementTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, prefix string) int64 {
	t.Helper()
	name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return id
}

func mustCreatePGAnnouncement(t *testing.T, ctx context.Context, svc *Service, in CreateInput) Announcement {
	t.Helper()
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create seed %q: %v", in.Title, err)
	}
	return created
}

func readAnnouncementMigration(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "sql", "migrations", name))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(body)
}
