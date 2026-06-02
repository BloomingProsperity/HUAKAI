package userauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

type PostgresStore struct {
	db     db.DBTX
	cipher *credentialstore.Cipher
}

type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

func NewPostgresStore(database db.DBTX) *PostgresStore {
	return &PostgresStore{db: database}
}

func NewPostgresStoreWithKeys(database db.DBTX, keys credentialstore.KeyProvider) *PostgresStore {
	return NewPostgresStore(database).WithKeyProvider(keys)
}

func (s *PostgresStore) WithKeyProvider(keys credentialstore.KeyProvider) *PostgresStore {
	if s == nil {
		return nil
	}
	cp := *s
	if keys != nil {
		cp.cipher = credentialstore.NewCipher(keys)
	}
	return &cp
}

func (s *PostgresStore) WithTx(ctx context.Context, fn func(Store) error) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	beginner, ok := s.db.(txBeginner)
	if !ok {
		return fn(s)
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(&PostgresStore{db: tx, cipher: s.cipher}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) CreateUser(ctx context.Context, in CreateUserParams) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrStoreNotConfigured
	}
	email := NormalizeEmail(in.Email)
	status := in.Status
	if status == "" {
		status = UserStatusPendingVerification
	}
	const q = `
INSERT INTO users (
    tenant_id, email, display_name, password_hash, email_verified,
    invite_code_used, social_login_provider, status
) VALUES (
    $1, $2, $3, NULLIF($4, ''), $5, NULLIF($6, ''), NULLIF($7, ''), $8
)
RETURNING id, tenant_id, email, display_name, password_hash, email_verified,
          invite_code_used, social_login_provider, status, password_version,
          failed_login_count, locked_until, created_at, updated_at`
	user, err := scanUser(s.db.QueryRow(ctx, q,
		in.TenantID,
		email,
		strings.TrimSpace(in.DisplayName),
		strings.TrimSpace(in.PasswordHash),
		in.EmailVerified,
		strings.TrimSpace(in.InviteCodeUsed),
		strings.TrimSpace(in.SocialLoginProvider),
		status,
	))
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *PostgresStore) GetUserByEmail(ctx context.Context, tenantID int64, email string) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrStoreNotConfigured
	}
	const q = `
SELECT id, tenant_id, email, display_name, password_hash, email_verified,
       invite_code_used, social_login_provider, status, password_version,
       failed_login_count, locked_until, created_at, updated_at
FROM users
WHERE tenant_id = $1
  AND lower(email) = lower($2)
  AND deleted_at IS NULL
ORDER BY id
LIMIT 1`
	user, err := scanUser(s.db.QueryRow(ctx, q, tenantID, NormalizeEmail(email)))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return user, err
}

func (s *PostgresStore) GetUserByID(ctx context.Context, tenantID, userID int64) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrStoreNotConfigured
	}
	const q = `
SELECT id, tenant_id, email, display_name, password_hash, email_verified,
       invite_code_used, social_login_provider, status, password_version,
       failed_login_count, locked_until, created_at, updated_at
FROM users
WHERE tenant_id = $1
  AND id = $2
  AND deleted_at IS NULL`
	user, err := scanUser(s.db.QueryRow(ctx, q, tenantID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return user, err
}

func (s *PostgresStore) MarkLoginSuccess(ctx context.Context, tenantID, userID int64) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	_, err := s.db.Exec(ctx, `
UPDATE users
SET failed_login_count = 0,
    locked_until = NULL,
    updated_at = NOW()
WHERE tenant_id = $1 AND id = $2
`, tenantID, userID)
	return err
}

func (s *PostgresStore) MarkLoginFailure(ctx context.Context, tenantID, userID int64, threshold int) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	if threshold <= 0 {
		threshold = DefaultLockoutThreshold
	}
	_, err := s.db.Exec(ctx, `
UPDATE users
SET failed_login_count = failed_login_count + 1,
    status = CASE WHEN failed_login_count + 1 >= $3 THEN 'locked' ELSE status END,
    updated_at = NOW()
WHERE tenant_id = $1 AND id = $2
`, tenantID, userID, threshold)
	return err
}

func (s *PostgresStore) GetUserBySocialIdentity(ctx context.Context, tenantID int64, provider, subject string) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrStoreNotConfigured
	}
	const q = `
SELECT u.id, u.tenant_id, u.email, u.display_name, u.password_hash, u.email_verified,
       u.invite_code_used, u.social_login_provider, u.status, u.password_version,
       u.failed_login_count, u.locked_until, u.created_at, u.updated_at
FROM social_identity_links sil
INNER JOIN users u ON u.tenant_id = sil.tenant_id AND u.id = sil.user_id
WHERE sil.tenant_id = $1
  AND sil.provider = $2
  AND sil.subject = $3
  AND u.deleted_at IS NULL
LIMIT 1`
	user, err := scanUser(s.db.QueryRow(ctx, q, tenantID, normalizeSocialProvider(provider), strings.TrimSpace(subject)))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return user, err
}

func (s *PostgresStore) LinkSocialIdentity(ctx context.Context, tenantID, userID int64, provider, subject string) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrStoreNotConfigured
	}
	provider = normalizeSocialProvider(provider)
	subject = strings.TrimSpace(subject)
	if provider == "" || subject == "" {
		return User{}, ErrInvalidInput
	}
	tag, err := s.db.Exec(ctx, `
INSERT INTO social_identity_links (tenant_id, user_id, provider, subject, email_verified)
VALUES ($1, $2, $3, $4, true)
ON CONFLICT (tenant_id, provider, subject)
DO UPDATE SET email_verified = true, updated_at = NOW()
WHERE social_identity_links.user_id = EXCLUDED.user_id
`, tenantID, userID, provider, subject)
	if err != nil {
		return User{}, err
	}
	if tag.RowsAffected() != 1 {
		return User{}, ErrSocialLoginRejected
	}
	const q = `
UPDATE users
SET social_login_provider = $3,
    email_verified = true,
    status = CASE WHEN status = 'pending_verification' THEN 'active' ELSE status END,
    updated_at = NOW()
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
RETURNING id, tenant_id, email, display_name, password_hash, email_verified,
          invite_code_used, social_login_provider, status, password_version,
          failed_login_count, locked_until, created_at, updated_at`
	user, err := scanUser(s.db.QueryRow(ctx, q, tenantID, userID, provider))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return user, err
}

func (s *PostgresStore) CreateEmailVerificationToken(ctx context.Context, challenge TokenChallenge) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	_, err := s.db.Exec(ctx, `
INSERT INTO email_verification_tokens (id, tenant_id, user_id, token_hash, expires_at)
VALUES ($1::uuid, $2, $3, $4, $5)
`, challenge.ID, challenge.TenantID, challenge.UserID, challenge.TokenHash, challenge.ExpiresAt.UTC())
	return err
}

func (s *PostgresStore) ConsumeEmailVerificationToken(ctx context.Context, tenantID int64, tokenHash []byte, now time.Time) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrStoreNotConfigured
	}
	const q = `
WITH consumed AS (
    UPDATE email_verification_tokens
    SET consumed_at = NOW()
    WHERE tenant_id = $1
      AND token_hash = $2
      AND consumed_at IS NULL
      AND expires_at > $3
    RETURNING user_id
), updated AS (
    UPDATE users u
    SET email_verified = true,
        status = CASE WHEN u.status = 'pending_verification' THEN 'active' ELSE u.status END,
        updated_at = NOW()
    FROM consumed c
    WHERE u.tenant_id = $1 AND u.id = c.user_id AND u.deleted_at IS NULL
    RETURNING u.id, u.tenant_id, u.email, u.display_name, u.password_hash, u.email_verified,
              u.invite_code_used, u.social_login_provider, u.status, u.password_version,
              u.failed_login_count, u.locked_until, u.created_at, u.updated_at
)
SELECT * FROM updated`
	user, err := scanUser(s.db.QueryRow(ctx, q, tenantID, tokenHash, now.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrTokenInvalid
	}
	return user, err
}

func (s *PostgresStore) CreatePasswordResetToken(ctx context.Context, challenge TokenChallenge, passwordVersion int) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	_, err := s.db.Exec(ctx, `
INSERT INTO password_reset_tokens (id, tenant_id, user_id, token_hash, password_version, expires_at)
VALUES ($1::uuid, $2, $3, $4, $5, $6)
	`, challenge.ID, challenge.TenantID, challenge.UserID, challenge.TokenHash, passwordVersion, challenge.ExpiresAt.UTC())
	return err
}

func (s *PostgresStore) PreparePasswordResetTokenUser(ctx context.Context, tenantID int64, tokenHash []byte, now time.Time) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrStoreNotConfigured
	}
	const q = `
WITH candidate AS (
    SELECT prt.user_id, prt.password_version
    FROM password_reset_tokens prt
    WHERE prt.tenant_id = $1
      AND prt.token_hash = $2
      AND prt.consumed_at IS NULL
      AND prt.expires_at > $3
), barrier AS (
    UPDATE users u
    SET status = CASE
            WHEN u.status IN ('active', 'locked', 'pending_verification') THEN 'reset_required'
            ELSE u.status
        END,
        updated_at = CASE
            WHEN u.status IN ('active', 'locked', 'pending_verification') THEN NOW()
            ELSE u.updated_at
        END
    FROM candidate c
    WHERE u.tenant_id = $1
      AND u.id = c.user_id
      AND u.password_version = c.password_version
      AND u.deleted_at IS NULL
    RETURNING u.id, u.tenant_id, u.email, u.display_name, u.password_hash, u.email_verified,
              u.invite_code_used, u.social_login_provider, u.status, u.password_version,
              u.failed_login_count, u.locked_until, u.created_at, u.updated_at
)
SELECT u.id, u.tenant_id, u.email, u.display_name, u.password_hash, u.email_verified,
       u.invite_code_used, u.social_login_provider, u.status, u.password_version,
       u.failed_login_count, u.locked_until, u.created_at, u.updated_at
FROM barrier u`
	user, err := scanUser(s.db.QueryRow(ctx, q, tenantID, tokenHash, now.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrTokenInvalid
	}
	return user, err
}

func (s *PostgresStore) ConsumePasswordResetToken(ctx context.Context, tenantID int64, tokenHash []byte, passwordHash string, now time.Time) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrStoreNotConfigured
	}
	const q = `
WITH consumed AS (
    UPDATE password_reset_tokens
    SET consumed_at = NOW()
    WHERE tenant_id = $1
      AND token_hash = $2
      AND consumed_at IS NULL
      AND expires_at > $3
    RETURNING user_id, password_version
), updated AS (
    UPDATE users u
    SET password_hash = $4,
        password_version = u.password_version + 1,
        failed_login_count = 0,
        locked_until = NULL,
        status = CASE WHEN u.status IN ('locked', 'reset_required', 'pending_verification') THEN 'active' ELSE u.status END,
        updated_at = NOW()
    FROM consumed c
    WHERE u.tenant_id = $1
      AND u.id = c.user_id
      AND u.password_version = c.password_version
      AND u.deleted_at IS NULL
    RETURNING u.id, u.tenant_id, u.email, u.display_name, u.password_hash, u.email_verified,
              u.invite_code_used, u.social_login_provider, u.status, u.password_version,
              u.failed_login_count, u.locked_until, u.created_at, u.updated_at
)
SELECT * FROM updated`
	user, err := scanUser(s.db.QueryRow(ctx, q, tenantID, tokenHash, now.UTC(), passwordHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrTokenInvalid
	}
	return user, err
}

func (s *PostgresStore) RedeemInvite(ctx context.Context, tenantID int64, rawCode string, now time.Time) (InviteCode, error) {
	if s == nil || s.db == nil {
		return InviteCode{}, ErrStoreNotConfigured
	}
	codeHash := HashInviteCode(rawCode)
	if _, inTx := s.db.(pgx.Tx); inTx {
		if err := AcquireInviteAdvisoryLock(ctx, s.db, codeHash); err != nil {
			return InviteCode{}, err
		}
		return redeemInviteWithDB(ctx, s.db, tenantID, codeHash, now)
	}
	if beginner, ok := s.db.(txBeginner); ok {
		tx, err := beginner.Begin(ctx)
		if err != nil {
			return InviteCode{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := AcquireInviteAdvisoryLock(ctx, tx, codeHash); err != nil {
			return InviteCode{}, err
		}
		invite, err := redeemInviteWithDB(ctx, tx, tenantID, codeHash, now)
		if err != nil {
			return InviteCode{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return InviteCode{}, err
		}
		return invite, nil
	}
	return redeemInviteWithDB(ctx, s.db, tenantID, codeHash, now)
}

func redeemInviteWithDB(ctx context.Context, database db.DBTX, tenantID int64, codeHash string, now time.Time) (InviteCode, error) {
	const q = `
UPDATE invite_codes
SET used_count = used_count + 1,
    status = CASE WHEN used_count + 1 >= max_uses THEN 'exhausted' ELSE status END,
    updated_at = NOW()
WHERE tenant_id = $1
  AND code = $2
  AND status = 'active'
  AND (valid_until IS NULL OR valid_until > $3)
  AND used_count < max_uses
RETURNING code, tenant_id, created_by, max_uses, used_count, valid_until, status, created_at, updated_at`
	invite, err := scanInvite(database.QueryRow(ctx, q, tenantID, codeHash, now.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return InviteCode{}, ErrInviteInvalid
	}
	return invite, err
}

func (s *PostgresStore) CreateInviteBinding(ctx context.Context, tenantID, userID int64, inviteCodeHash string, redeemedAt time.Time) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	if strings.TrimSpace(inviteCodeHash) == "" {
		return nil
	}
	_, err := s.db.Exec(ctx, `
INSERT INTO invite_bindings (id, tenant_id, user_id, invite_code, redeemed_at)
VALUES ($1::uuid, $2, $3, $4, $5)
`, uuid.NewString(), tenantID, userID, strings.TrimSpace(inviteCodeHash), redeemedAt.UTC())
	return err
}

func (s *PostgresStore) CreateOAuthFlowSession(ctx context.Context, challenge OAuthFlowChallenge) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	ciphertext, err := s.encryptPKCEVerifier(ctx, challenge)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
INSERT INTO oauth_flow_sessions (
    id, tenant_id, provider, state_hash, nonce_hash, pkce_verifier, pkce_verifier_ciphertext, redirect_uri, expires_at
) VALUES (
    $1::uuid, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9
)
`, challenge.ID, challenge.TenantID, challenge.Provider, challenge.StateHash, challenge.NonceHash, "encrypted:v1", ciphertext, challenge.RedirectURI, challenge.ExpiresAt.UTC())
	return err
}

func (s *PostgresStore) ConsumeOAuthFlowSession(ctx context.Context, tenantID int64, provider string, stateHash []byte, now time.Time) (OAuthFlowSession, error) {
	if s == nil || s.db == nil {
		return OAuthFlowSession{}, ErrStoreNotConfigured
	}
	const q = `
UPDATE oauth_flow_sessions
SET consumed_at = NOW()
WHERE tenant_id = $1
  AND provider = $2
  AND state_hash = $3
  AND consumed_at IS NULL
  AND expires_at > $4
RETURNING id::text, tenant_id, provider, state_hash, nonce_hash,
          pkce_verifier_ciphertext, pkce_verifier, redirect_uri, expires_at, consumed_at, created_at`
	flow, err := scanOAuthFlow(s.db.QueryRow(ctx, q, tenantID, normalizeSocialProvider(provider), stateHash, now.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return OAuthFlowSession{}, ErrOAuthFlowNotFound
	}
	if err != nil {
		return OAuthFlowSession{}, err
	}
	verifier, err := s.decryptPKCEVerifier(ctx, flow)
	if err != nil {
		return OAuthFlowSession{}, err
	}
	flow.PKCEVerifier = verifier
	return flow, nil
}

func scanUser(row pgx.Row) (User, error) {
	var out User
	var email, displayName, passwordHash, inviteCode, socialProvider pgtype.Text
	var lockedUntil pgtype.Timestamptz
	var status string
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&email,
		&displayName,
		&passwordHash,
		&out.EmailVerified,
		&inviteCode,
		&socialProvider,
		&status,
		&out.PasswordVersion,
		&out.FailedLoginCount,
		&lockedUntil,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		return User{}, err
	}
	out.Email = textValue(email)
	out.DisplayName = textValue(displayName)
	out.PasswordHash = textValue(passwordHash)
	out.InviteCodeUsed = textValue(inviteCode)
	out.SocialLoginProvider = textValue(socialProvider)
	out.Status = UserStatus(status)
	if lockedUntil.Valid {
		t := lockedUntil.Time
		out.LockedUntil = &t
	}
	return out, nil
}

func scanInvite(row pgx.Row) (InviteCode, error) {
	var out InviteCode
	var createdBy pgtype.Int8
	var validUntil pgtype.Timestamptz
	if err := row.Scan(
		&out.Code,
		&out.TenantID,
		&createdBy,
		&out.MaxUses,
		&out.UsedCount,
		&validUntil,
		&out.Status,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		return InviteCode{}, err
	}
	if createdBy.Valid {
		out.CreatedBy = createdBy.Int64
	}
	if validUntil.Valid {
		t := validUntil.Time
		out.ValidUntil = &t
	}
	return out, nil
}

func scanOAuthFlow(row pgx.Row) (OAuthFlowSession, error) {
	var out OAuthFlowSession
	var legacyVerifier pgtype.Text
	var redirectURI pgtype.Text
	var consumedAt pgtype.Timestamptz
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.Provider,
		&out.StateHash,
		&out.NonceHash,
		&out.PKCEVerifierCiphertext,
		&legacyVerifier,
		&redirectURI,
		&out.ExpiresAt,
		&consumedAt,
		&out.CreatedAt,
	); err != nil {
		return OAuthFlowSession{}, err
	}
	out.Provider = normalizeSocialProvider(out.Provider)
	out.PKCEVerifier = textValue(legacyVerifier)
	out.RedirectURI = textValue(redirectURI)
	if consumedAt.Valid {
		t := consumedAt.Time
		out.ConsumedAt = &t
	}
	return out, nil
}

const pkceVerifierEnvelopePrefix = "huakai-userauth-pkce-v1:"

type pkceVerifierEnvelope struct {
	Ciphertext       []byte `json:"ciphertext"`
	Nonce            []byte `json:"nonce"`
	KeyID            string `json:"key_id"`
	EncryptionScheme string `json:"encryption_scheme"`
	AADHash          string `json:"aad_hash,omitempty"`
}

func (s *PostgresStore) encryptPKCEVerifier(ctx context.Context, challenge OAuthFlowChallenge) ([]byte, error) {
	if strings.TrimSpace(challenge.PKCEVerifier) == "" {
		return nil, ErrInvalidInput
	}
	if s == nil || s.cipher == nil {
		return nil, fmt.Errorf("%w: userauth pkce cipher not configured", credentialstore.ErrKeyUnavailable)
	}
	env, err := s.cipher.Encrypt(ctx, []byte(challenge.PKCEVerifier), pkceVerifierAAD(
		challenge.TenantID, challenge.Provider, challenge.ID, challenge.StateHash,
	))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(pkceVerifierEnvelope{
		Ciphertext:       env.Ciphertext,
		Nonce:            env.Nonce,
		KeyID:            env.KeyID,
		EncryptionScheme: env.EncryptionScheme,
		AADHash:          env.AADHash,
	})
	if err != nil {
		return nil, err
	}
	return append([]byte(pkceVerifierEnvelopePrefix), raw...), nil
}

func (s *PostgresStore) decryptPKCEVerifier(ctx context.Context, flow OAuthFlowSession) (string, error) {
	if len(flow.PKCEVerifierCiphertext) == 0 {
		if strings.TrimSpace(flow.PKCEVerifier) != "" && flow.PKCEVerifier != "encrypted:v1" {
			return flow.PKCEVerifier, nil
		}
		return "", fmt.Errorf("%w: userauth pkce verifier ciphertext missing", credentialstore.ErrDecryptFailed)
	}
	if s == nil || s.cipher == nil {
		return "", fmt.Errorf("%w: userauth pkce cipher not configured", credentialstore.ErrKeyUnavailable)
	}
	if !bytes.HasPrefix(flow.PKCEVerifierCiphertext, []byte(pkceVerifierEnvelopePrefix)) {
		return "", fmt.Errorf("%w: userauth pkce verifier envelope missing", credentialstore.ErrDecryptFailed)
	}
	var packed pkceVerifierEnvelope
	if err := json.Unmarshal(bytes.TrimPrefix(flow.PKCEVerifierCiphertext, []byte(pkceVerifierEnvelopePrefix)), &packed); err != nil {
		return "", fmt.Errorf("%w: userauth pkce verifier envelope invalid", credentialstore.ErrDecryptFailed)
	}
	plaintext, err := s.cipher.Decrypt(ctx, credentialstore.Envelope{
		Ciphertext:       packed.Ciphertext,
		Nonce:            packed.Nonce,
		KeyID:            packed.KeyID,
		EncryptionScheme: packed.EncryptionScheme,
		AADHash:          packed.AADHash,
	}, pkceVerifierAAD(flow.TenantID, flow.Provider, flow.ID, flow.StateHash))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func pkceVerifierAAD(tenantID int64, provider, flowID string, stateHash []byte) credentialstore.AAD {
	sum := sha256.Sum256(append(append([]byte("huakai-userauth-pkce-v1:"), []byte(strings.TrimSpace(flowID))...), stateHash...))
	return credentialstore.AAD{
		TenantID:          tenantID,
		ProviderAccountID: int64(binary.BigEndian.Uint64(sum[:8]) & 0x7fffffffffffffff),
		Vendor:            "userauth_oauth_flow",
		AuthMode:          normalizeSocialProvider(provider),
		Version:           1,
	}
}

func textValue(value pgtype.Text) string {
	if value.Valid {
		return value.String
	}
	return ""
}
