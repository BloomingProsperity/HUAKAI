//go:build integration_pg

package accountmodeldiscovery

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

func TestSyncPersistsCatalogAndRejectsRotatedCredential(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := openModelDiscoveryIntegrationPool(t, ctx)
	tenantID, accountID, credentialID := seedModelDiscoveryAccount(t, ctx, pool)

	vault := stubVault{
		credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		account: provider.AccountInfo{
			AccountID: accountID, TenantID: tenantID,
			Platform: credentialstore.VendorOpenAI, AccountType: credentialstore.AuthModeAPIKey,
			AccountCredentialID: credentialID, CredentialVersion: 1,
		},
	}
	dispatcher := &queuedDispatcher{responses: []*gateway.DispatchResult{
		response(http.StatusOK, `{"data":[{"id":"gpt-b"},{"id":"gpt-a"}]}`),
		response(http.StatusOK, `{"data":[{"id":"gpt-a"},{"id":"gpt-b"}]}`),
		response(http.StatusOK, `{"data":[{"id":"gpt-c"}]}`),
	}}
	service := NewService(vault, dispatcher, pool)
	input := SyncInput{
		TenantID: tenantID, AccountID: accountID,
		ActorID: "admin_token:9", ActorRole: admin.RoleTenantOperator,
		RequestID: "req-model-sync-1", Reason: "刷新账号模型目录",
	}

	first, err := service.Sync(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || first.PreviousCount != 0 || strings.Join(first.ModelIDs(), ",") != "gpt-a,gpt-b" {
		t.Fatalf("首次同步结果=%+v", first)
	}
	assertStoredModelIDs(t, ctx, pool, tenantID, accountID, []string{"gpt-a", "gpt-b"})

	input.RequestID = "req-model-sync-2"
	second, err := service.Sync(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || second.PreviousCount != 2 {
		t.Fatalf("重复同步结果=%+v", second)
	}
	var logCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int
FROM admin_audit_events
WHERE tenant_id=$1 AND target_type='provider_account' AND target_id=$2
  AND payload->>'operation'='sync_account_models'`, tenantID, accountID).Scan(&logCount); err != nil {
		t.Fatal(err)
	}
	if logCount != 2 {
		t.Fatalf("模型同步日志数=%d，期望首次更新和重复确认各一条", logCount)
	}

	if _, err := pool.Exec(ctx, `
UPDATE account_credentials
SET state='revoked', updated_at=NOW()
WHERE tenant_id=$1 AND provider_account_id=$2 AND id=$3`, tenantID, accountID, credentialID); err != nil {
		t.Fatal(err)
	}
	input.RequestID = "req-model-sync-stale"
	_, syncErr := service.Sync(ctx, input)
	if KindOf(syncErr) != ErrorCredentialChanged {
		t.Fatalf("凭据轮换后的同步 err=%v，期望 credential_changed", syncErr)
	}
	var rotationErr *DiscoveryError
	if !errors.As(syncErr, &rotationErr) || rotationErr.Vendor == "" || rotationErr.AuthMode == "" {
		t.Fatalf("同步失败必须回填账号族供失败日志辨识: %+v", rotationErr)
	}
	assertStoredModelIDs(t, ctx, pool, tenantID, accountID, []string{"gpt-a", "gpt-b"})
}

func seedModelDiscoveryAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, int64, int64) {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	var tenantID, providerID, poolGroupID, channelID, accountID, credentialID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "model-discovery-"+suffix).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
VALUES ($1,$2,$3,'openai_chat') RETURNING id`, tenantID, "openai-"+suffix, "OpenAI "+suffix).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`, tenantID, "pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`, tenantID, poolGroupID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type)
VALUES ($1,$2,$3,$4,'api_key') RETURNING id`, tenantID, providerID, channelID, "account-"+suffix).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO account_credentials (
    tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
    encrypted_payload, key_id, nonce, aad_hash
) VALUES ($1,$2,'openai','api_key','active',1,$3,'integration-key',$4,$5)
RETURNING id`, tenantID, accountID, []byte{1}, []byte{2}, "integration-aad").Scan(&credentialID); err != nil {
		t.Fatal(err)
	}
	return tenantID, accountID, credentialID
}

func assertStoredModelIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, accountID int64, want []string) {
	t.Helper()
	var got []string
	if err := pool.QueryRow(ctx, `
SELECT model_allow_list FROM provider_accounts WHERE tenant_id=$1 AND id=$2`, tenantID, accountID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("落库模型=%v，期望 %v", got, want)
	}
}

func openModelDiscoveryIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("HUAKAI_TEST_DATABASE_URL"))
	if baseDSN == "" {
		baseDSN = strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
	}
	if baseDSN == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL/HUAKAI_DATABASE_URL，跳过 integration_pg")
	}
	adminConn, err := pgx.Connect(ctx, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := "huakai_model_discovery_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+quoteModelDiscoveryIdentifier(databaseName)); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatal(err)
	}
	if err := adminConn.Close(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.Background(), baseDSN)
		if err != nil {
			t.Logf("连接维护库清理临时数据库失败：%v", err)
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", databaseName)
		if _, err := cleanup.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quoteModelDiscoveryIdentifier(databaseName)); err != nil {
			t.Logf("删除临时数据库失败：%v", err)
		}
	})
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	testDSN := parsed.String()
	if err := dbmigrate.Up(sqlmigrations.Files, testDSN); err != nil {
		t.Fatalf("迁移临时数据库失败：%v", err)
	}
	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func quoteModelDiscoveryIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
