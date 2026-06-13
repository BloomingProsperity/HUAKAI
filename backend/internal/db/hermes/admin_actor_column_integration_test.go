//go:build integration_pg

package hermes

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestHermesAdminActorColumnsExistAndNullable guards migration 0144: the
// nullable admin_actor_token_id column must be present on BOTH
// hermes_audit_events and hermes_conversations and must NOT be NOT NULL.
//
// This is the discriminating regression guard. The unit tests use in-memory
// fakes / sqlc params that do not reflect live schema, so they cannot catch a
// missing or wrongly-constrained column. This test runs against the real gate
// DB and FAILS if 0144 is reverted (the columns are absent) or if a future edit
// makes the column NOT NULL (which would break the existing end-user-path
// INSERTs that never set it). Verified discriminating: before 0144 the rows
// returned by the catalog query are empty.
func TestHermesAdminActorColumnsExistAndNullable(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping hermes admin-actor column integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pg ping: %v", err)
	}

	for _, table := range []string{"hermes_audit_events", "hermes_conversations"} {
		var dataType, isNullable string
		err := pool.QueryRow(ctx,
			`SELECT data_type, is_nullable
               FROM information_schema.columns
              WHERE table_name = $1 AND column_name = 'admin_actor_token_id'`,
			table,
		).Scan(&dataType, &isNullable)
		if err != nil {
			t.Fatalf("%s.admin_actor_token_id missing (migration 0144 not applied?): %v", table, err)
		}
		if !strings.EqualFold(dataType, "bigint") {
			t.Fatalf("%s.admin_actor_token_id data_type=%s want bigint", table, dataType)
		}
		// Discriminating: the column must stay nullable so the legacy end-user
		// INSERTs that never set it continue to succeed.
		if !strings.EqualFold(isNullable, "YES") {
			t.Fatalf("%s.admin_actor_token_id is_nullable=%s want YES", table, isNullable)
		}
	}
}
