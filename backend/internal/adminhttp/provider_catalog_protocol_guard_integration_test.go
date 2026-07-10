//go:build integration_pg

package adminhttp

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// TestUpdateProviderProtocolRejectsIncompatibleExistingAccounts 咬住 S1-5 的完整不变量:
// 把带 api_key 账号的 provider 协议改成 session 族必须被拒(api_key 与 session 不兼容),
// 且协议不落库(事务回滚)。改成兼容的非 session 协议应成功。
// 变异:去掉 UpdateProviderCatalogWithAudit 里的 ensureAccountsCompatibleWithProtocol
// 调用 → 改 session 成功、协议落库,本测试两处断言红。
func TestUpdateProviderProtocolRejectsIncompatibleExistingAccounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAdminIntegrationPool(t, ctx)

	tenantID, providerCode := seedProviderWithAPIKeyAccount(t, ctx, pool)
	store := NewProviderCatalogStoreAdapter(admindb.New(pool), pool)
	audit := admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorRole: "platform_admin", Action: "update_provider", TargetType: "provider", Payload: []byte("{}"),
	}

	// 负例:改成 session 族 → 被拒,协议保持 openai_chat。
	_, err := store.UpdateProviderCatalogWithAudit(ctx, providerCatalogUpdateParams{
		TenantID: tenantID, Code: providerCode, DisplayName: "x",
		UpstreamProtocol: "anthropic_claude_session", Enabled: true,
	}, audit)
	if !errors.Is(err, errProviderProtocolChangeIncompatible) {
		t.Fatalf("改 session 族应被存量 api_key 账号拒,却 err=%v", err)
	}
	if got := currentProtocol(t, ctx, pool, tenantID, providerCode); got != "openai_chat" {
		t.Fatalf("被拒后协议不得落库,却=%q", got)
	}

	// 正例:改成兼容的非 session 协议 → 成功。
	if _, err := store.UpdateProviderCatalogWithAudit(ctx, providerCatalogUpdateParams{
		TenantID: tenantID, Code: providerCode, DisplayName: "x",
		UpstreamProtocol: "openai_responses", Enabled: true,
	}, audit); err != nil {
		t.Fatalf("改兼容协议应成功: %v", err)
	}
	if got := currentProtocol(t, ctx, pool, tenantID, providerCode); got != "openai_responses" {
		t.Fatalf("兼容协议应落库,却=%q", got)
	}
}

// TestUpdateProviderProtocolChecksEveryActiveCredential 咬住 S1-5 的多凭据遮蔽绕过:
// 一个 oauth 账号同时有"较新的兼容凭据"和"较旧的不兼容凭据"时,改成 session 族仍须被拒
// (不能被较新兼容凭据遮蔽)。account_type=oauth 使 account_type 校验通过,故拒绝只能来自
// 那条不兼容凭据 → 证守卫逐条校验每一条活跃凭据、且真的读了 vendor/auth。
// 变异:把 listProviderAccountsForProviderCompat 改回 LIMIT 1(按 version DESC 取较新兼容
// 那条)→ 遮蔽不兼容凭据 → 改 session 成功,本测试红。
func TestUpdateProviderProtocolChecksEveryActiveCredential(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAdminIntegrationPool(t, ctx)

	tenantID, providerCode, accountID := seedProviderWithOAuthAccount(t, ctx, pool)
	// 较旧:不兼容(openai/api_key 与 session 不符);较新:兼容(anthropic/claude_ai_oauth)。
	seedCredential(t, ctx, pool, tenantID, accountID, "openai", "api_key", 1)
	seedCredential(t, ctx, pool, tenantID, accountID, "anthropic", "claude_ai_oauth", 2)

	store := NewProviderCatalogStoreAdapter(admindb.New(pool), pool)
	audit := admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorRole: "platform_admin", Action: "update_provider", TargetType: "provider", Payload: []byte("{}"),
	}
	_, err := store.UpdateProviderCatalogWithAudit(ctx, providerCatalogUpdateParams{
		TenantID: tenantID, Code: providerCode, DisplayName: "x",
		UpstreamProtocol: "anthropic_claude_session", Enabled: true,
	}, audit)
	if !errors.Is(err, errProviderProtocolChangeIncompatible) {
		t.Fatalf("较旧的不兼容凭据不得被较新兼容凭据遮蔽,应拒,却 err=%v", err)
	}
	if got := currentProtocol(t, ctx, pool, tenantID, providerCode); got != "openai_chat" {
		t.Fatalf("被拒后协议不得落库,却=%q", got)
	}
}

func currentProtocol(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, code string) string {
	t.Helper()
	var p string
	if err := pool.QueryRow(ctx, `SELECT upstream_protocol FROM providers WHERE tenant_id=$1 AND code=$2 AND deleted_at IS NULL`, tenantID, code).Scan(&p); err != nil {
		t.Fatalf("读协议: %v", err)
	}
	return p
}

func seedProviderWithAPIKeyAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, string) {
	t.Helper()
	u := uuid.NewString()[:8]
	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "adm-guard-"+u).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	code := "guard-prov-" + u
	var providerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1,$2,$3,'openai_chat') RETURNING id`,
		tenantID, code, "Guard "+u,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`, tenantID, "pg-"+u).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	var channelID int64
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`, tenantID, poolGroupID, "ch-"+u).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, credentials, extra)
		 VALUES ($1,$2,$3,$4,'api_key','{}','{}')`,
		tenantID, providerID, channelID, "apikey-acct-"+u,
	); err != nil {
		t.Fatalf("seed api_key account: %v", err)
	}
	return tenantID, code
}

// seedProviderWithOAuthAccount 建 openai_chat provider + 一个 oauth 账号,返回账号 id。
func seedProviderWithOAuthAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, string, int64) {
	t.Helper()
	u := uuid.NewString()[:8]
	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "adm-guard2-"+u).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM account_credentials WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	code := "guard2-prov-" + u
	var providerID int64
	if err := pool.QueryRow(ctx, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1,$2,$3,'openai_chat') RETURNING id`, tenantID, code, "Guard2 "+u).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`, tenantID, "pg-"+u).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	var channelID int64
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`, tenantID, poolGroupID, "ch-"+u).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var accountID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, credentials, extra)
		 VALUES ($1,$2,$3,$4,'oauth','{}','{}') RETURNING id`,
		tenantID, providerID, channelID, "oauth-acct-"+u,
	).Scan(&accountID); err != nil {
		t.Fatalf("seed oauth account: %v", err)
	}
	return tenantID, code, accountID
}

// seedCredential 插一条活跃 account_credentials(守卫只读 vendor/auth_mode,加密字段填占位)。
func seedCredential(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, accountID int64, vendor, authMode string, version int) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO account_credentials
		   (tenant_id, provider_account_id, vendor, auth_mode, state, credential_version, encrypted_payload, key_id, nonce, aad_hash)
		 VALUES ($1,$2,$3,$4,'active',$5,'\x00','test-key','\x00','\x00')`,
		tenantID, accountID, vendor, authMode, version,
	); err != nil {
		t.Fatalf("seed credential %s/%s: %v", vendor, authMode, err)
	}
}

func openAdminIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置,跳过 integration_pg 测试")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开连接池: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}
