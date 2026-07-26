//go:build integration_pg

package accountfphttp

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

func TestFingerprintBindingAndLogAreAtomic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Fatal("integration_pg 必须显式设置 HUAKAI_DATABASE_URL")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 PostgreSQL: %v", err)
	}
	defer pool.Close()

	tenantID, accountID, profileID := seedFingerprintBinding(t, ctx, pool)
	store := NewPostgresStore(pool)
	audit := fingerprintAudit(tenantID, accountID, "invalid_action")
	if err := store.UpdateFingerprintProfileWithAudit(ctx, admindb.UpdateProviderAccountFingerprintProfileParams{
		TenantID: tenantID, ID: accountID, ProfileID: &profileID,
	}, audit); err == nil {
		t.Fatal("日志约束拒绝时指纹绑定必须失败")
	}
	assertFingerprintBinding(t, ctx, pool, tenantID, accountID, nil)

	audit = fingerprintAudit(tenantID, accountID, "update_provider_account")
	if err := store.UpdateFingerprintProfileWithAudit(ctx, admindb.UpdateProviderAccountFingerprintProfileParams{
		TenantID: tenantID, ID: accountID, ProfileID: &profileID,
	}, audit); err != nil {
		t.Fatalf("指纹绑定正向提交失败: %v", err)
	}
	assertFingerprintBinding(t, ctx, pool, tenantID, accountID, &profileID)

	var logCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM admin_audit_events
WHERE tenant_id=$1 AND target_id=$2 AND action='update_provider_account'`,
		tenantID, accountID,
	).Scan(&logCount); err != nil {
		t.Fatalf("读取指纹绑定日志: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("指纹绑定与日志应同时提交，日志数=%d", logCount)
	}

	err = store.UpdateFingerprintProfileWithAudit(ctx, admindb.UpdateProviderAccountFingerprintProfileParams{
		TenantID: tenantID, ID: accountID + 1_000_000, ProfileID: nil,
	}, fingerprintAudit(tenantID, accountID+1_000_000, "update_provider_account"))
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("不存在账号应返回 pgx.ErrNoRows，实际 %v", err)
	}
}

func seedFingerprintBinding(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, int64, int64) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var tenantID, providerID, poolID, channelID, accountID, profileID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants(name) VALUES($1) RETURNING id`, "fp-tx-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO providers(tenant_id,code,display_name,upstream_protocol)
VALUES($1,$2,$3,'openai_chat') RETURNING id`,
		tenantID, "fp-provider-"+suffix, "Provider "+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("插入 provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups(tenant_id,name) VALUES($1,$2) RETURNING id`,
		tenantID, "fp-pool-"+suffix,
	).Scan(&poolID); err != nil {
		t.Fatalf("插入池: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels(tenant_id,pool_group_id,name) VALUES($1,$2,$3) RETURNING id`,
		tenantID, poolID, "fp-channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("插入渠道: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO provider_accounts(tenant_id,provider_id,channel_id,name,account_type)
VALUES($1,$2,$3,$4,'api_key') RETURNING id`,
		tenantID, providerID, channelID, "fp-account-"+suffix,
	).Scan(&accountID); err != nil {
		t.Fatalf("插入账号: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO tls_fingerprint_profiles(tenant_id,name)
VALUES($1,$2) RETURNING id`,
		tenantID, "fp-profile-"+suffix,
	).Scan(&profileID); err != nil {
		t.Fatalf("插入指纹档案: %v", err)
	}
	return tenantID, accountID, profileID
}

func fingerprintAudit(tenantID, accountID int64, action string) admindb.InsertAdminAuditEventParams {
	return admindb.InsertAdminAuditEventParams{
		TenantID:   &tenantID,
		ActorID:    "admin_token:305",
		ActorRole:  admin.RolePlatformAdmin,
		Action:     action,
		TargetType: "provider_account",
		TargetID:   &accountID,
		Payload:    []byte(`{"op":"fingerprint_profile"}`),
	}
}

func assertFingerprintBinding(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, accountID int64, want *int64) {
	t.Helper()
	var got *int64
	if err := pool.QueryRow(ctx, `
SELECT tls_fingerprint_profile_id
FROM provider_accounts
WHERE tenant_id=$1 AND id=$2`,
		tenantID, accountID,
	).Scan(&got); err != nil {
		t.Fatalf("读取指纹绑定: %v", err)
	}
	if (got == nil) != (want == nil) || got != nil && *got != *want {
		t.Fatalf("指纹绑定=%v，期望 %v", got, want)
	}
}
