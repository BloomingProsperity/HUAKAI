package passkey

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type PostgresStore struct {
	db db.DBTX
}

type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

func NewPostgresStore(database db.DBTX) *PostgresStore {
	return &PostgresStore{db: database}
}

func (s *PostgresStore) SaveCredential(ctx context.Context, record CredentialRecord) (CredentialRecord, error) {
	if s == nil || s.db == nil {
		return CredentialRecord{}, ErrStoreNotConfigured
	}
	if record.TenantID <= 0 || record.UserID <= 0 || len(record.CredentialID) == 0 || len(record.PublicKey) == 0 {
		return CredentialRecord{}, ErrInvalidInput
	}
	const q = `
INSERT INTO passkey_credentials (
    tenant_id, user_id, credential_id, public_key, sign_count, aaguid,
    attestation_type, transports, clone_warning, name, created_at
) VALUES (
    $1, $2, $3, $4, $5, NULLIF($6::bytea, ''::bytea), NULLIF($7, ''), NULLIF($8, ''),
    $9, NULLIF($10, ''), $11
)
RETURNING id, tenant_id, user_id, credential_id, public_key, sign_count, aaguid,
          attestation_type, transports, clone_warning, name, created_at, last_used_at`
	row, err := scanCredential(s.db.QueryRow(ctx, q,
		record.TenantID, record.UserID, record.CredentialID, record.PublicKey, int64(record.SignCount),
		nullableBytes(record.AAGUID), strings.TrimSpace(record.AttestationType), strings.Join(record.Transports, ","),
		record.CloneWarning, cleanName(record.Name), firstTime(record.CreatedAt, time.Now().UTC()),
	))
	if isUniqueViolation(err) {
		return CredentialRecord{}, ErrDuplicateCredential
	}
	return row, err
}

func (s *PostgresStore) SaveCredentialForSession(
	ctx context.Context,
	record CredentialRecord,
	familyID string,
	authVersion int,
) (CredentialRecord, error) {
	var saved CredentialRecord
	err := s.runSessionBoundMutation(
		ctx, record.TenantID, record.UserID, familyID, authVersion,
		func(txStore *PostgresStore) error {
			var err error
			saved, err = txStore.SaveCredential(ctx, record)
			return err
		},
	)
	return saved, err
}

func (s *PostgresStore) DeleteCredentialForSession(
	ctx context.Context,
	tenantID, userID, credentialID int64,
	familyID string,
	authVersion int,
) error {
	return s.runSessionBoundMutation(
		ctx, tenantID, userID, familyID, authVersion,
		func(txStore *PostgresStore) error {
			return txStore.DeleteCredential(ctx, tenantID, userID, credentialID)
		},
	)
}

func (s *PostgresStore) runSessionBoundMutation(
	ctx context.Context,
	tenantID, userID int64,
	familyID string,
	authVersion int,
	mutate func(*PostgresStore) error,
) error {
	if s == nil || s.db == nil || mutate == nil {
		return ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 || strings.TrimSpace(familyID) == "" || authVersion <= 0 {
		return ErrInvalidInput
	}
	run := func(tx pgx.Tx) error {
		if err := usersession.LockUserSessionsInTransaction(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		var familyVersion, userVersion int
		err := tx.QueryRow(ctx, `
SELECT sf.auth_version, u.password_version
FROM session_families sf
INNER JOIN users u
  ON u.tenant_id = sf.tenant_id
 AND u.id = sf.user_id
WHERE sf.tenant_id = $1
  AND sf.user_id = $2
  AND sf.id = $3::uuid
  AND sf.status IN ('active', 'suspicious')
  AND u.status = 'active'
  AND u.deleted_at IS NULL
FOR UPDATE OF sf, u`, tenantID, userID, familyID).Scan(&familyVersion, &userVersion)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSecurityStateChanged
			}
			return err
		}
		if familyVersion != authVersion || userVersion != authVersion {
			return ErrSecurityStateChanged
		}
		return mutate(&PostgresStore{db: tx})
	}
	if tx, ok := s.db.(pgx.Tx); ok {
		return run(tx)
	}
	beginner, ok := s.db.(txBeginner)
	if !ok {
		return ErrStoreNotConfigured
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := run(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) ListCredentials(ctx context.Context, tenantID, userID int64) ([]CredentialRecord, error) {
	if s == nil || s.db == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.db.Query(ctx, `
SELECT id, tenant_id, user_id, credential_id, public_key, sign_count, aaguid,
       attestation_type, transports, clone_warning, name, created_at, last_used_at
FROM passkey_credentials
WHERE tenant_id = $1 AND user_id = $2
ORDER BY created_at DESC, id DESC
`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CredentialRecord
	for rows.Next() {
		record, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetCredentialByID(ctx context.Context, tenantID, id int64) (CredentialRecord, error) {
	if s == nil || s.db == nil {
		return CredentialRecord{}, ErrStoreNotConfigured
	}
	record, err := scanCredential(s.db.QueryRow(ctx, `
SELECT id, tenant_id, user_id, credential_id, public_key, sign_count, aaguid,
       attestation_type, transports, clone_warning, name, created_at, last_used_at
FROM passkey_credentials
WHERE tenant_id = $1 AND id = $2
`, tenantID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return CredentialRecord{}, ErrCredentialNotFound
	}
	return record, err
}

func (s *PostgresStore) GetCredentialByCredentialID(ctx context.Context, tenantID int64, credentialID []byte) (CredentialRecord, error) {
	if s == nil || s.db == nil {
		return CredentialRecord{}, ErrStoreNotConfigured
	}
	record, err := scanCredential(s.db.QueryRow(ctx, `
SELECT id, tenant_id, user_id, credential_id, public_key, sign_count, aaguid,
       attestation_type, transports, clone_warning, name, created_at, last_used_at
FROM passkey_credentials
WHERE tenant_id = $1 AND credential_id = $2
`, tenantID, credentialID))
	if errors.Is(err, pgx.ErrNoRows) {
		return CredentialRecord{}, ErrCredentialNotFound
	}
	return record, err
}

func (s *PostgresStore) DeleteCredential(ctx context.Context, tenantID, userID, id int64) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	tag, err := s.db.Exec(ctx, `
DELETE FROM passkey_credentials
WHERE tenant_id = $1 AND user_id = $2 AND id = $3
`, tenantID, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrCredentialNotFound
	}
	return nil
}

func (s *PostgresStore) UpdateCredentialUsage(ctx context.Context, tenantID int64, credentialID []byte, signCount uint32, cloneWarning bool, now time.Time) (CredentialRecord, error) {
	if s == nil || s.db == nil {
		return CredentialRecord{}, ErrStoreNotConfigured
	}
	// CAS 守卫:把 service 层 signCountRegressed 的判定原子化到这条 UPDATE 上,
	// 防并发 ceremony 竞态绕过克隆检测——两个并发登录都读到旧 sign_count、都过了
	// 应用层 check,若无守卫则后写的会盲覆盖。守卫 `sign_count < $3 OR (sign_count=0
	// AND $3=0)` 与 signCountRegressed 完全对偶:仅当新计数严格大于库内值(或双方
	// 均为 0 的"不支持计数"设备)才写,克隆/重放的非递增计数 0 行命中。
	record, err := scanCredential(s.db.QueryRow(ctx, `
UPDATE passkey_credentials
SET sign_count = $3,
    clone_warning = clone_warning OR $4,
    last_used_at = $5
WHERE tenant_id = $1 AND credential_id = $2
  AND (sign_count < $3 OR (sign_count = 0 AND $3 = 0))
RETURNING id, tenant_id, user_id, credential_id, public_key, sign_count, aaguid,
          attestation_type, transports, clone_warning, name, created_at, last_used_at
`, tenantID, credentialID, int64(signCount), cloneWarning, now.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		// 0 行有两种成因:凭据不存在,或 CAS 失败(并发竞态下 sign_count 已被另一
		// 请求推进=克隆/重放)。用一次存在性查询区分,避免把克隆误报成"凭据不存在"。
		exists, exErr := s.credentialExists(ctx, tenantID, credentialID)
		if exErr != nil {
			return CredentialRecord{}, exErr
		}
		if exists {
			return CredentialRecord{}, ErrCloneDetected
		}
		return CredentialRecord{}, ErrCredentialNotFound
	}
	return record, err
}

// credentialExists 报告 (tenant_id, credential_id) 行是否存在,用于 CAS 失败后
// 区分"克隆/重放"与"凭据不存在"。
func (s *PostgresStore) credentialExists(ctx context.Context, tenantID int64, credentialID []byte) (bool, error) {
	var exists bool
	if err := s.db.QueryRow(ctx, `
SELECT EXISTS(SELECT 1 FROM passkey_credentials WHERE tenant_id = $1 AND credential_id = $2)
`, tenantID, credentialID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (s *PostgresStore) FlagCredentialCloneWarning(ctx context.Context, tenantID int64, credentialID []byte, _ time.Time) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	tag, err := s.db.Exec(ctx, `
UPDATE passkey_credentials
SET clone_warning = true
WHERE tenant_id = $1 AND credential_id = $2
`, tenantID, credentialID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrCredentialNotFound
	}
	return nil
}

func (s *PostgresStore) SaveCeremonySession(ctx context.Context, session CeremonySession) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	if session.ID == "" || session.TenantID <= 0 || session.Purpose == "" || len(session.SessionData) == 0 || session.ExpiresAt.IsZero() {
		return ErrInvalidInput
	}
	_, err := s.db.Exec(ctx, `
INSERT INTO webauthn_session (id, tenant_id, user_id, purpose, session_data, expires_at, created_at)
VALUES ($1, $2, NULLIF($3, 0), $4, $5::jsonb, $6, $7)
`, session.ID, session.TenantID, session.UserID, session.Purpose, string(session.SessionData), session.ExpiresAt.UTC(), firstTime(session.CreatedAt, time.Now().UTC()))
	return err
}

func (s *PostgresStore) ConsumeCeremonySession(ctx context.Context, in ConsumeCeremonyInput) (CeremonySession, error) {
	if s == nil || s.db == nil {
		return CeremonySession{}, ErrStoreNotConfigured
	}
	if in.ID == "" || in.TenantID <= 0 || in.Purpose == "" {
		return CeremonySession{}, ErrInvalidInput
	}
	q := `
DELETE FROM webauthn_session
WHERE id = $1 AND tenant_id = $2 AND purpose = $3 AND user_id IS NULL
RETURNING id, tenant_id, user_id, purpose, session_data, expires_at, created_at`
	args := []any{in.ID, in.TenantID, in.Purpose}
	if in.UserID > 0 {
		q = `
DELETE FROM webauthn_session
WHERE id = $1 AND tenant_id = $2 AND purpose = $3 AND user_id = $4
RETURNING id, tenant_id, user_id, purpose, session_data, expires_at, created_at`
		args = append(args, in.UserID)
	}
	session, err := scanCeremonySession(s.db.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return CeremonySession{}, ErrCeremonyNotFound
	}
	if err != nil {
		return CeremonySession{}, err
	}
	if !session.ExpiresAt.After(in.Now.UTC()) {
		return CeremonySession{}, ErrCeremonyExpired
	}
	return session, nil
}

func scanCredential(row pgx.Row) (CredentialRecord, error) {
	var record CredentialRecord
	var signCount int64
	var aaguid []byte
	var attestationType, transports, name pgtype.Text
	var lastUsedAt pgtype.Timestamptz
	if err := row.Scan(
		&record.ID, &record.TenantID, &record.UserID, &record.CredentialID, &record.PublicKey,
		&signCount, &aaguid, &attestationType, &transports, &record.CloneWarning,
		&name, &record.CreatedAt, &lastUsedAt,
	); err != nil {
		return CredentialRecord{}, err
	}
	record.SignCount = uint32(signCount)
	record.AAGUID = append([]byte(nil), aaguid...)
	if attestationType.Valid {
		record.AttestationType = attestationType.String
	}
	if transports.Valid && strings.TrimSpace(transports.String) != "" {
		record.Transports = strings.Split(transports.String, ",")
	}
	if name.Valid {
		record.Name = name.String
	}
	if lastUsedAt.Valid {
		t := lastUsedAt.Time.UTC()
		record.LastUsedAt = &t
	}
	record.CreatedAt = record.CreatedAt.UTC()
	return cloneCredential(record), nil
}

func scanCeremonySession(row pgx.Row) (CeremonySession, error) {
	var session CeremonySession
	var userID pgtype.Int8
	if err := row.Scan(
		&session.ID, &session.TenantID, &userID, &session.Purpose,
		&session.SessionData, &session.ExpiresAt, &session.CreatedAt,
	); err != nil {
		return CeremonySession{}, err
	}
	if userID.Valid {
		session.UserID = userID.Int64
	}
	session.SessionData = append([]byte(nil), session.SessionData...)
	session.ExpiresAt = session.ExpiresAt.UTC()
	session.CreatedAt = session.CreatedAt.UTC()
	return session, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullableBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	return value
}

func firstTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback.UTC()
	}
	return value.UTC()
}
