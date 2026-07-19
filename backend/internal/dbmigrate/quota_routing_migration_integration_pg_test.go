//go:build integration_pg

package dbmigrate_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestQuotaRoutingMigrationRoundTrip(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()
	dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_quota_routing_roundtrip")
	runner := newEmbeddedMigrationRunner(t, dsn)
	if err := runner.Migrate(201); err != nil {
		t.Fatalf("迁移到 0201: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("连接临时库: %v", err)
	}
	defer conn.Close(ctx)
	assertMigrationTablePresence(t, ctx, conn, "provider_account_quota_facts", false)
	assertMigrationTablePresence(t, ctx, conn, "provider_account_routing_signals", false)
	if err := runner.Migrate(202); err != nil {
		t.Fatalf("升级到 0202: %v", err)
	}
	assertMigrationTablePresence(t, ctx, conn, "provider_account_quota_facts", true)
	assertMigrationTablePresence(t, ctx, conn, "provider_account_routing_signals", true)
	assertMigrationColumnPresence(t, ctx, conn, "provider_accounts", "upstream_cost_ratio", false)
	if err := runner.Migrate(203); err != nil {
		t.Fatalf("升级到 0203: %v", err)
	}
	assertMigrationColumnPresence(t, ctx, conn, "provider_accounts", "upstream_cost_ratio", true)
	if err := runner.Steps(-1); err != nil {
		t.Fatalf("回退 0203: %v", err)
	}
	assertMigrationColumnPresence(t, ctx, conn, "provider_accounts", "upstream_cost_ratio", false)
	assertMigrationTablePresence(t, ctx, conn, "provider_account_quota_facts", true)
	assertMigrationTablePresence(t, ctx, conn, "provider_account_routing_signals", true)
	if err := runner.Steps(-1); err != nil {
		t.Fatalf("回退 0202: %v", err)
	}
	assertMigrationTablePresence(t, ctx, conn, "provider_account_quota_facts", false)
	assertMigrationTablePresence(t, ctx, conn, "provider_account_routing_signals", false)
	if err := runner.Migrate(203); err != nil {
		t.Fatalf("回退后重升 0203: %v", err)
	}
}

func assertMigrationColumnPresence(t *testing.T, ctx context.Context, conn *pgx.Conn, table, column string, want bool) {
	t.Helper()
	var exists bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		)`, table, column).Scan(&exists); err != nil {
		t.Fatalf("检查列 %s.%s: %v", table, column, err)
	}
	if exists != want {
		t.Fatalf("列 %s.%s exists=%v want %v", table, column, exists, want)
	}
}
