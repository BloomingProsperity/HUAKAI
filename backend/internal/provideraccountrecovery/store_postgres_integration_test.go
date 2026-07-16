//go:build integration_pg

package provideraccountrecovery

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestPostgresStoreClearRateLimitAndAuditAreAtomic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openProviderAccountRecoveryPool(t, ctx)
	tenantID, accountID := seedProviderAccountRecoveryAccount(t, ctx, pool)
	t.Cleanup(func() { cleanupProviderAccountRecoveryTenant(t, pool, tenantID) })

	resetAt := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `
UPDATE provider_accounts
SET rate_limited_at = NOW(),
    rate_limit_reset_at = $3,
    rate_limit_reason = 'rate_limit_rpm',
    model_rate_limits = '{"gpt-x":1}'::jsonb
WHERE tenant_id = $1 AND id = $2`, tenantID, accountID, resetAt); err != nil {
		t.Fatalf("seed rate limit: %v", err)
	}

	actorID := "admin:atomic-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	requestID := actorID + "-request"
	reason := "原子清理测试"
	store := NewPostgresStore(pool)
	invalid := AccountMutation{
		Clear: admindb.ClearProviderAccountRateLimitParams{
			ID: accountID, TenantID: tenantID, ActorID: &actorID,
		},
		Audit: admindb.InsertAdminAuditEventParams{
			TenantID: &tenantID, ActorID: actorID, ActorRole: "platform_admin",
			Action: "invalid_clear_action", TargetType: "provider_account", TargetID: &accountID,
			RequestID: &requestID, Reason: &reason, Payload: []byte(`{"cleared":true}`),
		},
	}
	if _, err := store.ClearRateLimitWithAudit(ctx, invalid); err == nil {
		t.Fatal("invalid audit action must roll back the account update")
	}
	var persistedReset *time.Time
	if err := pool.QueryRow(ctx, `
SELECT rate_limit_reset_at
FROM provider_accounts
WHERE tenant_id = $1 AND id = $2`, tenantID, accountID).Scan(&persistedReset); err != nil {
		t.Fatalf("read rolled back account: %v", err)
	}
	if persistedReset == nil || !persistedReset.Equal(resetAt) {
		t.Fatalf("account update was not rolled back: reset=%v want %s", persistedReset, resetAt)
	}

	valid := invalid
	valid.Audit.Action = clearRateLimitAuditAction
	account, err := store.ClearRateLimitWithAudit(ctx, valid)
	if err != nil {
		t.Fatalf("ClearRateLimitWithAudit: %v", err)
	}
	if account.ID != accountID || account.RateLimitResetAt.Valid || account.RateLimitReason != nil {
		t.Fatalf("cleared account mismatch: %+v", account)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM admin_audit_events
WHERE tenant_id = $1
  AND target_type = 'provider_account'
  AND target_id = $2
  AND action = $3`, tenantID, accountID, clearRateLimitAuditAction).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count=%d want 1", auditCount)
	}
}

func openProviderAccountRecoveryPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedProviderAccountRecoveryAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, int64) {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var tenantID, providerID, poolGroupID, channelID, accountID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "recovery-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
VALUES ($1, $2, 'Recovery Provider', 'openai_chat')
RETURNING id`, tenantID, "recovery-"+suffix).Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO pool_groups (tenant_id, name)
VALUES ($1, $2)
RETURNING id`, tenantID, "recovery-pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("insert pool group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO channels (tenant_id, pool_group_id, name)
VALUES ($1, $2, $3)
RETURNING id`, tenantID, poolGroupID, "recovery-channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO provider_accounts (
    tenant_id, provider_id, channel_id, name, account_type, enabled, health_state
) VALUES ($1, $2, $3, $4, 'api_key', true, 'healthy')
RETURNING id`, tenantID, providerID, channelID, "recovery-account-"+suffix).Scan(&accountID); err != nil {
		t.Fatalf("insert provider account: %v", err)
	}
	return tenantID, accountID
}

func cleanupProviderAccountRecoveryTenant(t *testing.T, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, statement := range []string{
		`DELETE FROM admin_audit_events WHERE tenant_id = $1`,
		`DELETE FROM provider_accounts WHERE tenant_id = $1`,
		`DELETE FROM channels WHERE tenant_id = $1`,
		`DELETE FROM pool_groups WHERE tenant_id = $1`,
		`DELETE FROM providers WHERE tenant_id = $1`,
		`DELETE FROM tenants WHERE id = $1`,
	} {
		if _, err := pool.Exec(ctx, statement, tenantID); err != nil {
			t.Errorf("cleanup %q: %v", statement, err)
		}
	}
}
