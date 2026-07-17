//go:build integration_pg

package tenantcapability

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

func TestGrantRevokeExpiryAndTenantIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := openCapabilityPool(t, ctx)
	first := seedCapabilityTenant(t, ctx, pool, "first")
	second := seedCapabilityTenant(t, ctx, pool, "second")
	now := time.Date(2026, 7, 17, 4, 0, 0, 0, time.UTC)
	store := NewStore(pool)
	store.now = func() time.Time { return now }
	if err := store.Require(ctx, first, CRSAccountSync); !errors.Is(err, ErrDenied) {
		t.Fatalf("默认未授权 err=%v", err)
	}
	expires := now.Add(time.Minute)
	grant, err := store.Set(ctx, Mutation{TenantID: first, Capability: CRSAccountSync, Enabled: true,
		ExpiresAt: &expires, ActorID: "admin_token:1", Reason: "批准租户执行远程账号同步", Now: now})
	if err != nil || grant.Status != "granted" || grant.Revision != 1 {
		t.Fatalf("grant=%+v err=%v", grant, err)
	}
	if err := store.Require(ctx, first, CRSAccountSync); err != nil {
		t.Fatalf("有效授权 err=%v", err)
	}
	if err := store.Require(ctx, second, CRSAccountSync); !errors.Is(err, ErrDenied) {
		t.Fatalf("跨租户授权泄漏 err=%v", err)
	}
	store.now = func() time.Time { return now.Add(2 * time.Minute) }
	if err := store.Require(ctx, first, CRSAccountSync); !errors.Is(err, ErrDenied) {
		t.Fatalf("过期授权 err=%v", err)
	}
	revoked, err := store.Set(ctx, Mutation{TenantID: first, Capability: CRSAccountSync, Enabled: false,
		ActorID: "admin_token:1", Reason: "撤销租户远程账号同步授权", Now: now.Add(3 * time.Minute)})
	if err != nil || revoked.Status != "revoked" || revoked.Revision != 2 {
		t.Fatalf("revoke=%+v err=%v", revoked, err)
	}
	var events int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM tenant_capability_grant_events WHERE tenant_id=$1 AND capability=$2`, first, CRSAccountSync).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("events=%d want 2", events)
	}
}

func openCapabilityPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("HUAKAI_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL/HUAKAI_DATABASE_URL，跳过 integration_pg")
	}
	adminConnection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConnection.Close(ctx)
	databaseName := "huakai_capability_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConnection.Exec(ctx, "CREATE DATABASE "+quoteCapabilityIdentifier(databaseName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", databaseName)
		_, _ = cleanup.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quoteCapabilityIdentifier(databaseName))
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	if err := dbmigrate.Up(sqlmigrations.Files, parsed.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: parsed.String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedCapabilityTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, prefix string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, prefix+"-"+uuid.NewString()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func quoteCapabilityIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
