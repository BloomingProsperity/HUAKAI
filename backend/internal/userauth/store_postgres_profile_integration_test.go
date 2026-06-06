//go:build integration_pg

package userauth

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPGUpdateDisplayNamePersistsAndStaysTenantScoped(t *testing.T) {
	ctx := context.Background()
	pool := openUserAuthProfilePool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantA := seedUserAuthProfileTenant(t, ctx, pool, "profile-a-"+suffix)
	tenantB := seedUserAuthProfileTenant(t, ctx, pool, "profile-b-"+suffix)
	userA := seedUserAuthProfileUser(t, ctx, pool, tenantA, "Alice")
	userB := seedUserAuthProfileUser(t, ctx, pool, tenantB, "Bob")
	t.Cleanup(func() {
		cleanupUserAuthProfileTenant(t, ctx, pool, tenantA)
		cleanupUserAuthProfileTenant(t, ctx, pool, tenantB)
	})

	updated, err := store.UpdateDisplayName(ctx, tenantA, userA, "Alice Updated")
	if err != nil {
		t.Fatalf("UpdateDisplayName: %v", err)
	}
	if updated.DisplayName != "Alice Updated" || updated.TenantID != tenantA || updated.ID != userA {
		t.Fatalf("updated user=%+v, want tenant/user scoped Alice Updated", updated)
	}
	if got := readUserAuthProfileDisplayName(t, ctx, pool, tenantA, userA); got != "Alice Updated" {
		t.Fatalf("persisted display_name=%q want Alice Updated; MUTATION: UPDATE no-op should fail", got)
	}
	if got := readUserAuthProfileDisplayName(t, ctx, pool, tenantB, userB); got != "Bob" {
		t.Fatalf("tenant B display_name=%q want Bob", got)
	}

	if _, err := store.UpdateDisplayName(ctx, tenantB, userA, "Cross Tenant"); err != ErrUserNotFound {
		t.Fatalf("cross-tenant update err=%v want ErrUserNotFound; MUTATION: dropping tenant_id from UPDATE should write user A", err)
	}
	if got := readUserAuthProfileDisplayName(t, ctx, pool, tenantA, userA); got != "Alice Updated" {
		t.Fatalf("cross-tenant attempt changed tenant A display_name=%q", got)
	}
}

func openUserAuthProfilePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping postgres: %v", err)
	}
	return pool
}

func seedUserAuthProfileTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	return id
}

func seedUserAuthProfileUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, displayName string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name, status) VALUES ($1, $2, 'active') RETURNING id`, tenantID, displayName).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func readUserAuthProfileDisplayName(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64) string {
	t.Helper()
	var displayName string
	if err := pool.QueryRow(ctx, `SELECT display_name FROM users WHERE tenant_id=$1 AND id=$2`, tenantID, userID).Scan(&displayName); err != nil {
		t.Fatalf("read display_name: %v", err)
	}
	return displayName
}

func cleanupUserAuthProfileTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup users: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup tenant: %v", err)
	}
}
