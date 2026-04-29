//go:build integration_pg

// Real PostgreSQL connection smoke test. Requires:
//   make db-up && make db-migrate
// then:
//   make test-integration
//
// Skipped in the default suite (no //go:build => standard build tag).
package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("HUAKAI_DATABASE_URL")
	if v == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	return v
}

// TestPgConnect verifies the pgxpool factory opens a real connection,
// passes liveness probe, and returns a usable *Queries handle.
func TestPgConnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := Open(ctx, PoolConfig{DSN: dsn(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	row := pool.QueryRow(ctx, "SELECT 1")
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("SELECT 1 scan: %v", err)
	}
	if n != 1 {
		t.Fatalf("SELECT 1 returned %d, want 1", n)
	}
}

// TestPgSchemaApplied confirms the 6 migrations landed and the key
// money-path tables (claim ledger + usage records + outbox) exist.
// If this fails, run `make db-migrate`.
func TestPgSchemaApplied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := Open(ctx, PoolConfig{DSN: dsn(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	expected := []string{
		"billing_ledger_claims",
		"usage_records",
		"scheduler_outbox",
		"provider_accounts",
		"pool_slot_acquisitions",
		"oauth_refresh_audit_events",
		"sticky_bindings",
	}
	for _, table := range expected {
		var present bool
		err := pool.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1)",
			table,
		).Scan(&present)
		if err != nil {
			t.Fatalf("query existence of %s: %v", table, err)
		}
		if !present {
			t.Errorf("expected table %s in schema; not found (did you run `make db-migrate`?)", table)
		}
	}

	// schema_migrations sanity: golang-migrate sets version=6 dirty=false after Phase B.3.
	var version int
	var dirty bool
	if err := pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatalf("schema_migrations probe: %v", err)
	}
	if dirty {
		t.Fatalf("schema_migrations is DIRTY at version %d; previous migration failed mid-flight", version)
	}
	if version < 6 {
		t.Fatalf("schema_migrations version=%d, expected >=6 (run `make db-migrate`)", version)
	}
}

// TestOpenWithoutDSN proves the contract from plan §F: "If a function
// cannot reach PG, it returns a typed error, not a 200 OK."
func TestOpenWithoutDSN(t *testing.T) {
	_, err := Open(context.Background(), PoolConfig{DSN: ""})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured for empty DSN; got %v", err)
	}
}
