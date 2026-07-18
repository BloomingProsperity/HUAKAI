//go:build integration_pg

package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestPostgresCredentialVaultWithStore_RevokedV2DoesNotFallbackToLegacy(t *testing.T) {
	ctx := context.Background()
	suffix := "v2-revoked-no-legacy"
	f := setupFixture(ctx, t, suffix)
	registerFixtureCleanup(t, &f)

	legacyKey := "sk-legacy-bypass"
	f.providerAccountID = insertProviderAccount(ctx, t, testDB,
		f.tenantID, f.providerID, f.channelID,
		"test-account-"+suffix, "api_key", true,
		map[string]string{"api_key": legacyKey})
	store := newPostgresVaultCredentialStore(t)
	meta := createPostgresVaultAPIKeyCredential(t, ctx, store, f, "sk-v2-before-revoke")
	if err := store.SetState(ctx, f.tenantID, f.providerAccountID, meta.ID, credentialstore.StateRevoked, "owner"); err != nil {
		t.Fatalf("SetState revoked: %v", err)
	}

	vault := NewPostgresCredentialVaultWithStore(testDB, store)
	cred, _, err := vault.Resolve(ctx, f.tenantID, f.providerAccountID)
	if err == nil {
		t.Fatalf("Resolve returned legacy credential %q after v2 revoke; want fail-closed error", cred.Value)
	}
	if !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("Resolve err=%v, want %v", err, ErrAccountDisabled)
	}
	if cred.Value == legacyKey {
		t.Fatal("Resolve returned legacy credential after revoked v2 credential")
	}
}

func TestPostgresCredentialVaultWithStore_LegacyFallbackWhenNoV2Rows(t *testing.T) {
	ctx := context.Background()
	suffix := "no-v2-legacy"
	f := setupFixture(ctx, t, suffix)
	registerFixtureCleanup(t, &f)

	legacyKey := "sk-legacy-still-valid"
	f.providerAccountID = insertProviderAccount(ctx, t, testDB,
		f.tenantID, f.providerID, f.channelID,
		"test-account-"+suffix, "api_key", true,
		map[string]string{"api_key": legacyKey})
	vault := NewPostgresCredentialVaultWithStore(testDB, newPostgresVaultCredentialStore(t))

	cred, info, err := vault.Resolve(ctx, f.tenantID, f.providerAccountID)
	if err != nil {
		t.Fatalf("Resolve legacy fallback: %v", err)
	}
	if cred.Value != legacyKey {
		t.Fatalf("credential value=%q, want legacy %q", cred.Value, legacyKey)
	}
	if info.AccountCredentialID != 0 {
		t.Fatalf("AccountCredentialID=%d, want 0 for legacy fallback", info.AccountCredentialID)
	}
}

func TestPostgresCredentialVaultWithStore_ActiveV2OverridesLegacy(t *testing.T) {
	ctx := context.Background()
	suffix := "active-v2"
	f := setupFixture(ctx, t, suffix)
	registerFixtureCleanup(t, &f)

	f.providerAccountID = insertProviderAccount(ctx, t, testDB,
		f.tenantID, f.providerID, f.channelID,
		"test-account-"+suffix, "api_key", true,
		map[string]string{"api_key": "sk-legacy-should-not-use"})
	store := newPostgresVaultCredentialStore(t)
	meta := createPostgresVaultAPIKeyCredential(t, ctx, store, f, "sk-v2-active")
	vault := NewPostgresCredentialVaultWithStore(testDB, store)

	cred, info, err := vault.Resolve(ctx, f.tenantID, f.providerAccountID)
	if err != nil {
		t.Fatalf("Resolve active v2: %v", err)
	}
	if cred.Value != "sk-v2-active" {
		t.Fatalf("credential value=%q, want active v2", cred.Value)
	}
	if info.AccountCredentialID != meta.ID {
		t.Fatalf("AccountCredentialID=%d, want v2 credential id %d", info.AccountCredentialID, meta.ID)
	}
}

// TestPostgresCredentialVaultWithStore_ExternalAccountIDProjectedToAccountInfo 是
// R7 身份改写穿线的【数据穿透】守卫(测试 D):凭据行的 external_account_id 列
// (迁移 0141)经 ResolveActive → resolveFromStore 必须填进 AccountInfo.ExternalAccountID,
// 供 R7 身份改写把它投影进 metadata.user_id。
//
// 变异证伪:把 resolveFromStore 里 ExternalAccountID 的赋值删掉(穿线丢字段)→
// info.ExternalAccountID 退回空串 → 下面断言变红。本测试构造 external id 非空且与
// 任何零值/默认值不同,确保"丢字段"必被发现(discriminating fixture)。
func TestPostgresCredentialVaultWithStore_ExternalAccountIDProjectedToAccountInfo(t *testing.T) {
	ctx := context.Background()
	suffix := "ext-acct-id-projected"
	f := setupFixture(ctx, t, suffix)
	registerFixtureCleanup(t, &f)

	f.providerAccountID = insertProviderAccount(ctx, t, testDB,
		f.tenantID, f.providerID, f.channelID,
		"test-account-"+suffix, "api_key", true,
		map[string]string{"api_key": "sk-legacy-ignored"})
	store := newPostgresVaultCredentialStore(t)

	const wantExternalID = "acc-xyz-from-credential-row"
	_, err := store.Create(ctx, credentialstore.CreateCredentialInput{
		TenantID:          f.tenantID,
		ProviderAccountID: f.providerAccountID,
		Vendor:            credentialstore.VendorOpenAI,
		AuthMode:          credentialstore.AuthModeAPIKey,
		Payload:           []byte(`{"api_key":"sk-v2-active"}`),
		ActorID:           "owner",
		ExternalAccountID: wantExternalID,
	})
	if err != nil {
		t.Fatalf("Create v2 credential with external id: %v", err)
	}

	vault := NewPostgresCredentialVaultWithStore(testDB, store)
	_, info, err := vault.Resolve(ctx, f.tenantID, f.providerAccountID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info.ExternalAccountID != wantExternalID {
		t.Fatalf("AccountInfo.ExternalAccountID=%q, want %q —— 凭据行 external_account_id 未穿到 AccountInfo",
			info.ExternalAccountID, wantExternalID)
	}
}

// TestPostgresCredentialVaultWithStore_NoExternalAccountIDStaysEmpty 验证:凭据行
// 未提取到 external_account_id(NULL)时,AccountInfo.ExternalAccountID 为空串
// (= 下游 fail-open 不改写)。守 derefString 对 nil 的处理。
//
// 变异证伪:把 derefString 改成对 nil panic 或返回某占位非空值 → 本测试 panic/红。
func TestPostgresCredentialVaultWithStore_NoExternalAccountIDStaysEmpty(t *testing.T) {
	ctx := context.Background()
	suffix := "no-ext-acct-id"
	f := setupFixture(ctx, t, suffix)
	registerFixtureCleanup(t, &f)

	f.providerAccountID = insertProviderAccount(ctx, t, testDB,
		f.tenantID, f.providerID, f.channelID,
		"test-account-"+suffix, "api_key", true,
		map[string]string{"api_key": "sk-legacy-ignored"})
	store := newPostgresVaultCredentialStore(t)
	// 不传 ExternalAccountID → 列存 NULL。
	createPostgresVaultAPIKeyCredential(t, ctx, store, f, "sk-v2-active")

	vault := NewPostgresCredentialVaultWithStore(testDB, store)
	_, info, err := vault.Resolve(ctx, f.tenantID, f.providerAccountID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info.ExternalAccountID != "" {
		t.Fatalf("未提取到 external id 时 AccountInfo.ExternalAccountID 应为空,实际 %q", info.ExternalAccountID)
	}
}

func newPostgresVaultCredentialStore(t *testing.T) *credentialstore.Store {
	t.Helper()
	return credentialstore.NewStore(testDB, mustVaultTestKeyProvider(t), credentialstore.DefaultHandlerRegistry())
}

func createPostgresVaultAPIKeyCredential(
	t *testing.T,
	ctx context.Context,
	store *credentialstore.Store,
	f testFixture,
	apiKey string,
) credentialstore.CredentialMetadata {
	t.Helper()
	meta, err := store.Create(ctx, credentialstore.CreateCredentialInput{
		TenantID:          f.tenantID,
		ProviderAccountID: f.providerAccountID,
		Vendor:            credentialstore.VendorOpenAI,
		AuthMode:          credentialstore.AuthModeAPIKey,
		Payload:           []byte(`{"api_key":"` + apiKey + `"}`),
		ActorID:           "owner",
	})
	if err != nil {
		t.Fatalf("Create v2 credential: %v", err)
	}
	return meta
}
