//go:build integration_pg

package dbmigrate_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestBulkAuditMigrationKeepsTenantLifecycleActions(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()
	dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_bulk_audit_upgrade")
	runner := newEmbeddedMigrationRunner(t, dsn)
	if err := runner.Migrate(233); err != nil {
		t.Fatalf("迁移到 0233: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("连接 0233 临时库: %v", err)
	}
	defer conn.Close(ctx)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var tenantID int64
	if err := conn.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "bulk-audit-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}
	for _, action := range []string{"create_tenant", "enable_tenant", "disable_tenant", "delete_tenant"} {
		if _, err := conn.Exec(ctx, `
INSERT INTO admin_audit_events (tenant_id, actor_id, actor_role, action, target_type, reason)
VALUES ($1,'admin_token:376','platform_admin',$2,'tenant','0233存量')`, tenantID, action); err != nil {
			t.Fatalf("0233 未放行 %q: %v", action, err)
		}
	}
	if err := runner.Migrate(240); err != nil {
		t.Fatalf("存在 0233 租户日志时升级 0240: %v", err)
	}
	for _, action := range []string{"create_tenant", "enable_tenant", "disable_tenant", "delete_tenant", "move_provider_account_channel", "bulk_provider_accounts"} {
		target := "tenant"
		if action == "move_provider_account_channel" {
			target = "provider_account"
		}
		if action == "bulk_provider_accounts" {
			target = "provider_account_batch"
		}
		if _, err := conn.Exec(ctx, `
INSERT INTO admin_audit_events (tenant_id, actor_id, actor_role, action, target_type, reason)
VALUES ($1,'admin_token:376','platform_admin',$2,$3,'0240并集')`, tenantID, action, target); err != nil {
			t.Fatalf("0240 必须继续放行 %q: %v", action, err)
		}
	}
}
