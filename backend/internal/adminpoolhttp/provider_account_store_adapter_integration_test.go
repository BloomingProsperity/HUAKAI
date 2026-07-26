//go:build integration_pg

package adminpoolhttp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestProviderAccountMutationAndLogAreAtomic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openProviderAccountMutationPool(t, ctx)
	defer pool.Close()

	tenantID, accountID := seedProviderAccountMutation(t, ctx, pool)
	store := NewAdminPoolAccountStoreAdapter(admindb.New(pool), pool)
	invalidAudit := providerAccountMutationAudit(tenantID, accountID, "invalid_action")

	priority := int32(9)
	if _, err := store.UpdateAdminProviderAccountWithAudit(ctx, admindb.UpdateAdminProviderAccountParams{
		ID: accountID, TenantID: tenantID, Priority: &priority,
	}, invalidAudit); err == nil {
		t.Fatal("日志约束拒绝时账号更新必须失败")
	}
	assertProviderAccountMutationState(t, ctx, pool, tenantID, accountID, 100, true, false)

	if err := store.UpdateProviderAccountEnabledWithAudit(ctx, admindb.UpdateProviderAccountEnabledParams{
		ID: accountID, TenantID: tenantID, Enabled: false,
	}, invalidAudit); err == nil {
		t.Fatal("日志约束拒绝时账号停用必须失败")
	}
	assertProviderAccountMutationState(t, ctx, pool, tenantID, accountID, 100, true, false)

	if err := store.SoftDeleteProviderAccountWithAudit(ctx, admindb.SoftDeleteProviderAccountParams{
		ID: accountID, TenantID: tenantID,
	}, invalidAudit); err == nil {
		t.Fatal("日志约束拒绝时账号删除必须失败")
	}
	assertProviderAccountMutationState(t, ctx, pool, tenantID, accountID, 100, true, false)

	validAudit := providerAccountMutationAudit(tenantID, accountID, "update_provider_account")
	updated, err := store.UpdateAdminProviderAccountWithAudit(ctx, admindb.UpdateAdminProviderAccountParams{
		ID: accountID, TenantID: tenantID, Priority: &priority,
	}, validAudit)
	if err != nil {
		t.Fatalf("账号更新正向提交失败: %v", err)
	}
	if updated.Priority != priority {
		t.Fatalf("更新返回 priority=%d，期望 %d", updated.Priority, priority)
	}
	assertProviderAccountMutationState(t, ctx, pool, tenantID, accountID, priority, true, false)

	var logCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM admin_audit_events
WHERE tenant_id=$1 AND target_id=$2 AND action='update_provider_account'`,
		tenantID, accountID,
	).Scan(&logCount); err != nil {
		t.Fatalf("读取账号操作日志: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("账号更新与日志应同时提交，日志数=%d", logCount)
	}

	missingID := accountID + 1_000_000
	err = store.UpdateProviderAccountEnabledWithAudit(ctx, admindb.UpdateProviderAccountEnabledParams{
		ID: missingID, TenantID: tenantID, Enabled: false,
	}, providerAccountMutationAudit(tenantID, missingID, "disable_provider_account"))
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("不存在账号启停应返回 pgx.ErrNoRows，实际 %v", err)
	}
}

func openProviderAccountMutationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Fatal("integration_pg 必须显式设置 HUAKAI_DATABASE_URL")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 PostgreSQL: %v", err)
	}
	return pool
}

func seedProviderAccountMutation(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, int64) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var tenantID, providerID, poolID, channelID, accountID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants(name) VALUES($1) RETURNING id`, "pa-tx-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO providers(tenant_id, code, display_name, upstream_protocol)
VALUES($1,$2,$3,'openai_chat') RETURNING id`,
		tenantID, "pa-provider-"+suffix, "Provider "+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("插入 provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups(tenant_id,name) VALUES($1,$2) RETURNING id`,
		tenantID, "pa-pool-"+suffix,
	).Scan(&poolID); err != nil {
		t.Fatalf("插入池: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels(tenant_id,pool_group_id,name) VALUES($1,$2,$3) RETURNING id`,
		tenantID, poolID, "pa-channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("插入渠道: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO provider_accounts(tenant_id,provider_id,channel_id,name,account_type,priority,enabled)
VALUES($1,$2,$3,$4,'api_key',100,true) RETURNING id`,
		tenantID, providerID, channelID, "pa-account-"+suffix,
	).Scan(&accountID); err != nil {
		t.Fatalf("插入账号: %v", err)
	}
	return tenantID, accountID
}

func providerAccountMutationAudit(tenantID, accountID int64, action string) admindb.InsertAdminAuditEventParams {
	return admindb.InsertAdminAuditEventParams{
		TenantID:   &tenantID,
		ActorID:    "admin_token:305",
		ActorRole:  admin.RolePlatformAdmin,
		Action:     action,
		TargetType: "provider_account",
		TargetID:   &accountID,
		Payload:    []byte(`{"test":"atomic"}`),
	}
}

func assertProviderAccountMutationState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, accountID int64,
	priority int32,
	enabled, deleted bool,
) {
	t.Helper()
	var gotPriority int32
	var gotEnabled bool
	var gotDeleted bool
	if err := pool.QueryRow(ctx, `
SELECT priority, enabled, deleted_at IS NOT NULL
FROM provider_accounts
WHERE tenant_id=$1 AND id=$2`,
		tenantID, accountID,
	).Scan(&gotPriority, &gotEnabled, &gotDeleted); err != nil {
		t.Fatalf("读取账号状态: %v", err)
	}
	if gotPriority != priority || gotEnabled != enabled || gotDeleted != deleted {
		t.Fatalf("账号状态=(priority=%d enabled=%v deleted=%v)，期望 (%d %v %v)",
			gotPriority, gotEnabled, gotDeleted, priority, enabled, deleted)
	}
}
