//go:build integration_pg

package dbmigrate_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestModelDiscoveryMigrationRoundTripAndRollbackGuard(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()

	t.Run("空发现箱可往返", func(t *testing.T) {
		dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_model_discovery_roundtrip")
		runner := newEmbeddedMigrationRunner(t, dsn)
		if err := runner.Migrate(200); err != nil {
			t.Fatalf("迁移到 0200: %v", err)
		}
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatalf("连接临时库: %v", err)
		}
		defer conn.Close(ctx)
		assertMigrationTablePresence(t, ctx, conn, "model_discovery_inbox", false)
		if err := runner.Migrate(201); err != nil {
			t.Fatalf("升级到 0201: %v", err)
		}
		assertMigrationTablePresence(t, ctx, conn, "model_discovery_inbox", true)
		if err := runner.Steps(-1); err != nil {
			t.Fatalf("空发现箱回退 0201: %v", err)
		}
		assertMigrationTablePresence(t, ctx, conn, "model_discovery_inbox", false)
		if err := runner.Migrate(201); err != nil {
			t.Fatalf("回退后重新升级 0201: %v", err)
		}
		assertMigrationTablePresence(t, ctx, conn, "model_discovery_inbox", true)
	})

	t.Run("存在运营事实时拒绝回退", func(t *testing.T) {
		dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_model_discovery_guard")
		runner := newEmbeddedMigrationRunner(t, dsn)
		if err := runner.Migrate(201); err != nil {
			t.Fatalf("迁移到 0201: %v", err)
		}
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatalf("连接临时库: %v", err)
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, `
INSERT INTO model_discovery_inbox (
    vendor, model_id_normalized, provider_model_id, display_name, owned_by,
    protocol_family, status
) VALUES ('openai', 'gpt-guard', 'gpt-guard', 'GPT Guard', 'openai', 'openai_chat', 'pending')
`); err != nil {
			t.Fatalf("插入回退保护事实: %v", err)
		}
		if err := runner.Steps(-1); err == nil {
			t.Fatal("存在发现箱运营事实时回退 0201 竟然成功")
		}
		assertMigrationTablePresence(t, ctx, conn, "model_discovery_inbox", true)
		var count int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM model_discovery_inbox WHERE provider_model_id='gpt-guard'`).Scan(&count); err != nil {
			t.Fatalf("回退失败后读取运营事实: %v", err)
		}
		if count != 1 {
			t.Fatalf("回退失败后运营事实 count=%d want 1", count)
		}
	})
}

func assertMigrationTablePresence(t *testing.T, ctx context.Context, conn *pgx.Conn, table string, want bool) {
	t.Helper()
	var name *string
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.' || $1)::text`, table).Scan(&name); err != nil {
		t.Fatalf("查询表 %s: %v", table, err)
	}
	if got := name != nil; got != want {
		t.Fatalf("表 %s presence=%v want %v", table, got, want)
	}
}
