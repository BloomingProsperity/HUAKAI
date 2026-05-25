package usersession

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

type Store interface {
	CreateFamily(context.Context, CreateInput, time.Time) (SessionFamily, error)
	InsertSessionToken(context.Context, SessionToken) error
	LookupSessionToken(context.Context, []byte) (SessionRecord, error)
	TouchSessionToken(context.Context, int64, string, time.Time) error
	RevokeSessionToken(context.Context, int64, []byte, time.Time) error
	InsertRefreshToken(context.Context, RefreshToken) error
	LookupRefreshToken(context.Context, []byte) (RefreshRecord, error)
	RotateRefreshToken(context.Context, RefreshToken, RefreshToken, time.Time) (SessionFamily, error)
	RevokeToken(context.Context, int64, []byte, string, time.Time) error
	RevokeFamily(context.Context, int64, string, string, time.Time) (SessionFamily, error)
	RevokeUser(context.Context, int64, int64, string, time.Time) (int64, error)
	ListFamilies(context.Context, int64, int64) ([]SessionFamily, error)
}

type PostgresStore struct {
	db    db.DBTX
	cache *MemoryStore
}

type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

func NewPostgresStore(database db.DBTX) *PostgresStore {
	return &PostgresStore{db: database, cache: NewMemoryStore()}
}

func (s *PostgresStore) CreateFamily(ctx context.Context, in CreateInput, now time.Time) (SessionFamily, error) {
	if s == nil || s.db == nil {
		return SessionFamily{}, ErrStoreNotConfigured
	}
	family := SessionFamily{
		ID:           uuid.NewString(),
		TenantID:     in.TenantID,
		UserID:       in.UserID,
		Status:       FamilyStatusActive,
		Generation:   1,
		CreatedAt:    now.UTC(),
		LastActiveAt: now.UTC(),
		DeviceInfo:   normalizeDeviceInfo(in.DeviceInfo, in.UserAgent),
		IPBaseline:   IPClass(in.IP),
	}
	deviceInfo, err := json.Marshal(family.DeviceInfo)
	if err != nil {
		return SessionFamily{}, err
	}
	const q = `
INSERT INTO session_families (
    id, tenant_id, user_id, status, generation, created_at, last_active_at, device_info, ip_baseline
) VALUES (
    $1::uuid, $2, $3, $4, $5, $6, $7, $8::jsonb, $9
)
RETURNING id::text, tenant_id, user_id, status, generation, created_at, last_active_at,
          device_info, ip_baseline, revoked_at, revoked_reason`
	row, err := scanFamily(s.db.QueryRow(ctx, q,
		family.ID, family.TenantID, family.UserID, family.Status, family.Generation,
		family.CreatedAt, family.LastActiveAt, deviceInfo, family.IPBaseline,
	))
	if err == nil && s.cache != nil {
		s.cache.putFamily(row)
	}
	return row, err
}

func (s *PostgresStore) InsertSessionToken(ctx context.Context, token SessionToken) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	_, err := s.db.Exec(ctx, `
INSERT INTO session_tokens (id, tenant_id, family_id, token_hash, generation, expires_at, created_at)
VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7)
`, token.ID, token.TenantID, token.FamilyID, token.TokenHash, token.Generation, token.ExpiresAt.UTC(), token.CreatedAt.UTC())
	if err == nil && s.cache != nil {
		_ = s.cache.InsertSessionToken(ctx, token)
	}
	return err
}

func (s *PostgresStore) LookupSessionToken(ctx context.Context, tokenHash []byte) (SessionRecord, error) {
	if s == nil || s.db == nil {
		return SessionRecord{}, ErrStoreNotConfigured
	}
	const q = `
SELECT st.id::text, st.tenant_id, st.family_id::text, st.token_hash, st.generation,
       st.expires_at, st.created_at, st.last_used_at, st.revoked_at,
       sf.id::text, sf.tenant_id, sf.user_id, sf.status, sf.generation, sf.created_at,
       sf.last_active_at, sf.device_info, sf.ip_baseline, sf.revoked_at, sf.revoked_reason
FROM session_tokens st
INNER JOIN session_families sf ON sf.id = st.family_id AND sf.tenant_id = st.tenant_id
WHERE st.token_hash = $1`
	rec, err := scanSessionRecord(s.db.QueryRow(ctx, q, tokenHash))
	if errors.Is(err, pgx.ErrNoRows) {
		if s.cache != nil {
			s.cache.deleteSessionTokenHash(tokenHash)
		}
		return SessionRecord{}, ErrTokenNotFound
	}
	if err == nil && s.cache != nil {
		s.cache.putSessionRecord(rec)
	}
	return rec, err
}

func (s *PostgresStore) TouchSessionToken(ctx context.Context, tenantID int64, tokenID string, now time.Time) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	_, err := s.db.Exec(ctx, `
UPDATE session_tokens
SET last_used_at = $3
WHERE tenant_id = $1 AND id = $2::uuid AND revoked_at IS NULL
`, tenantID, tokenID, now.UTC())
	return err
}

func (s *PostgresStore) RevokeSessionToken(ctx context.Context, tenantID int64, tokenHash []byte, now time.Time) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	tag, err := s.db.Exec(ctx, `
UPDATE session_tokens
SET revoked_at = COALESCE(revoked_at, $3)
WHERE tenant_id = $1 AND token_hash = $2 AND revoked_at IS NULL
`, tenantID, tokenHash, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTokenNotFound
	}
	if s.cache != nil {
		_ = s.cache.RevokeSessionToken(ctx, tenantID, tokenHash, now)
	}
	return nil
}

func (s *PostgresStore) InsertRefreshToken(ctx context.Context, token RefreshToken) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	_, err := s.db.Exec(ctx, `
INSERT INTO refresh_tokens (id, tenant_id, family_id, token_hash, generation, status, expires_at, created_at)
VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7, $8)
`, token.ID, token.TenantID, token.FamilyID, token.TokenHash, token.Generation, token.Status, token.ExpiresAt.UTC(), token.CreatedAt.UTC())
	if err == nil && s.cache != nil {
		_ = s.cache.InsertRefreshToken(ctx, token)
	}
	return err
}

func (s *PostgresStore) LookupRefreshToken(ctx context.Context, tokenHash []byte) (RefreshRecord, error) {
	if s == nil || s.db == nil {
		return RefreshRecord{}, ErrStoreNotConfigured
	}
	const q = `
SELECT rt.id::text, rt.tenant_id, rt.family_id::text, rt.token_hash, rt.generation, rt.status,
       rt.expires_at, rt.created_at, rt.consumed_at,
       sf.id::text, sf.tenant_id, sf.user_id, sf.status, sf.generation, sf.created_at,
       sf.last_active_at, sf.device_info, sf.ip_baseline, sf.revoked_at, sf.revoked_reason
FROM refresh_tokens rt
INNER JOIN session_families sf ON sf.id = rt.family_id AND sf.tenant_id = rt.tenant_id
WHERE rt.token_hash = $1`
	rec, err := scanRefreshRecord(s.db.QueryRow(ctx, q, tokenHash))
	if err == nil {
		if s.cache != nil {
			s.cache.putRecord(rec)
		}
		return rec, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if s.cache != nil {
			s.cache.deleteRefreshTokenHash(tokenHash)
		}
		return RefreshRecord{}, ErrTokenNotFound
	}
	if s.cache != nil {
		return s.cache.LookupRefreshToken(ctx, tokenHash)
	}
	return RefreshRecord{}, err
}

func (s *PostgresStore) RotateRefreshToken(ctx context.Context, oldToken RefreshToken, newToken RefreshToken, now time.Time) (SessionFamily, error) {
	if s == nil || s.db == nil {
		return SessionFamily{}, ErrStoreNotConfigured
	}
	if beginner, ok := s.db.(txBeginner); ok {
		tx, err := beginner.Begin(ctx)
		if err != nil {
			return SessionFamily{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		family, err := rotateWithDB(ctx, tx, oldToken, newToken, now)
		if err != nil {
			return SessionFamily{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return SessionFamily{}, err
		}
		if s.cache != nil {
			_, _ = s.cache.RotateRefreshToken(ctx, oldToken, newToken, now)
			s.cache.putFamily(family)
		}
		return family, nil
	}
	family, err := rotateWithDB(ctx, s.db, oldToken, newToken, now)
	if err == nil && s.cache != nil {
		_, _ = s.cache.RotateRefreshToken(ctx, oldToken, newToken, now)
		s.cache.putFamily(family)
	}
	return family, err
}

func rotateWithDB(ctx context.Context, database db.DBTX, oldToken RefreshToken, newToken RefreshToken, now time.Time) (SessionFamily, error) {
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
  AND status IN ('active', 'suspicious')
RETURNING id::text, tenant_id, user_id, status, generation, created_at, last_active_at,
          device_info, ip_baseline, revoked_at, revoked_reason`
	family, err := scanFamily(database.QueryRow(ctx, q, oldToken.FamilyID, oldToken.TenantID, newToken.Generation, now.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionFamily{}, ErrFamilyRevoked
	}
	return family, err
}

func (s *PostgresStore) RevokeToken(ctx context.Context, tenantID int64, tokenHash []byte, reason string, now time.Time) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	_, err := s.db.Exec(ctx, `
UPDATE refresh_tokens
SET status = 'revoked',
    consumed_at = COALESCE(consumed_at, $3)
WHERE tenant_id = $1 AND token_hash = $2 AND status = 'active'
`, tenantID, tokenHash, now.UTC())
	if err == nil && s.cache != nil {
		_ = s.cache.RevokeToken(ctx, tenantID, tokenHash, reason, now)
	}
	return err
}

func (s *PostgresStore) RevokeFamily(ctx context.Context, tenantID int64, familyID, reason string, now time.Time) (SessionFamily, error) {
	if s == nil || s.db == nil {
		return SessionFamily{}, ErrStoreNotConfigured
	}
	const q = `
UPDATE session_families
SET status = 'revoked',
    revoked_at = $4,
    revoked_reason = NULLIF($3, ''),
    last_active_at = $4
WHERE tenant_id = $1 AND id = $2::uuid
RETURNING id::text, tenant_id, user_id, status, generation, created_at, last_active_at,
          device_info, ip_baseline, revoked_at, revoked_reason`
	family, err := scanFamily(s.db.QueryRow(ctx, q, tenantID, familyID, strings.TrimSpace(reason), now.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionFamily{}, ErrFamilyNotFound
	}
	if err != nil {
		return SessionFamily{}, err
	}
	_, err = s.db.Exec(ctx, `
UPDATE refresh_tokens
SET status = 'revoked',
    consumed_at = COALESCE(consumed_at, $3)
WHERE tenant_id = $1 AND family_id = $2::uuid AND status = 'active'
`, tenantID, familyID, now.UTC())
	if err == nil {
		_, err = s.db.Exec(ctx, `
UPDATE session_tokens
SET revoked_at = COALESCE(revoked_at, $3)
WHERE tenant_id = $1 AND family_id = $2::uuid AND revoked_at IS NULL
`, tenantID, familyID, now.UTC())
	}
	if err == nil && s.cache != nil {
		_, _ = s.cache.RevokeFamily(ctx, tenantID, familyID, reason, now)
		s.cache.putFamily(family)
	}
	return family, err
}

func (s *PostgresStore) RevokeUser(ctx context.Context, tenantID, userID int64, reason string, now time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, ErrStoreNotConfigured
	}
	tag, err := s.db.Exec(ctx, `
UPDATE session_families
SET status = 'revoked',
    revoked_at = $4,
    revoked_reason = NULLIF($3, ''),
    last_active_at = $4
WHERE tenant_id = $1
  AND user_id = $2
  AND status IN ('active', 'suspicious')
`, tenantID, userID, strings.TrimSpace(reason), now.UTC())
	if err != nil {
		return 0, err
	}
	_, err = s.db.Exec(ctx, `
UPDATE refresh_tokens rt
SET status = 'revoked',
    consumed_at = COALESCE(consumed_at, $3)
FROM session_families sf
WHERE rt.family_id = sf.id
  AND rt.tenant_id = $1
  AND sf.user_id = $2
  AND rt.status = 'active'
`, tenantID, userID, now.UTC())
	if err == nil {
		_, err = s.db.Exec(ctx, `
UPDATE session_tokens st
SET revoked_at = COALESCE(revoked_at, $3)
FROM session_families sf
WHERE st.family_id = sf.id
  AND st.tenant_id = $1
  AND sf.user_id = $2
  AND st.revoked_at IS NULL
`, tenantID, userID, now.UTC())
	}
	if err == nil && s.cache != nil {
		_, _ = s.cache.RevokeUser(ctx, tenantID, userID, reason, now)
	}
	return tag.RowsAffected(), err
}

func (s *PostgresStore) ListFamilies(ctx context.Context, tenantID, userID int64) ([]SessionFamily, error) {
	if s == nil || s.db == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.db.Query(ctx, `
SELECT id::text, tenant_id, user_id, status, generation, created_at, last_active_at,
       device_info, ip_baseline, revoked_at, revoked_reason
FROM session_families
WHERE tenant_id = $1 AND user_id = $2
ORDER BY last_active_at DESC
`, tenantID, userID)
	if err != nil {
		if s.cache != nil {
			return s.cache.ListFamilies(ctx, tenantID, userID)
		}
		return nil, err
	}
	defer rows.Close()
	var out []SessionFamily
	for rows.Next() {
		family, err := scanFamily(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, family)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type MemoryStore struct {
	mu            sync.Mutex
	families      map[string]SessionFamily
	tokens        map[string]RefreshToken
	byHash        map[string]string
	sessionTokens map[string]SessionToken
	sessionByHash map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		families:      make(map[string]SessionFamily),
		tokens:        make(map[string]RefreshToken),
		byHash:        make(map[string]string),
		sessionTokens: make(map[string]SessionToken),
		sessionByHash: make(map[string]string),
	}
}

func (s *MemoryStore) CreateFamily(_ context.Context, in CreateInput, now time.Time) (SessionFamily, error) {
	if s == nil {
		return SessionFamily{}, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 || in.UserID <= 0 {
		return SessionFamily{}, ErrInvalidInput
	}
	family := SessionFamily{
		ID:           uuid.NewString(),
		TenantID:     in.TenantID,
		UserID:       in.UserID,
		Status:       FamilyStatusActive,
		Generation:   1,
		CreatedAt:    now.UTC(),
		LastActiveAt: now.UTC(),
		DeviceInfo:   normalizeDeviceInfo(in.DeviceInfo, in.UserAgent),
		IPBaseline:   IPClass(in.IP),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.families[family.ID] = family
	return family, nil
}

func (s *MemoryStore) InsertRefreshToken(_ context.Context, token RefreshToken) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token.ID] = token
	s.byHash[hashKey(token.TokenHash)] = token.ID
	return nil
}

func (s *MemoryStore) InsertSessionToken(_ context.Context, token SessionToken) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionTokens[token.ID] = token
	s.sessionByHash[hashKey(token.TokenHash)] = token.ID
	return nil
}

func (s *MemoryStore) LookupSessionToken(_ context.Context, tokenHash []byte) (SessionRecord, error) {
	if s == nil {
		return SessionRecord{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tokenID, ok := s.sessionByHash[hashKey(tokenHash)]
	if !ok {
		return SessionRecord{}, ErrTokenNotFound
	}
	token := s.sessionTokens[tokenID]
	family, ok := s.families[token.FamilyID]
	if !ok {
		return SessionRecord{}, ErrFamilyNotFound
	}
	return SessionRecord{Token: token, Family: family}, nil
}

func (s *MemoryStore) TouchSessionToken(_ context.Context, tenantID int64, tokenID string, now time.Time) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.sessionTokens[tokenID]
	if !ok || token.TenantID != tenantID {
		return ErrTokenNotFound
	}
	t := now.UTC()
	token.LastUsedAt = &t
	s.sessionTokens[token.ID] = token
	return nil
}

func (s *MemoryStore) RevokeSessionToken(_ context.Context, tenantID int64, tokenHash []byte, now time.Time) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tokenID, ok := s.sessionByHash[hashKey(tokenHash)]
	if !ok {
		return ErrTokenNotFound
	}
	token := s.sessionTokens[tokenID]
	if token.TenantID != tenantID {
		return ErrTokenNotFound
	}
	t := now.UTC()
	token.RevokedAt = &t
	s.sessionTokens[token.ID] = token
	return nil
}

func (s *MemoryStore) LookupRefreshToken(_ context.Context, tokenHash []byte) (RefreshRecord, error) {
	if s == nil {
		return RefreshRecord{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tokenID, ok := s.byHash[hashKey(tokenHash)]
	if !ok {
		return RefreshRecord{}, ErrTokenNotFound
	}
	token := s.tokens[tokenID]
	family, ok := s.families[token.FamilyID]
	if !ok {
		return RefreshRecord{}, ErrFamilyNotFound
	}
	return RefreshRecord{Token: token, Family: family}, nil
}

func (s *MemoryStore) RotateRefreshToken(_ context.Context, oldToken RefreshToken, newToken RefreshToken, now time.Time) (SessionFamily, error) {
	if s == nil {
		return SessionFamily{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.tokens[oldToken.ID]
	if !ok || current.Status != RefreshTokenStatusActive {
		return SessionFamily{}, ErrRefreshReplay
	}
	current.Status = RefreshTokenStatusConsumed
	t := now.UTC()
	current.ConsumedAt = &t
	s.tokens[current.ID] = current
	s.tokens[newToken.ID] = newToken
	s.byHash[hashKey(newToken.TokenHash)] = newToken.ID
	family, ok := s.families[oldToken.FamilyID]
	if !ok {
		return SessionFamily{}, ErrFamilyNotFound
	}
	if family.Status != FamilyStatusActive && family.Status != FamilyStatusSuspicious {
		return SessionFamily{}, ErrFamilyRevoked
	}
	family.Generation = newToken.Generation
	family.LastActiveAt = t
	s.families[family.ID] = family
	return family, nil
}

func (s *MemoryStore) RevokeToken(_ context.Context, tenantID int64, tokenHash []byte, _ string, now time.Time) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tokenID, ok := s.byHash[hashKey(tokenHash)]
	if !ok {
		return ErrTokenNotFound
	}
	token := s.tokens[tokenID]
	if token.TenantID != tenantID {
		return ErrTokenNotFound
	}
	token.Status = RefreshTokenStatusRevoked
	t := now.UTC()
	token.ConsumedAt = &t
	s.tokens[token.ID] = token
	return nil
}

func (s *MemoryStore) RevokeFamily(_ context.Context, tenantID int64, familyID, reason string, now time.Time) (SessionFamily, error) {
	if s == nil {
		return SessionFamily{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	family, ok := s.families[familyID]
	if !ok || family.TenantID != tenantID {
		return SessionFamily{}, ErrFamilyNotFound
	}
	t := now.UTC()
	family.Status = FamilyStatusRevoked
	family.RevokedAt = &t
	family.RevokedReason = strings.TrimSpace(reason)
	family.LastActiveAt = t
	s.families[family.ID] = family
	for id, token := range s.tokens {
		if token.FamilyID == familyID && token.TenantID == tenantID && token.Status == RefreshTokenStatusActive {
			token.Status = RefreshTokenStatusRevoked
			token.ConsumedAt = &t
			s.tokens[id] = token
		}
	}
	for id, token := range s.sessionTokens {
		if token.FamilyID == familyID && token.TenantID == tenantID && token.RevokedAt == nil {
			token.RevokedAt = &t
			s.sessionTokens[id] = token
		}
	}
	return family, nil
}

func (s *MemoryStore) RevokeUser(_ context.Context, tenantID, userID int64, reason string, now time.Time) (int64, error) {
	if s == nil {
		return 0, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := now.UTC()
	var count int64
	for id, family := range s.families {
		if family.TenantID == tenantID && family.UserID == userID && (family.Status == FamilyStatusActive || family.Status == FamilyStatusSuspicious) {
			family.Status = FamilyStatusRevoked
			family.RevokedAt = &t
			family.RevokedReason = strings.TrimSpace(reason)
			family.LastActiveAt = t
			s.families[id] = family
			count++
		}
	}
	for id, token := range s.tokens {
		family := s.families[token.FamilyID]
		if family.TenantID == tenantID && family.UserID == userID && token.Status == RefreshTokenStatusActive {
			token.Status = RefreshTokenStatusRevoked
			token.ConsumedAt = &t
			s.tokens[id] = token
		}
	}
	for id, token := range s.sessionTokens {
		family := s.families[token.FamilyID]
		if family.TenantID == tenantID && family.UserID == userID && token.RevokedAt == nil {
			token.RevokedAt = &t
			s.sessionTokens[id] = token
		}
	}
	return count, nil
}

func (s *MemoryStore) ListFamilies(_ context.Context, tenantID, userID int64) ([]SessionFamily, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionFamily, 0)
	for _, family := range s.families {
		if family.TenantID == tenantID && family.UserID == userID {
			out = append(out, family)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastActiveAt.After(out[j].LastActiveAt)
	})
	return out, nil
}

func (s *MemoryStore) putRecord(rec RefreshRecord) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.families[rec.Family.ID] = rec.Family
	s.tokens[rec.Token.ID] = rec.Token
	s.byHash[hashKey(rec.Token.TokenHash)] = rec.Token.ID
}

func (s *MemoryStore) putFamily(family SessionFamily) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.families[family.ID] = family
}

func (s *MemoryStore) putSessionRecord(rec SessionRecord) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.families[rec.Family.ID] = rec.Family
	s.sessionTokens[rec.Token.ID] = rec.Token
	s.sessionByHash[hashKey(rec.Token.TokenHash)] = rec.Token.ID
}

func (s *MemoryStore) deleteSessionTokenHash(tokenHash []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hashKey(tokenHash)
	if tokenID, ok := s.sessionByHash[key]; ok {
		delete(s.sessionTokens, tokenID)
	}
	delete(s.sessionByHash, key)
}

func (s *MemoryStore) deleteRefreshTokenHash(tokenHash []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hashKey(tokenHash)
	if tokenID, ok := s.byHash[key]; ok {
		delete(s.tokens, tokenID)
	}
	delete(s.byHash, key)
}

func scanRefreshRecord(row pgx.Row) (RefreshRecord, error) {
	var rec RefreshRecord
	var consumedAt, revokedAt pgtype.Timestamptz
	var revokedReason pgtype.Text
	var tokenStatus, familyStatus string
	var deviceInfo []byte
	if err := row.Scan(
		&rec.Token.ID,
		&rec.Token.TenantID,
		&rec.Token.FamilyID,
		&rec.Token.TokenHash,
		&rec.Token.Generation,
		&tokenStatus,
		&rec.Token.ExpiresAt,
		&rec.Token.CreatedAt,
		&consumedAt,
		&rec.Family.ID,
		&rec.Family.TenantID,
		&rec.Family.UserID,
		&familyStatus,
		&rec.Family.Generation,
		&rec.Family.CreatedAt,
		&rec.Family.LastActiveAt,
		&deviceInfo,
		&rec.Family.IPBaseline,
		&revokedAt,
		&revokedReason,
	); err != nil {
		return RefreshRecord{}, err
	}
	rec.Token.Status = RefreshTokenStatus(tokenStatus)
	rec.Family.Status = FamilyStatus(familyStatus)
	if consumedAt.Valid {
		t := consumedAt.Time
		rec.Token.ConsumedAt = &t
	}
	if revokedAt.Valid {
		t := revokedAt.Time
		rec.Family.RevokedAt = &t
	}
	if revokedReason.Valid {
		rec.Family.RevokedReason = revokedReason.String
	}
	if len(deviceInfo) > 0 {
		_ = json.Unmarshal(deviceInfo, &rec.Family.DeviceInfo)
	}
	if rec.Family.DeviceInfo == nil {
		rec.Family.DeviceInfo = map[string]any{}
	}
	return rec, nil
}

func scanSessionRecord(row pgx.Row) (SessionRecord, error) {
	var rec SessionRecord
	var lastUsedAt, tokenRevokedAt, familyRevokedAt pgtype.Timestamptz
	var revokedReason pgtype.Text
	var familyStatus string
	var deviceInfo []byte
	if err := row.Scan(
		&rec.Token.ID,
		&rec.Token.TenantID,
		&rec.Token.FamilyID,
		&rec.Token.TokenHash,
		&rec.Token.Generation,
		&rec.Token.ExpiresAt,
		&rec.Token.CreatedAt,
		&lastUsedAt,
		&tokenRevokedAt,
		&rec.Family.ID,
		&rec.Family.TenantID,
		&rec.Family.UserID,
		&familyStatus,
		&rec.Family.Generation,
		&rec.Family.CreatedAt,
		&rec.Family.LastActiveAt,
		&deviceInfo,
		&rec.Family.IPBaseline,
		&familyRevokedAt,
		&revokedReason,
	); err != nil {
		return SessionRecord{}, err
	}
	rec.Family.Status = FamilyStatus(familyStatus)
	if lastUsedAt.Valid {
		t := lastUsedAt.Time
		rec.Token.LastUsedAt = &t
	}
	if tokenRevokedAt.Valid {
		t := tokenRevokedAt.Time
		rec.Token.RevokedAt = &t
	}
	if familyRevokedAt.Valid {
		t := familyRevokedAt.Time
		rec.Family.RevokedAt = &t
	}
	if revokedReason.Valid {
		rec.Family.RevokedReason = revokedReason.String
	}
	if len(deviceInfo) > 0 {
		_ = json.Unmarshal(deviceInfo, &rec.Family.DeviceInfo)
	}
	if rec.Family.DeviceInfo == nil {
		rec.Family.DeviceInfo = map[string]any{}
	}
	return rec, nil
}

func scanFamily(row pgx.Row) (SessionFamily, error) {
	var out SessionFamily
	var status string
	var deviceInfo []byte
	var revokedAt pgtype.Timestamptz
	var revokedReason pgtype.Text
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.UserID,
		&status,
		&out.Generation,
		&out.CreatedAt,
		&out.LastActiveAt,
		&deviceInfo,
		&out.IPBaseline,
		&revokedAt,
		&revokedReason,
	); err != nil {
		return SessionFamily{}, err
	}
	out.Status = FamilyStatus(status)
	if len(deviceInfo) > 0 {
		_ = json.Unmarshal(deviceInfo, &out.DeviceInfo)
	}
	if out.DeviceInfo == nil {
		out.DeviceInfo = map[string]any{}
	}
	if revokedAt.Valid {
		t := revokedAt.Time
		out.RevokedAt = &t
	}
	if revokedReason.Valid {
		out.RevokedReason = revokedReason.String
	}
	return out, nil
}

func normalizeDeviceInfo(in map[string]any, userAgent string) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	if ua := UserAgentClass(userAgent); ua != "" {
		out["ua_class"] = ua
	}
	return out
}

func hashKey(hash []byte) string {
	return base64.RawStdEncoding.EncodeToString(hash)
}
