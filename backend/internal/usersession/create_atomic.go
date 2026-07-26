package usersession

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/tenancy"
)

func newSessionFamily(in CreateInput, now time.Time) SessionFamily {
	authVersion := in.AuthVersion
	if authVersion <= 0 {
		authVersion = 1
	}
	return SessionFamily{
		ID:           uuid.NewString(),
		TenantID:     in.TenantID,
		UserID:       in.UserID,
		Status:       FamilyStatusActive,
		Generation:   1,
		AuthVersion:  authVersion,
		CreatedAt:    now.UTC(),
		LastActiveAt: now.UTC(),
		DeviceInfo:   normalizeDeviceInfo(in.DeviceInfo, in.UserAgent),
		IPBaseline:   IPClass(in.IP),
	}
}

func validateSessionBundle(bundle SessionBundle) error {
	if bundle.Family.ID == "" || bundle.Family.TenantID <= 0 || bundle.Family.UserID <= 0 ||
		bundle.RefreshToken.ID == "" || bundle.RefreshToken.FamilyID != bundle.Family.ID ||
		bundle.RefreshToken.TenantID != bundle.Family.TenantID ||
		bundle.SessionToken.ID == "" || bundle.SessionToken.FamilyID != bundle.Family.ID ||
		bundle.SessionToken.TenantID != bundle.Family.TenantID {
		return ErrInvalidInput
	}
	return nil
}

func (s *PostgresStore) CreateSession(ctx context.Context, bundle SessionBundle, policy SessionCreatePolicy, now time.Time) (SessionFamily, error) {
	if s == nil || s.db == nil {
		return SessionFamily{}, ErrStoreNotConfigured
	}
	if err := validateSessionBundle(bundle); err != nil {
		return SessionFamily{}, err
	}
	beginner, ok := s.db.(txBeginner)
	if !ok {
		return SessionFamily{}, ErrStoreNotConfigured
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return SessionFamily{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := LockUserSessionsInTransaction(ctx, tx, bundle.Family.TenantID, bundle.Family.UserID); err != nil {
		return SessionFamily{}, err
	}
	if err := tenancy.LockActiveForWrite(ctx, tx, bundle.Family.TenantID); err != nil {
		if errors.Is(err, tenancy.ErrTenantInactive) {
			return SessionFamily{}, ErrUserIneligible
		}
		return SessionFamily{}, err
	}
	var currentAuthVersion int
	err = tx.QueryRow(ctx, `
SELECT u.password_version
FROM users u
WHERE u.tenant_id = $1
  AND u.id = $2
  AND u.status = 'active'
  AND u.deleted_at IS NULL
FOR UPDATE`, bundle.Family.TenantID, bundle.Family.UserID).Scan(&currentAuthVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SessionFamily{}, ErrUserIneligible
		}
		return SessionFamily{}, err
	}
	if policy.ExpectedAuthVersion > 0 {
		if currentAuthVersion != policy.ExpectedAuthVersion ||
			bundle.Family.AuthVersion != policy.ExpectedAuthVersion {
			return SessionFamily{}, ErrAuthenticationStale
		}
	}
	revokedFamilyID, err := enforceDevicePolicyInTx(ctx, tx, bundle.Family.TenantID, bundle.Family.UserID, policy, now)
	if err != nil {
		return SessionFamily{}, err
	}
	storedFamily, err := insertSessionBundle(ctx, tx, bundle)
	if err != nil {
		return SessionFamily{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionFamily{}, err
	}
	if s.cache != nil {
		if revokedFamilyID != "" {
			_, _ = s.cache.RevokeFamily(ctx, bundle.Family.TenantID, revokedFamilyID, "device_limit_revoke_oldest", now)
		}
		s.cache.putFamily(storedFamily)
		_ = s.cache.InsertRefreshToken(ctx, bundle.RefreshToken)
		_ = s.cache.InsertSessionToken(ctx, bundle.SessionToken)
	}
	return storedFamily, nil
}

func enforceDevicePolicyInTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64, policy SessionCreatePolicy, now time.Time) (string, error) {
	if policy.MaxActiveFamilies <= 0 {
		return "", nil
	}
	rows, err := tx.Query(ctx, `
SELECT id::text
FROM session_families
WHERE tenant_id = $1 AND user_id = $2 AND status IN ('active', 'suspicious')
ORDER BY last_active_at ASC
LIMIT $3
FOR UPDATE`, tenantID, userID, policy.MaxActiveFamilies)
	if err != nil {
		return "", err
	}
	var familyIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		familyIDs = append(familyIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	if len(familyIDs) < policy.MaxActiveFamilies {
		return "", nil
	}
	switch strings.TrimSpace(policy.Mode) {
	case "revoke_oldest":
		oldest := familyIDs[0]
		if err := revokeFamilyInTx(ctx, tx, tenantID, oldest, "device_limit_revoke_oldest", now); err != nil {
			return "", err
		}
		return oldest, nil
	case "confirm":
		return "", ErrDeviceConfirmationRequired
	default:
		return "", ErrDeviceLimitExceeded
	}
}

func revokeFamilyInTx(ctx context.Context, tx pgx.Tx, tenantID int64, familyID, reason string, now time.Time) error {
	tag, err := tx.Exec(ctx, `
UPDATE session_families
SET status = 'revoked', revoked_at = $4, revoked_reason = $3, last_active_at = $4
WHERE tenant_id = $1 AND id = $2::uuid AND status IN ('active', 'suspicious')`,
		tenantID, familyID, reason, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrFamilyNotFound
	}
	if _, err := tx.Exec(ctx, `
UPDATE refresh_tokens SET status = 'revoked', consumed_at = COALESCE(consumed_at, $3)
WHERE tenant_id = $1 AND family_id = $2::uuid AND status = 'active'`, tenantID, familyID, now.UTC()); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
UPDATE session_tokens SET revoked_at = COALESCE(revoked_at, $3)
WHERE tenant_id = $1 AND family_id = $2::uuid AND revoked_at IS NULL`, tenantID, familyID, now.UTC())
	return err
}

func insertSessionBundle(ctx context.Context, tx pgx.Tx, bundle SessionBundle) (SessionFamily, error) {
	deviceInfo, err := json.Marshal(bundle.Family.DeviceInfo)
	if err != nil {
		return SessionFamily{}, err
	}
	const familyQuery = `
INSERT INTO session_families (
	    id, tenant_id, user_id, status, generation, auth_version, created_at, last_active_at, device_info, ip_baseline
) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10)
RETURNING id::text, tenant_id, user_id, status, generation, auth_version, created_at, last_active_at,
	          device_info, ip_baseline, revoked_at, revoked_reason`
	family, err := scanFamily(tx.QueryRow(ctx, familyQuery,
		bundle.Family.ID, bundle.Family.TenantID, bundle.Family.UserID, bundle.Family.Status,
		bundle.Family.Generation, bundle.Family.AuthVersion, bundle.Family.CreatedAt.UTC(),
		bundle.Family.LastActiveAt.UTC(), deviceInfo, bundle.Family.IPBaseline,
	))
	if err != nil {
		return SessionFamily{}, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO refresh_tokens (id, tenant_id, family_id, token_hash, generation, status, expires_at, created_at)
VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7, $8)`,
		bundle.RefreshToken.ID, bundle.RefreshToken.TenantID, bundle.RefreshToken.FamilyID,
		bundle.RefreshToken.TokenHash, bundle.RefreshToken.Generation, bundle.RefreshToken.Status,
		bundle.RefreshToken.ExpiresAt.UTC(), bundle.RefreshToken.CreatedAt.UTC()); err != nil {
		return SessionFamily{}, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO session_tokens (id, tenant_id, family_id, token_hash, generation, expires_at, created_at)
VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7)`,
		bundle.SessionToken.ID, bundle.SessionToken.TenantID, bundle.SessionToken.FamilyID,
		bundle.SessionToken.TokenHash, bundle.SessionToken.Generation,
		bundle.SessionToken.ExpiresAt.UTC(), bundle.SessionToken.CreatedAt.UTC()); err != nil {
		return SessionFamily{}, err
	}
	return family, nil
}

func (s *MemoryStore) CreateSession(_ context.Context, bundle SessionBundle, policy SessionCreatePolicy, now time.Time) (SessionFamily, error) {
	if s == nil {
		return SessionFamily{}, ErrStoreNotConfigured
	}
	if err := validateSessionBundle(bundle); err != nil {
		return SessionFamily{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	active := make([]SessionFamily, 0)
	for _, family := range s.families {
		if family.TenantID == bundle.Family.TenantID && family.UserID == bundle.Family.UserID &&
			(family.Status == FamilyStatusActive || family.Status == FamilyStatusSuspicious) {
			active = append(active, family)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].LastActiveAt.Before(active[j].LastActiveAt) })
	if policy.MaxActiveFamilies > 0 && len(active) >= policy.MaxActiveFamilies {
		switch strings.TrimSpace(policy.Mode) {
		case "revoke_oldest":
			revokeMemoryFamilyLocked(s, active[0].ID, "device_limit_revoke_oldest", now)
		case "confirm":
			return SessionFamily{}, ErrDeviceConfirmationRequired
		default:
			return SessionFamily{}, ErrDeviceLimitExceeded
		}
	}
	s.families[bundle.Family.ID] = bundle.Family
	s.tokens[bundle.RefreshToken.ID] = bundle.RefreshToken
	s.byHash[hashKey(bundle.RefreshToken.TokenHash)] = bundle.RefreshToken.ID
	s.sessionTokens[bundle.SessionToken.ID] = bundle.SessionToken
	s.sessionByHash[hashKey(bundle.SessionToken.TokenHash)] = bundle.SessionToken.ID
	return bundle.Family, nil
}

func revokeMemoryFamilyLocked(s *MemoryStore, familyID, reason string, now time.Time) {
	family := s.families[familyID]
	t := now.UTC()
	family.Status = FamilyStatusRevoked
	family.RevokedAt = &t
	family.RevokedReason = reason
	family.LastActiveAt = t
	s.families[familyID] = family
	for id, token := range s.tokens {
		if token.FamilyID == familyID && token.Status == RefreshTokenStatusActive {
			token.Status = RefreshTokenStatusRevoked
			token.ConsumedAt = &t
			s.tokens[id] = token
		}
	}
	for id, token := range s.sessionTokens {
		if token.FamilyID == familyID && token.RevokedAt == nil {
			token.RevokedAt = &t
			s.sessionTokens[id] = token
		}
	}
}
