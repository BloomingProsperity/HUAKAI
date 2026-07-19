package credentialstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// PortableExportRecord 只供受控加密迁移包读取一条精确版本的凭据。
// PlaintextPayload 必须由调用方在使用后立即清零，且不得写入日志或普通响应。
type PortableExportRecord struct {
	CredentialRecord
	ExternalAccountIDValue    string
	ExternalSubjectIDValue    string
	ExternalAccountEmailValue string
	ExternalIdentitySource    string
}

// LoadExactForPortableExport 以账号、凭据和版本三重条件读取迁移材料，防止预检后
// 并发轮换导致导出包混入未确认的新版本。
func (s *Store) LoadExactForPortableExport(ctx context.Context, tenantID, providerAccountID, credentialID int64, version int32) (PortableExportRecord, error) {
	if err := s.validateReady(); err != nil {
		return PortableExportRecord{}, err
	}
	const query = `
SELECT ac.id, ac.tenant_id, ac.provider_account_id, ac.vendor, ac.auth_mode, ac.state,
       ac.credential_version, ac.encrypted_payload, ac.encryption_scheme, ac.key_id,
       ac.nonce, ac.aad_hash, ac.payload_fingerprint, ac.refresh_token_fingerprint,
       ac.access_expires_at, ac.refresh_expires_at, ac.refresh_before_at, ac.grace_until,
       ac.last_refresh_at, ac.last_refresh_outcome, ac.failure_class, ac.failure_count,
       ac.next_attempt_at, ac.created_at, ac.updated_at, ac.deleted_at,
       COALESCE(ac.external_account_id, ''), COALESCE(ac.external_subject_id, ''),
       COALESCE(ac.external_account_email, ''), COALESCE(ac.external_identity_source, '')
FROM account_credentials ac
JOIN provider_accounts pa
  ON pa.id = ac.provider_account_id AND pa.tenant_id = ac.tenant_id
WHERE ac.id = $1 AND ac.tenant_id = $2 AND ac.provider_account_id = $3
  AND ac.credential_version = $4 AND ac.deleted_at IS NULL AND pa.deleted_at IS NULL`
	var out PortableExportRecord
	var accessExp, refreshExp, refreshBefore, graceUntil, lastRefresh, nextAttempt, createdAt, updatedAt, deletedAt pgtype.Timestamptz
	err := s.db.QueryRow(ctx, query, credentialID, tenantID, providerAccountID, version).Scan(
		&out.ID, &out.TenantID, &out.ProviderAccountID, &out.Vendor, &out.AuthMode, &out.State,
		&out.CredentialVersion, &out.EncryptedPayload, &out.EncryptionScheme, &out.KeyID,
		&out.Nonce, &out.AADHash, &out.PayloadFingerprint, &out.RefreshTokenFingerprint,
		&accessExp, &refreshExp, &refreshBefore, &graceUntil,
		&lastRefresh, &out.LastRefreshOutcome, &out.FailureClass, &out.FailureCount,
		&nextAttempt, &createdAt, &updatedAt, &deletedAt,
		&out.ExternalAccountIDValue, &out.ExternalSubjectIDValue,
		&out.ExternalAccountEmailValue, &out.ExternalIdentitySource,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PortableExportRecord{}, ErrCredentialVersionConflict
		}
		return PortableExportRecord{}, err
	}
	out.AccessExpiresAt = pgTime(accessExp)
	out.RefreshExpiresAt = pgTime(refreshExp)
	out.RefreshBeforeAt = pgTime(refreshBefore)
	out.GraceUntil = pgTime(graceUntil)
	out.LastRefreshAt = pgTime(lastRefresh)
	out.NextAttemptAt = pgTime(nextAttempt)
	out.CreatedAt = pgTime(createdAt)
	out.UpdatedAt = pgTime(updatedAt)
	out.DeletedAt = pgTime(deletedAt)
	plaintext, err := s.decryptRecord(ctx, out.CredentialRecord)
	if err != nil {
		return PortableExportRecord{}, err
	}
	out.PlaintextPayload = plaintext
	return out, nil
}
