package credentialstore

import (
	"context"
	"fmt"
	"strings"
)

// CredentialIdentityMetadata 是账号接入预检可读取的无秘密 inventory。
type CredentialIdentityMetadata struct {
	ProviderAccountID             int64
	ProviderAccountName           string
	Vendor                        string
	AuthMode                      string
	State                         string
	ExternalAccountID             string
	ExternalSubjectID             string
	ExternalAccountEmail          string
	ExternalIdentitySource        string
	CredentialMaterialFingerprint string
}

// ListIdentityInventory 返回租户范围内的账号身份和单向指纹，不读取加密 payload。
func (s *Store) ListIdentityInventory(ctx context.Context, tenantID int64, vendor string) ([]CredentialIdentityMetadata, error) {
	if err := s.validateReady(); err != nil {
		return nil, err
	}
	if tenantID <= 0 {
		return nil, fmt.Errorf("%w: tenantID required", ErrInvalidPayload)
	}
	vendor = Normalize(vendor)
	const q = `
WITH current_credentials AS (
    SELECT DISTINCT ON (ac.provider_account_id, ac.vendor, ac.auth_mode)
           pa.id AS provider_account_id,
           pa.name AS provider_account_name,
           ac.vendor,
           ac.auth_mode,
           ac.state,
           COALESCE(ac.external_account_id, '') AS external_account_id,
           COALESCE(ac.external_subject_id, '') AS external_subject_id,
           COALESCE(ac.external_account_email, '') AS external_account_email,
           COALESCE(ac.external_identity_source, '') AS external_identity_source,
           COALESCE(ac.credential_material_fingerprint, '') AS credential_material_fingerprint
    FROM account_credentials ac
    JOIN provider_accounts pa
      ON pa.id = ac.provider_account_id
     AND pa.tenant_id = ac.tenant_id
    WHERE ac.tenant_id = $1
      AND pa.tenant_id = $1
      AND ac.deleted_at IS NULL
      AND pa.deleted_at IS NULL
      AND ($2::text = '' OR ac.vendor = $2::text)
    ORDER BY ac.provider_account_id, ac.vendor, ac.auth_mode,
             CASE ac.state
                 WHEN 'active' THEN 0
                 WHEN 'refreshing_with_grace' THEN 1
                 WHEN 'refreshing' THEN 2
                 WHEN 'temp_unschedulable' THEN 3
                 WHEN 'needs_rotation' THEN 4
                 WHEN 'operator_attention' THEN 5
                 WHEN 'expired' THEN 6
                 WHEN 'revoked' THEN 7
                 ELSE 8
             END,
             ac.updated_at DESC,
             ac.id DESC
)
SELECT provider_account_id, provider_account_name, vendor, auth_mode, state,
       external_account_id, external_subject_id, external_account_email,
       external_identity_source, credential_material_fingerprint
FROM current_credentials
ORDER BY provider_account_id, vendor, auth_mode`
	rows, err := s.db.Query(ctx, q, tenantID, vendor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CredentialIdentityMetadata
	for rows.Next() {
		var item CredentialIdentityMetadata
		if err := rows.Scan(
			&item.ProviderAccountID, &item.ProviderAccountName,
			&item.Vendor, &item.AuthMode, &item.State,
			&item.ExternalAccountID, &item.ExternalSubjectID,
			&item.ExternalAccountEmail, &item.ExternalIdentitySource,
			&item.CredentialMaterialFingerprint,
		); err != nil {
			return nil, err
		}
		item.ProviderAccountName = strings.TrimSpace(item.ProviderAccountName)
		out = append(out, item)
	}
	return out, rows.Err()
}
