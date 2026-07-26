package usersession

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/tenancy"
)

func (s *PostgresStore) RotateSession(
	ctx context.Context,
	expectedFamily SessionFamily,
	oldToken RefreshToken,
	newToken RefreshToken,
	sessionToken SessionToken,
	now time.Time,
) (SessionFamily, error) {
	if s == nil || s.db == nil {
		return SessionFamily{}, ErrStoreNotConfigured
	}
	if beginner, ok := s.db.(txBeginner); ok {
		tx, err := beginner.Begin(ctx)
		if err != nil {
			return SessionFamily{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		family, err := rotateSessionWithDB(
			ctx, tx, expectedFamily, oldToken, newToken, sessionToken, now,
		)
		if err != nil {
			return SessionFamily{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return SessionFamily{}, err
		}
		if s.cache != nil {
			_, _ = s.cache.RotateSession(
				ctx, expectedFamily, oldToken, newToken, sessionToken, now,
			)
			s.cache.putFamily(family)
		}
		return family, nil
	}
	family, err := rotateSessionWithDB(
		ctx, s.db, expectedFamily, oldToken, newToken, sessionToken, now,
	)
	if err == nil && s.cache != nil {
		_, _ = s.cache.RotateSession(
			ctx, expectedFamily, oldToken, newToken, sessionToken, now,
		)
		s.cache.putFamily(family)
	}
	return family, err
}

func rotateSessionWithDB(
	ctx context.Context,
	database db.DBTX,
	expectedFamily SessionFamily,
	oldToken RefreshToken,
	newToken RefreshToken,
	sessionToken SessionToken,
	now time.Time,
) (SessionFamily, error) {
	if err := validateRefreshRotation(expectedFamily, oldToken, newToken, sessionToken); err != nil {
		return SessionFamily{}, err
	}
	tx, ok := database.(pgx.Tx)
	if !ok {
		return SessionFamily{}, ErrStoreNotConfigured
	}
	if err := LockUserSessionsInTransaction(
		ctx, tx, expectedFamily.TenantID, expectedFamily.UserID,
	); err != nil {
		return SessionFamily{}, err
	}
	if err := tenancy.LockActiveForWrite(ctx, tx, expectedFamily.TenantID); err != nil {
		if errors.Is(err, tenancy.ErrTenantInactive) {
			return SessionFamily{}, ErrUserIneligible
		}
		return SessionFamily{}, err
	}
	var currentAuthVersion int
	err := tx.QueryRow(ctx, `
SELECT password_version
FROM users
WHERE tenant_id=$1
  AND id=$2
  AND status='active'
  AND deleted_at IS NULL
FOR UPDATE`, expectedFamily.TenantID, expectedFamily.UserID).Scan(&currentAuthVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionFamily{}, ErrUserIneligible
	}
	if err != nil {
		return SessionFamily{}, err
	}
	if currentAuthVersion != expectedFamily.AuthVersion {
		return SessionFamily{}, ErrAuthenticationStale
	}

	tag, err := database.Exec(ctx, `
UPDATE refresh_tokens
SET status = 'consumed',
    consumed_at = $3
WHERE id = $1::uuid
  AND family_id = $2::uuid
  AND status = 'active'
`, oldToken.ID, oldToken.FamilyID, now.UTC())
	if err != nil {
		return SessionFamily{}, err
	}
	if tag.RowsAffected() != 1 {
		return SessionFamily{}, ErrRefreshReplay
	}
	if _, err := database.Exec(ctx, `
INSERT INTO refresh_tokens (id, tenant_id, family_id, token_hash, generation, status, expires_at, created_at)
VALUES ($1::uuid, $2, $3::uuid, $4, $5, 'active', $6, $7)
`, newToken.ID, newToken.TenantID, newToken.FamilyID, newToken.TokenHash, newToken.Generation, newToken.ExpiresAt.UTC(), newToken.CreatedAt.UTC()); err != nil {
		return SessionFamily{}, err
	}
	const q = `
UPDATE session_families
SET generation = $3,
    last_active_at = $4
WHERE id = $1::uuid
  AND tenant_id = $2
  AND user_id = $5
  AND auth_version = $6
  AND status IN ('active', 'suspicious')
RETURNING id::text, tenant_id, user_id, status, generation, auth_version, created_at, last_active_at,
          device_info, ip_baseline, revoked_at, revoked_reason`
	family, err := scanFamily(database.QueryRow(
		ctx, q,
		oldToken.FamilyID, oldToken.TenantID, newToken.Generation, now.UTC(),
		expectedFamily.UserID, expectedFamily.AuthVersion,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionFamily{}, ErrFamilyRevoked
	}
	if err != nil {
		return SessionFamily{}, err
	}
	if _, err := database.Exec(ctx, `
INSERT INTO session_tokens (id, tenant_id, family_id, token_hash, generation, expires_at, created_at)
VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7)
`, sessionToken.ID, sessionToken.TenantID, sessionToken.FamilyID,
		sessionToken.TokenHash, sessionToken.Generation,
		sessionToken.ExpiresAt.UTC(), sessionToken.CreatedAt.UTC(),
	); err != nil {
		return SessionFamily{}, err
	}
	return family, nil
}

func validateRefreshRotation(
	family SessionFamily,
	oldToken RefreshToken,
	newToken RefreshToken,
	sessionToken SessionToken,
) error {
	if family.ID == "" || family.TenantID <= 0 || family.UserID <= 0 || family.AuthVersion <= 0 ||
		oldToken.ID == "" || oldToken.FamilyID != family.ID || oldToken.TenantID != family.TenantID ||
		newToken.ID == "" || newToken.FamilyID != family.ID || newToken.TenantID != family.TenantID ||
		newToken.Generation != oldToken.Generation+1 ||
		sessionToken.ID == "" || sessionToken.FamilyID != family.ID ||
		sessionToken.TenantID != family.TenantID || sessionToken.Generation != newToken.Generation {
		return ErrInvalidInput
	}
	return nil
}

func (s *MemoryStore) RotateSession(
	_ context.Context,
	expectedFamily SessionFamily,
	oldToken RefreshToken,
	newToken RefreshToken,
	sessionToken SessionToken,
	now time.Time,
) (SessionFamily, error) {
	if s == nil {
		return SessionFamily{}, ErrStoreNotConfigured
	}
	if err := validateRefreshRotation(expectedFamily, oldToken, newToken, sessionToken); err != nil {
		return SessionFamily{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.tokens[oldToken.ID]
	if !ok || current.Status != RefreshTokenStatusActive {
		return SessionFamily{}, ErrRefreshReplay
	}
	family, ok := s.families[oldToken.FamilyID]
	if !ok {
		return SessionFamily{}, ErrFamilyNotFound
	}
	if family.Status != FamilyStatusActive && family.Status != FamilyStatusSuspicious {
		return SessionFamily{}, ErrFamilyRevoked
	}
	if family.TenantID != expectedFamily.TenantID || family.UserID != expectedFamily.UserID ||
		family.AuthVersion != expectedFamily.AuthVersion {
		return SessionFamily{}, ErrAuthenticationStale
	}
	t := now.UTC()
	current.Status = RefreshTokenStatusConsumed
	current.ConsumedAt = &t
	s.tokens[current.ID] = current
	s.tokens[newToken.ID] = newToken
	s.byHash[hashKey(newToken.TokenHash)] = newToken.ID
	family.Generation = newToken.Generation
	family.LastActiveAt = t
	s.families[family.ID] = family
	s.sessionTokens[sessionToken.ID] = sessionToken
	s.sessionByHash[hashKey(sessionToken.TokenHash)] = sessionToken.ID
	return family, nil
}
