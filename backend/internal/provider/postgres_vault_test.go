//go:build integration_pg

// 集成测试：PostgresCredentialVault
//
// 策略 B：integration_pg 构建标签 + HUAKAI_DATABASE_URL 环境变量。
//
// 运行方式：
//
//	HUAKAI_DATABASE_URL="postgres://..." go test -tags=integration_pg ./...
//
// 若 HUAKAI_DATABASE_URL 未设置，所有测试自动跳过（t.Skip）。
// 测试在独立事务中运行，完成后回滚，不污染数据库状态。
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testDB 是测试套件共享的连接池；由 TestMain 初始化。
var testDB *pgxpool.Pool

// TestMain 初始化数据库连接池，若环境变量未设置则跳过所有测试。
func TestMain(m *testing.M) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		// 未配置数据库 URL，跳过整个测试套件。
		fmt.Println("跳过 integration_pg 测试：HUAKAI_DATABASE_URL 未设置")
		os.Exit(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var err error
	testDB, err = pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法连接数据库: %v\n", err)
		os.Exit(1)
	}
	if err := testDB.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "数据库 ping 失败: %v\n", err)
		os.Exit(1)
	}
	defer testDB.Close()

	os.Exit(m.Run())
}

// ---- 测试辅助函数 ------------------------------------------------------------

// testFixture 保存单个测试用例插入的行主键，供测试结束后清理。
type testFixture struct {
	tenantID          int64
	providerID        int64
	poolGroupID       int64
	channelID         int64
	providerAccountID int64
}

// insertTestTenant 插入一个租户行，返回其 id。
func insertTestTenant(ctx context.Context, t *testing.T, db *pgxpool.Pool, suffix string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"test-tenant-pgvault-"+suffix,
	).Scan(&id)
	if err != nil {
		t.Fatalf("插入测试租户失败: %v", err)
	}
	return id
}

// insertTestProvider 插入一个 provider 行，返回其 id。
func insertTestProvider(ctx context.Context, t *testing.T, db *pgxpool.Pool, tenantID int64, code, suffix string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		tenantID, code+"-"+suffix, "Test Provider "+suffix,
	).Scan(&id)
	if err != nil {
		t.Fatalf("插入测试 provider 失败: %v", err)
	}
	return id
}

// insertTestPoolGroup 插入 pool_group，返回 id。
func insertTestPoolGroup(ctx context.Context, t *testing.T, db *pgxpool.Pool, tenantID int64, suffix string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "test-pool-"+suffix,
	).Scan(&id)
	if err != nil {
		t.Fatalf("插入测试 pool_group 失败: %v", err)
	}
	return id
}

// insertTestChannel 插入 channel，返回 id。
func insertTestChannel(ctx context.Context, t *testing.T, db *pgxpool.Pool, tenantID, poolGroupID int64, suffix string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "test-channel-"+suffix,
	).Scan(&id)
	if err != nil {
		t.Fatalf("插入测试 channel 失败: %v", err)
	}
	return id
}

// insertProviderAccount 插入一条 provider_accounts 行，返回其 id。
func insertProviderAccount(
	ctx context.Context, t *testing.T, db *pgxpool.Pool,
	tenantID, providerID, channelID int64,
	name, accountType string,
	enabled bool,
	credentials interface{},
) int64 {
	t.Helper()
	credJSON, err := json.Marshal(credentials)
	if err != nil {
		t.Fatalf("序列化凭据失败: %v", err)
	}
	var id int64
	err = db.QueryRow(ctx,
		`INSERT INTO provider_accounts
		   (tenant_id, provider_id, channel_id, name, account_type, enabled, credentials)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		tenantID, providerID, channelID, name, accountType, enabled, credJSON,
	).Scan(&id)
	if err != nil {
		t.Fatalf("插入 provider_account 失败: %v", err)
	}
	return id
}

// cleanupFixture 删除测试插入的所有行（顺序：子表先删）。
func cleanupFixture(ctx context.Context, t *testing.T, db *pgxpool.Pool, f testFixture) {
	t.Helper()
	if f.tenantID != 0 {
		_, _ = db.Exec(ctx, `DELETE FROM credential_audit_events WHERE tenant_id = $1`, f.tenantID)
		_, _ = db.Exec(ctx, `DELETE FROM account_credentials WHERE tenant_id = $1`, f.tenantID)
	}
	if f.providerAccountID != 0 {
		_, _ = db.Exec(ctx, `DELETE FROM provider_accounts WHERE id = $1`, f.providerAccountID)
	}
	if f.channelID != 0 {
		_, _ = db.Exec(ctx, `DELETE FROM channels WHERE id = $1`, f.channelID)
	}
	if f.poolGroupID != 0 {
		_, _ = db.Exec(ctx, `DELETE FROM pool_groups WHERE id = $1`, f.poolGroupID)
	}
	if f.providerID != 0 {
		_, _ = db.Exec(ctx, `DELETE FROM providers WHERE id = $1`, f.providerID)
	}
	if f.tenantID != 0 {
		_, _ = db.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, f.tenantID)
	}
}

// setupFixture 创建一个独立的租户+provider+pool_group+channel，供单个测试使用。
func setupFixture(ctx context.Context, t *testing.T, suffix string) testFixture {
	t.Helper()
	var f testFixture
	f.tenantID = insertTestTenant(ctx, t, testDB, suffix)
	f.providerID = insertTestProvider(ctx, t, testDB, f.tenantID, "openai", suffix)
	f.poolGroupID = insertTestPoolGroup(ctx, t, testDB, f.tenantID, suffix)
	f.channelID = insertTestChannel(ctx, t, testDB, f.tenantID, f.poolGroupID, suffix)
	return f
}

// ---- 测试用例 ----------------------------------------------------------------

// TestPostgresCredentialVault_AccountNotFound 验证未知 accountID 返回 ErrAccountNotFound。
func TestPostgresCredentialVault_AccountNotFound(t *testing.T) {
	ctx := context.Background()
	vault := NewPostgresCredentialVault(testDB)

	// 使用不可能存在的负数 ID。tenantID 用 1 占位 (此用例只验账号不存在)。
	_, _, err := vault.Resolve(ctx, 1, -999999)
	if err == nil {
		t.Fatal("期望 ErrAccountNotFound，但得到 nil")
	}
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("期望 ErrAccountNotFound，但得到: %v", err)
	}
}

// TestPostgresCredentialVault_AccountDisabled 验证 enabled=false 返回 ErrAccountDisabled。
func TestPostgresCredentialVault_AccountDisabled(t *testing.T) {
	ctx := context.Background()
	suffix := "disabled"
	f := setupFixture(ctx, t, suffix)
	defer cleanupFixture(ctx, t, testDB, f)

	creds := map[string]string{"api_key": "sk-disabled-test"}
	f.providerAccountID = insertProviderAccount(ctx, t, testDB,
		f.tenantID, f.providerID, f.channelID,
		"test-account-"+suffix, "api_key", false, creds)

	vault := NewPostgresCredentialVault(testDB)
	_, _, err := vault.Resolve(ctx, f.tenantID, f.providerAccountID)
	if err == nil {
		t.Fatal("期望 ErrAccountDisabled，但得到 nil")
	}
	if !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("期望 ErrAccountDisabled，但得到: %v", err)
	}
}

// TestPostgresCredentialVault_APIKeyHappyPath 验证 api_key 类型凭据正确映射。
func TestPostgresCredentialVault_APIKeyHappyPath(t *testing.T) {
	ctx := context.Background()
	suffix := "apikey-happy"
	f := setupFixture(ctx, t, suffix)
	defer cleanupFixture(ctx, t, testDB, f)

	creds := map[string]interface{}{
		"api_key": "sk-test-key-12345",
		"extra": map[string]string{
			"org_id":     "org-abc",
			"project_id": "proj-xyz",
		},
	}
	f.providerAccountID = insertProviderAccount(ctx, t, testDB,
		f.tenantID, f.providerID, f.channelID,
		"test-account-"+suffix, "api_key", true, creds)

	vault := NewPostgresCredentialVault(testDB)
	cred, info, err := vault.Resolve(ctx, f.tenantID, f.providerAccountID)
	if err != nil {
		t.Fatalf("期望成功，但得到错误: %v", err)
	}

	// 验证凭据类型和主值。
	if cred.Type != CredentialTypeAPIKey {
		t.Errorf("期望 CredentialTypeAPIKey，得到 %q", cred.Type)
	}
	if cred.Value != "sk-test-key-12345" {
		t.Errorf("期望 Value='sk-test-key-12345'，得到 %q", cred.Value)
	}
	if cred.Extra["org_id"] != "org-abc" {
		t.Errorf("期望 Extra['org_id']='org-abc'，得到 %q", cred.Extra["org_id"])
	}
	if cred.Extra["project_id"] != "proj-xyz" {
		t.Errorf("期望 Extra['project_id']='proj-xyz'，得到 %q", cred.Extra["project_id"])
	}

	// 验证 AccountInfo。
	if info.AccountID != f.providerAccountID {
		t.Errorf("期望 AccountID=%d，得到 %d", f.providerAccountID, info.AccountID)
	}
	if info.AccountType != "api_key" {
		t.Errorf("期望 AccountType='api_key'，得到 %q", info.AccountType)
	}
	// Platform 应为 providers.code（插入时为 "openai-<suffix>"）。
	if info.Platform == "" {
		t.Error("期望 Platform 非空")
	}
}

// TestPostgresCredentialVault_OAuthHappyPath 验证 oauth 类型凭据正确映射。
func TestPostgresCredentialVault_OAuthHappyPath(t *testing.T) {
	ctx := context.Background()
	suffix := "oauth-happy"
	f := setupFixture(ctx, t, suffix)
	defer cleanupFixture(ctx, t, testDB, f)

	creds := map[string]string{
		"access_token":  "ya29.oauth-access-token",
		"refresh_token": "1//refresh-token-value",
		"expires_at":    "2026-12-31T23:59:59Z",
	}
	f.providerAccountID = insertProviderAccount(ctx, t, testDB,
		f.tenantID, f.providerID, f.channelID,
		"test-account-"+suffix, "oauth", true, creds)

	vault := NewPostgresCredentialVault(testDB)
	cred, _, err := vault.Resolve(ctx, f.tenantID, f.providerAccountID)
	if err != nil {
		t.Fatalf("期望成功，但得到错误: %v", err)
	}

	if cred.Type != CredentialTypeOAuthAccessToken {
		t.Errorf("期望 CredentialTypeOAuthAccessToken，得到 %q", cred.Type)
	}
	if cred.Value != "ya29.oauth-access-token" {
		t.Errorf("期望 Value='ya29.oauth-access-token'，得到 %q", cred.Value)
	}
	// refresh_token 必须出现在 Extra 中。
	if cred.Extra["refresh_token"] != "1//refresh-token-value" {
		t.Errorf("期望 Extra['refresh_token'] 正确，得到 %q", cred.Extra["refresh_token"])
	}
	// access_token 不应再出现在 Extra 中（已提升为 Value）。
	if _, ok := cred.Extra["access_token"]; ok {
		t.Error("access_token 不应出现在 Extra 中")
	}
}

// TestPostgresCredentialVault_ServiceAccountPath 验证 service_account 类型映射。
func TestPostgresCredentialVault_ServiceAccountPath(t *testing.T) {
	ctx := context.Background()
	suffix := "svcacct"
	f := setupFixture(ctx, t, suffix)
	defer cleanupFixture(ctx, t, testDB, f)

	creds := map[string]string{
		"client_email": "sa@project.iam.gserviceaccount.com",
		"private_key":  "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAK...\n-----END RSA PRIVATE KEY-----",
		"token_uri":    "https://oauth2.googleapis.com/token",
	}
	f.providerAccountID = insertProviderAccount(ctx, t, testDB,
		f.tenantID, f.providerID, f.channelID,
		"test-account-"+suffix, "service_account", true, creds)

	vault := NewPostgresCredentialVault(testDB)
	cred, _, err := vault.Resolve(ctx, f.tenantID, f.providerAccountID)
	if err != nil {
		t.Fatalf("期望成功，但得到错误: %v", err)
	}

	if cred.Type != CredentialTypeOAuthAccessToken {
		t.Errorf("期望 CredentialTypeOAuthAccessToken，得到 %q", cred.Type)
	}
	// auth_kind 必须为 google_sa。
	if cred.Extra["auth_kind"] != "google_sa" {
		t.Errorf("期望 Extra['auth_kind']='google_sa'，得到 %q", cred.Extra["auth_kind"])
	}
	if cred.Extra["client_email"] != "sa@project.iam.gserviceaccount.com" {
		t.Errorf("期望 client_email 正确，得到 %q", cred.Extra["client_email"])
	}
	if cred.Extra["private_key"] == "" {
		t.Error("期望 private_key 非空")
	}
}

// TestPostgresCredentialVault_UpstreamStaticPath 验证 upstream_static 类型映射。
func TestPostgresCredentialVault_UpstreamStaticPath(t *testing.T) {
	ctx := context.Background()
	suffix := "upstream"
	f := setupFixture(ctx, t, suffix)
	defer cleanupFixture(ctx, t, testDB, f)

	creds := map[string]string{
		"base_url":          "https://my-proxy.example.com/v1",
		"auth_header_value": "Bearer sk-proxy-key-abc",
	}
	f.providerAccountID = insertProviderAccount(ctx, t, testDB,
		f.tenantID, f.providerID, f.channelID,
		"test-account-"+suffix, "upstream_static", true, creds)

	vault := NewPostgresCredentialVault(testDB)
	cred, _, err := vault.Resolve(ctx, f.tenantID, f.providerAccountID)
	if err != nil {
		t.Fatalf("期望成功，但得到错误: %v", err)
	}

	if cred.Type != CredentialTypeUpstreamPassthrough {
		t.Errorf("期望 CredentialTypeUpstreamPassthrough，得到 %q", cred.Type)
	}
	if cred.Value != "Bearer sk-proxy-key-abc" {
		t.Errorf("期望 Value='Bearer sk-proxy-key-abc'，得到 %q", cred.Value)
	}
	if cred.Extra["base_url"] != "https://my-proxy.example.com/v1" {
		t.Errorf("期望 Extra['base_url'] 正确，得到 %q", cred.Extra["base_url"])
	}
}

// TestPostgresCredentialVault_MalformedCredentials 验证 JSONB 格式错误时返回 ErrCredentialFormat。
// 场景：account_type=api_key 但 credentials 中缺少 api_key 字段。
func TestPostgresCredentialVault_MalformedCredentials(t *testing.T) {
	ctx := context.Background()
	suffix := "malformed"
	f := setupFixture(ctx, t, suffix)
	defer cleanupFixture(ctx, t, testDB, f)

	// 故意省略 api_key 字段，只放一个无关字段。
	creds := map[string]string{
		"some_other_field": "should-not-be-here",
	}
	f.providerAccountID = insertProviderAccount(ctx, t, testDB,
		f.tenantID, f.providerID, f.channelID,
		"test-account-"+suffix, "api_key", true, creds)

	vault := NewPostgresCredentialVault(testDB)
	_, _, err := vault.Resolve(ctx, f.tenantID, f.providerAccountID)
	if err == nil {
		t.Fatal("期望 ErrCredentialFormat，但得到 nil")
	}
	if !errors.Is(err, ErrCredentialFormat) {
		t.Fatalf("期望 ErrCredentialFormat，但得到: %v", err)
	}
}
