package credentialstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// scannedCredentialRecord 集中维护 account_credentials 基础列的扫描顺序。
// 查询可以在这组列之后追加专用字段，但不得复制基础列映射。
type scannedCredentialRecord struct {
	record        CredentialRecord
	accessExp     pgtype.Timestamptz
	refreshExp    pgtype.Timestamptz
	refreshBefore pgtype.Timestamptz
	graceUntil    pgtype.Timestamptz
	lastRefresh   pgtype.Timestamptz
	nextAttempt   pgtype.Timestamptz
	createdAt     pgtype.Timestamptz
	updatedAt     pgtype.Timestamptz
	deletedAt     pgtype.Timestamptz
}

func (s *scannedCredentialRecord) targets() []any {
	return []any{
		&s.record.ID, &s.record.TenantID, &s.record.ProviderAccountID, &s.record.Vendor, &s.record.AuthMode, &s.record.State,
		&s.record.CredentialVersion, &s.record.EncryptedPayload, &s.record.EncryptionScheme, &s.record.KeyID,
		&s.record.Nonce, &s.record.AADHash, &s.record.PayloadFingerprint, &s.record.RefreshTokenFingerprint,
		&s.accessExp, &s.refreshExp, &s.refreshBefore, &s.graceUntil,
		&s.lastRefresh, &s.record.LastRefreshOutcome, &s.record.FailureClass, &s.record.FailureCount,
		&s.nextAttempt, &s.createdAt, &s.updatedAt, &s.deletedAt,
	}
}

func (s scannedCredentialRecord) value() CredentialRecord {
	rec := s.record
	rec.AccessExpiresAt = pgTime(s.accessExp)
	rec.RefreshExpiresAt = pgTime(s.refreshExp)
	rec.RefreshBeforeAt = pgTime(s.refreshBefore)
	rec.GraceUntil = pgTime(s.graceUntil)
	rec.LastRefreshAt = pgTime(s.lastRefresh)
	rec.NextAttemptAt = pgTime(s.nextAttempt)
	rec.CreatedAt = pgTime(s.createdAt)
	rec.UpdatedAt = pgTime(s.updatedAt)
	rec.DeletedAt = pgTime(s.deletedAt)
	return rec
}

func scanCredentialRecord(row pgx.Row, additional ...any) (CredentialRecord, error) {
	var scanned scannedCredentialRecord
	targets := append(scanned.targets(), additional...)
	if err := row.Scan(targets...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CredentialRecord{}, ErrCredentialNotFound
		}
		return CredentialRecord{}, err
	}
	return scanned.value(), nil
}

func (s *Store) scanRecord(ctx context.Context, query string, args ...any) (CredentialRecord, error) {
	return scanCredentialRecord(s.db.QueryRow(ctx, query, args...))
}

func (s *Store) scanRecordForRefresh(ctx context.Context, query string, args ...any) (CredentialRecord, error) {
	var refreshLeadSeconds *int32
	rec, err := scanCredentialRecord(s.db.QueryRow(ctx, query, args...), &refreshLeadSeconds)
	if err != nil {
		return CredentialRecord{}, err
	}
	rec.RefreshLeadSeconds = refreshLeadSeconds
	return rec, nil
}

func (s *Store) scanRecordWithCount(ctx context.Context, query string, args ...any) (CredentialRecord, int64, error) {
	var rowCount int64
	rec, err := scanCredentialRecord(s.db.QueryRow(ctx, query, args...), &rowCount)
	if err != nil {
		return CredentialRecord{}, 0, err
	}
	return rec, rowCount, nil
}
