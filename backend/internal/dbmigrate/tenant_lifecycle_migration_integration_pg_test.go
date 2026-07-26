//go:build integration_pg

package dbmigrate_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestTenantLifecycleMigrationNormalizesPopulatedDatabaseAndGuardsRollback(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()
	dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_tenant_lifecycle_upgrade")
	runner := newEmbeddedMigrationRunner(t, dsn)
	if err := runner.Migrate(232); err != nil {
		t.Fatalf("迁移到 0232: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("连接 0232 临时库: %v", err)
	}
	defer conn.Close(ctx)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	activeID := insertLegacyTenant(t, ctx, conn, "active-"+suffix, "active", false)
	disabledID := insertLegacyTenant(t, ctx, conn, "suspended-"+suffix, "suspended", false)
	deletedID := insertLegacyTenant(t, ctx, conn, "deleted-"+suffix, "active", true)

	if err := runner.Migrate(233); err != nil {
		t.Fatalf("有存量租户时升级 0233: %v", err)
	}
	assertTenantLifecycleMigrationRow(t, ctx, conn, activeID, "active", false)
	assertTenantLifecycleMigrationRow(t, ctx, conn, disabledID, "disabled", false)
	assertTenantLifecycleMigrationRow(t, ctx, conn, deletedID, "deleted", true)

	assertTenantLifecycleConstraintRejects(t, ctx, conn,
		`INSERT INTO tenants (name,status) VALUES ($1,'suspended')`,
		"invalid-status-"+suffix,
	)
	assertTenantLifecycleConstraintRejects(t, ctx, conn,
		`INSERT INTO tenants (name,status) VALUES ($1,'deleted')`,
		"deleted-without-time-"+suffix,
	)

	for _, action := range []string{"create_tenant", "enable_tenant", "disable_tenant", "delete_tenant"} {
		if _, err := conn.Exec(ctx, `
INSERT INTO admin_audit_events (
    tenant_id, actor_id, actor_role, action, target_type, target_id, reason
) VALUES ($1,'admin_token:305','platform_admin',$2,'tenant',$1,'迁移合同验证')`,
			activeID, action,
		); err != nil {
			t.Fatalf("0233 未放行租户日志动作 %q: %v", action, err)
		}
	}
	if err := runner.Steps(-1); err == nil {
		t.Fatal("存在租户生命周期日志时 0233 回退意外成功")
	}
}

func insertLegacyTenant(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	name, status string,
	deleted bool,
) int64 {
	t.Helper()
	var tenantID int64
	var deletedAt any
	if deleted {
		deletedAt = time.Now().UTC()
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO tenants (name,status,deleted_at)
VALUES ($1,$2,$3)
RETURNING id`, name, status, deletedAt).Scan(&tenantID); err != nil {
		t.Fatalf("插入 0232 存量租户 name=%q status=%q: %v", name, status, err)
	}
	return tenantID
}

func assertTenantLifecycleMigrationRow(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	tenantID int64,
	wantStatus string,
	wantDeleted bool,
) {
	t.Helper()
	var status string
	var version int64
	var statusChangedAt time.Time
	var deleted bool
	if err := conn.QueryRow(ctx, `
SELECT status, version, status_changed_at, deleted_at IS NOT NULL
FROM tenants
WHERE id=$1`, tenantID).Scan(&status, &version, &statusChangedAt, &deleted); err != nil {
		t.Fatalf("读取迁移后租户 %d: %v", tenantID, err)
	}
	if status != wantStatus || version != 1 || statusChangedAt.IsZero() || deleted != wantDeleted {
		t.Fatalf("迁移后租户 %d status/version/changed/deleted=%q/%d/%v/%v want %q/1/nonzero/%v",
			tenantID, status, version, statusChangedAt, deleted, wantStatus, wantDeleted)
	}
}

func assertTenantLifecycleConstraintRejects(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	query string,
	args ...any,
) {
	t.Helper()
	_, err := conn.Exec(ctx, query, args...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("生命周期约束错误=%v want SQLSTATE 23514", err)
	}
}
