package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

// resolveFromStore 优先从 v2 凭据仓库解析账号；找不到 v2 行时允许调用方回退旧表。
func (v *PostgresCredentialVault) resolveFromStore(
	ctx context.Context,
	tenantID, accountID int64,
) (Credential, AccountInfo, bool, error) {
	rec, err := v.store.ResolveActive(ctx, tenantID, accountID)
	if err != nil {
		if errors.Is(err, credentialstore.ErrCredentialNotActive) {
			return Credential{}, AccountInfo{}, true, fmt.Errorf("account %d: %w", accountID, ErrAccountDisabled)
		}
		if errors.Is(err, credentialstore.ErrCredentialNotFound) {
			return Credential{}, AccountInfo{}, false, nil
		}
		return Credential{}, AccountInfo{}, true, err
	}
	defer privacy.Zeroize(rec.PlaintextPayload)
	handler, err := v.store.HandlerRegistry().MustLookup(rec.Vendor, rec.AuthMode)
	if err != nil {
		return Credential{}, AccountInfo{}, true, err
	}
	if handler.RuntimeKind() == credentialstore.RuntimeCodexAgentIdentity {
		return v.resolveDynamicStoreCredential(ctx, rec, tenantID, accountID)
	}
	material, err := handler.RuntimeMaterial(rec.PlaintextPayload)
	if err != nil {
		return Credential{}, AccountInfo{}, true, err
	}
	cred := mapRuntimeMaterial(material)
	accountExtra, err := v.loadProviderAccountExtra(ctx, tenantID, accountID)
	if err != nil {
		return Credential{}, AccountInfo{}, true, err
	}
	cred = mergeCredentialAccountExtra(cred, accountExtra)
	return cred, AccountInfo{
		AccountID: rec.ProviderAccountID, TenantID: rec.TenantID,
		OAuthScope: strings.TrimSpace(material.Extra["scope"]),
		Platform:   rec.Vendor, AccountType: rec.AuthMode,
		CodexCLIOnly:        codexCLIOnlyFromAccountExtra(accountExtra),
		AccountCredentialID: rec.ID, CredentialVersion: int(rec.CredentialVersion),
		ExternalAccountID: derefString(rec.ExternalAccountID),
	}, true, nil
}

func (v *PostgresCredentialVault) resolveDynamicStoreCredential(
	ctx context.Context,
	rec credentialstore.CredentialRecord,
	tenantID, accountID int64,
) (Credential, AccountInfo, bool, error) {
	if v.dynamicResolver == nil {
		return Credential{}, AccountInfo{}, true, fmt.Errorf("provider vault: dynamic credential resolver 未配置")
	}
	cred, err := v.dynamicResolver.ResolveDynamicCredential(ctx, DynamicCredentialInput{
		TenantID: rec.TenantID, ProviderAccountID: rec.ProviderAccountID,
		AccountCredentialID: rec.ID, CredentialVersion: rec.CredentialVersion,
		Vendor: rec.Vendor, AuthMode: rec.AuthMode, Payload: rec.PlaintextPayload,
	})
	if err != nil {
		return Credential{}, AccountInfo{}, true, err
	}
	accountExtra, err := v.loadProviderAccountExtra(ctx, tenantID, accountID)
	if err != nil {
		return Credential{}, AccountInfo{}, true, err
	}
	cred = mergeCredentialAccountExtra(cred, accountExtra)
	return cred, AccountInfo{
		AccountID: rec.ProviderAccountID, TenantID: rec.TenantID,
		Platform: rec.Vendor, AccountType: rec.AuthMode,
		CodexCLIOnly:        codexCLIOnlyFromAccountExtra(accountExtra),
		AccountCredentialID: rec.ID, CredentialVersion: int(rec.CredentialVersion),
		ExternalAccountID: derefString(rec.ExternalAccountID),
	}, true, nil
}

func (v *PostgresCredentialVault) loadProviderAccountExtra(ctx context.Context, tenantID, accountID int64) (map[string]string, error) {
	if v == nil || v.pool == nil {
		return nil, nil
	}
	var raw []byte
	err := v.pool.QueryRow(ctx, `
SELECT extra
FROM provider_accounts
WHERE tenant_id = $1
  AND id = $2
  AND deleted_at IS NULL
LIMIT 1`, tenantID, accountID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("account %d: %w", accountID, ErrAccountNotFound)
		}
		return nil, err
	}
	return decodeProviderAccountExtra(raw), nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
