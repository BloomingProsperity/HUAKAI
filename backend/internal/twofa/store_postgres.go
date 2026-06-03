package twofa

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbtwofa "github.com/BloomingProsperity/HUAKAI/internal/db/twofa"
)

type postgresQueries interface {
	ConsumeTwoFactorBackupCode(ctx context.Context, arg dbtwofa.ConsumeTwoFactorBackupCodeParams) (int64, error)
	CountUnusedBackupCodes(ctx context.Context, arg dbtwofa.CountUnusedBackupCodesParams) (int64, error)
	CreateTwoFactorBackupCode(ctx context.Context, arg dbtwofa.CreateTwoFactorBackupCodeParams) error
	DeleteTwoFactorBackupCodesForUser(ctx context.Context, arg dbtwofa.DeleteTwoFactorBackupCodesForUserParams) error
	GetTwoFactorSettings(ctx context.Context, arg dbtwofa.GetTwoFactorSettingsParams) (dbtwofa.TwoFactorSetting, error)
	MarkTwoFactorSuccess(ctx context.Context, arg dbtwofa.MarkTwoFactorSuccessParams) error
	SetTwoFactorEnabled(ctx context.Context, arg dbtwofa.SetTwoFactorEnabledParams) error
	UpdateTwoFactorFailure(ctx context.Context, arg dbtwofa.UpdateTwoFactorFailureParams) error
	UpsertTwoFactorSettings(ctx context.Context, arg dbtwofa.UpsertTwoFactorSettingsParams) (dbtwofa.TwoFactorSetting, error)
}

type PostgresStore struct {
	pool *pgxpool.Pool
	q    postgresQueries
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	if pool == nil {
		return &PostgresStore{}
	}
	return &PostgresStore{pool: pool, q: dbtwofa.New(pool)}
}

func (s *PostgresStore) GetSettings(ctx context.Context, tenantID, userID int64) (Settings, bool, error) {
	if s == nil || s.q == nil {
		return Settings{}, false, ErrStoreNotConfigured
	}
	row, err := s.q.GetTwoFactorSettings(ctx, dbtwofa.GetTwoFactorSettingsParams{
		TenantID: tenantID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, err
	}
	return settingsFromDB(row), true, nil
}

func (s *PostgresStore) SaveSetup(ctx context.Context, settings Settings, backupCodeHashes [][]byte) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(ctx, tx, &committed)
	q := dbtwofa.New(tx)
	if _, err := q.UpsertTwoFactorSettings(ctx, dbtwofa.UpsertTwoFactorSettingsParams{
		TenantID: settings.TenantID, UserID: settings.UserID, SecretEnc: settings.SecretEnc,
		IsEnabled: settings.Enabled, FailedAttempts: int32(settings.FailedAttempts),
		LockedUntil: optionalTimestamptz(settings.LockedUntil),
		LastUsedAt:  optionalTimestamptz(settings.LastUsedAt),
		CreatedAt:   timestamptz(settings.CreatedAt),
		UpdatedAt:   timestamptz(settings.UpdatedAt),
	}); err != nil {
		return err
	}
	if err := replaceBackupCodes(ctx, q, settings.TenantID, settings.UserID, backupCodeHashes, settings.UpdatedAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *PostgresStore) SetEnabled(ctx context.Context, tenantID, userID int64, enabled bool, now time.Time) error {
	if s == nil || s.q == nil {
		return ErrStoreNotConfigured
	}
	return s.q.SetTwoFactorEnabled(ctx, dbtwofa.SetTwoFactorEnabledParams{
		TenantID: tenantID, UserID: userID, IsEnabled: enabled, UpdatedAt: timestamptz(now),
	})
}

func (s *PostgresStore) MarkSuccess(ctx context.Context, tenantID, userID int64, now time.Time) error {
	if s == nil || s.q == nil {
		return ErrStoreNotConfigured
	}
	return s.q.MarkTwoFactorSuccess(ctx, dbtwofa.MarkTwoFactorSuccessParams{
		TenantID: tenantID, UserID: userID, LastUsedAt: timestamptz(now),
	})
}

func (s *PostgresStore) MarkFailure(ctx context.Context, tenantID, userID int64, failedAttempts int, lockedUntil *time.Time, now time.Time) error {
	if s == nil || s.q == nil {
		return ErrStoreNotConfigured
	}
	return s.q.UpdateTwoFactorFailure(ctx, dbtwofa.UpdateTwoFactorFailureParams{
		TenantID: tenantID, UserID: userID, FailedAttempts: int32(failedAttempts),
		LockedUntil: optionalTimestamptz(lockedUntil), UpdatedAt: timestamptz(now),
	})
}

func (s *PostgresStore) CountUnusedBackupCodes(ctx context.Context, tenantID, userID int64) (int, error) {
	if s == nil || s.q == nil {
		return 0, ErrStoreNotConfigured
	}
	count, err := s.q.CountUnusedBackupCodes(ctx, dbtwofa.CountUnusedBackupCodesParams{
		TenantID: tenantID, UserID: userID,
	})
	return int(count), err
}

func (s *PostgresStore) ConsumeBackupCode(ctx context.Context, tenantID, userID int64, hash []byte, now time.Time) (bool, error) {
	if s == nil || s.q == nil {
		return false, ErrStoreNotConfigured
	}
	_, err := s.q.ConsumeTwoFactorBackupCode(ctx, dbtwofa.ConsumeTwoFactorBackupCodeParams{
		TenantID: tenantID, UserID: userID, CodeHash: hash, UsedAt: timestamptz(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *PostgresStore) ReplaceBackupCodes(ctx context.Context, tenantID, userID int64, hashes [][]byte, now time.Time) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(ctx, tx, &committed)
	q := dbtwofa.New(tx)
	if err := replaceBackupCodes(ctx, q, tenantID, userID, hashes, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}

func replaceBackupCodes(ctx context.Context, q postgresQueries, tenantID, userID int64, hashes [][]byte, now time.Time) error {
	if err := q.DeleteTwoFactorBackupCodesForUser(ctx, dbtwofa.DeleteTwoFactorBackupCodesForUserParams{
		TenantID: tenantID, UserID: userID,
	}); err != nil {
		return err
	}
	for _, hash := range hashes {
		if err := q.CreateTwoFactorBackupCode(ctx, dbtwofa.CreateTwoFactorBackupCodeParams{
			TenantID: tenantID, UserID: userID, CodeHash: hash, CreatedAt: timestamptz(now),
		}); err != nil {
			return err
		}
	}
	return nil
}

func settingsFromDB(row dbtwofa.TwoFactorSetting) Settings {
	return Settings{
		TenantID: row.TenantID, UserID: row.UserID,
		SecretEnc: append([]byte(nil), row.SecretEnc...),
		Enabled:   row.IsEnabled, FailedAttempts: int(row.FailedAttempts),
		LockedUntil: timePtrFromPG(row.LockedUntil),
		LastUsedAt:  timePtrFromPG(row.LastUsedAt),
		CreatedAt:   timeFromPG(row.CreatedAt),
		UpdatedAt:   timeFromPG(row.UpdatedAt),
	}
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func optionalTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return timestamptz(*t)
}

func timePtrFromPG(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	value := t.Time.UTC()
	return &value
}

func timeFromPG(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}

func rollbackUnlessCommitted(ctx context.Context, tx pgx.Tx, committed *bool) {
	if committed == nil || *committed {
		return
	}
	_ = tx.Rollback(ctx)
}
