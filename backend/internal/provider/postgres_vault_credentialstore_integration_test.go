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
	defer cleanupFixture(ctx, t, testDB, f)

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
	defer cleanupFixture(ctx, t, testDB, f)

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
	defer cleanupFixture(ctx, t, testDB, f)

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
