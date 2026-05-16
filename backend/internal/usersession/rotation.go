package usersession

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultSessionTTL = 15 * time.Minute
	DefaultRefreshTTL = 30 * 24 * time.Hour
)

type Service struct {
	Store             Store
	SessionTTL        time.Duration
	RefreshTTL        time.Duration
	SigningKey        []byte
	MaxActiveFamilies int
	DevicePolicy      string
	Now               func() time.Time
}

func NewService(store Store) *Service {
	return &Service{
		Store:      store,
		SessionTTL: DefaultSessionTTL,
		RefreshTTL: DefaultRefreshTTL,
		Now:        time.Now,
	}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (IssuedTokens, error) {
	if s == nil || s.Store == nil {
		return IssuedTokens{}, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 || in.UserID <= 0 {
		return IssuedTokens{}, ErrInvalidInput
	}
	if err := s.enforceDevicePolicy(ctx, in.TenantID, in.UserID); err != nil {
		return IssuedTokens{}, err
	}
	now := s.now()
	family, err := s.Store.CreateFamily(ctx, in, now)
	if err != nil {
		return IssuedTokens{}, err
	}
	refreshRaw, refreshHash, err := GenerateRefreshToken()
	if err != nil {
		return IssuedTokens{}, err
	}
	refreshTTL := firstDuration(in.RefreshTTL, s.RefreshTTL, DefaultRefreshTTL)
	token := RefreshToken{
		ID:         uuid.NewString(),
		TenantID:   in.TenantID,
		FamilyID:   family.ID,
		TokenHash:  refreshHash,
		Generation: family.Generation,
		Status:     RefreshTokenStatusActive,
		ExpiresAt:  now.Add(refreshTTL),
		CreatedAt:  now,
	}
	if err := s.Store.InsertRefreshToken(ctx, token); err != nil {
		return IssuedTokens{}, err
	}
	return s.issuePair(ctx, family, refreshRaw, token.ExpiresAt, firstDuration(in.SessionTTL, s.SessionTTL, DefaultSessionTTL))
}

func (s *Service) Refresh(ctx context.Context, in RefreshInput) (IssuedTokens, error) {
	if s == nil || s.Store == nil {
		return IssuedTokens{}, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 || strings.TrimSpace(in.RefreshToken) == "" {
		return IssuedTokens{}, ErrInvalidInput
	}
	now := s.now()
	rec, err := s.Store.LookupRefreshToken(ctx, HashRefreshToken(in.RefreshToken))
	if err != nil {
		return IssuedTokens{}, err
	}
	if rec.Token.TenantID != in.TenantID || rec.Family.TenantID != in.TenantID {
		return IssuedTokens{}, ErrTokenNotFound
	}
	if rec.Family.Status != FamilyStatusActive && rec.Family.Status != FamilyStatusSuspicious {
		return IssuedTokens{}, ErrFamilyRevoked
	}
	if rec.Token.Status != RefreshTokenStatusActive {
		_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, "refresh_replay", now)
		return IssuedTokens{}, ErrRefreshReplay
	}
	if !rec.Token.ExpiresAt.After(now) {
		_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, "refresh_expired", now)
		return IssuedTokens{}, ErrTokenExpired
	}
	if drift := DetectDrift(rec.Family, in.IP, in.UserAgent); drift.Level == DriftHigh {
		_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, drift.Reason, now)
		return IssuedTokens{}, ErrAnomalyRejected
	}
	refreshRaw, refreshHash, err := GenerateRefreshToken()
	if err != nil {
		return IssuedTokens{}, err
	}
	newToken := RefreshToken{
		ID:         uuid.NewString(),
		TenantID:   rec.Token.TenantID,
		FamilyID:   rec.Token.FamilyID,
		TokenHash:  refreshHash,
		Generation: rec.Token.Generation + 1,
		Status:     RefreshTokenStatusActive,
		ExpiresAt:  now.Add(firstDuration(in.RefreshTTL, s.RefreshTTL, DefaultRefreshTTL)),
		CreatedAt:  now,
	}
	family, err := s.Store.RotateRefreshToken(ctx, rec.Token, newToken, now)
	if err != nil {
		if err == ErrRefreshReplay {
			_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, "refresh_race_or_replay", now)
		}
		return IssuedTokens{}, err
	}
	return s.issuePair(ctx, family, refreshRaw, newToken.ExpiresAt, firstDuration(in.SessionTTL, s.SessionTTL, DefaultSessionTTL))
}

func GenerateRefreshToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := "husr_" + base64.RawURLEncoding.EncodeToString(raw)
	return token, HashRefreshToken(token), nil
}

func HashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return sum[:]
}

func HashSessionToken(token string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return sum[:]
}

type signedSessionPayload struct {
	ID         string `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	UserID     int64  `json:"user_id"`
	FamilyID   string `json:"family_id"`
	Generation int    `json:"generation"`
	ExpiresAt  int64  `json:"exp"`
}

func (s *Service) issuePair(ctx context.Context, family SessionFamily, refreshRaw string, refreshExpires time.Time, sessionTTL time.Duration) (IssuedTokens, error) {
	if len(s.SigningKey) < 32 {
		return IssuedTokens{}, ErrSigningKeyMissing
	}
	now := s.now()
	tokenID := uuid.NewString()
	sessionExpiry := now.Add(sessionTTL)
	payload := signedSessionPayload{
		ID: tokenID, TenantID: family.TenantID, UserID: family.UserID,
		FamilyID: family.ID, Generation: family.Generation, ExpiresAt: sessionExpiry.Unix(),
	}
	sessionToken, err := s.signPayload(payload)
	if err != nil {
		return IssuedTokens{}, err
	}
	if err := s.Store.InsertSessionToken(ctx, SessionToken{
		ID: tokenID, TenantID: family.TenantID, FamilyID: family.ID,
		TokenHash: HashSessionToken(sessionToken), Generation: family.Generation,
		ExpiresAt: sessionExpiry, CreatedAt: now,
	}); err != nil {
		return IssuedTokens{}, err
	}
	return IssuedTokens{
		SessionToken:  sessionToken,
		RefreshToken:  refreshRaw,
		SessionExpiry: sessionExpiry,
		RefreshExpiry: refreshExpires,
		Family:        family,
		Generation:    family.Generation,
	}, nil
}

func (s *Service) Validate(ctx context.Context, token string, ip string, userAgent string) (ValidatedSession, error) {
	if s == nil || s.Store == nil {
		return ValidatedSession{}, ErrStoreNotConfigured
	}
	payload, err := s.verifyPayload(token)
	if err != nil {
		return ValidatedSession{}, err
	}
	now := s.now()
	if !time.Unix(payload.ExpiresAt, 0).After(now) {
		return ValidatedSession{}, ErrTokenExpired
	}
	rec, err := s.Store.LookupSessionToken(ctx, HashSessionToken(token))
	if err != nil {
		return ValidatedSession{}, err
	}
	if rec.Token.RevokedAt != nil {
		return ValidatedSession{}, ErrFamilyRevoked
	}
	if rec.Token.TenantID != payload.TenantID || rec.Family.TenantID != payload.TenantID || rec.Family.UserID != payload.UserID ||
		rec.Token.FamilyID != payload.FamilyID || rec.Family.ID != payload.FamilyID || rec.Token.Generation != payload.Generation {
		return ValidatedSession{}, ErrTokenNotFound
	}
	if !rec.Token.ExpiresAt.After(now) {
		return ValidatedSession{}, ErrTokenExpired
	}
	if rec.Family.Status != FamilyStatusActive && rec.Family.Status != FamilyStatusSuspicious {
		return ValidatedSession{}, ErrFamilyRevoked
	}
	if rec.Family.Generation != payload.Generation {
		return ValidatedSession{}, ErrTokenNotFound
	}
	if drift := DetectDrift(rec.Family, ip, userAgent); drift.Level == DriftHigh {
		_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, drift.Reason, now)
		return ValidatedSession{}, ErrAnomalyRejected
	}
	_ = s.Store.TouchSessionToken(ctx, rec.Token.TenantID, rec.Token.ID, now)
	return ValidatedSession{
		TenantID: payload.TenantID, UserID: payload.UserID, FamilyID: payload.FamilyID,
		TokenID: payload.ID, Generation: payload.Generation, ExpiresAt: rec.Token.ExpiresAt,
	}, nil
}

func (s *Service) signPayload(payload signedSessionPayload) (string, error) {
	if len(s.SigningKey) < 32 {
		return "", ErrSigningKeyMissing
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, s.SigningKey)
	_, _ = mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return "hus_" + encoded + "." + sig, nil
}

func (s *Service) verifyPayload(token string) (signedSessionPayload, error) {
	if len(s.SigningKey) < 32 {
		return signedSessionPayload{}, ErrSigningKeyMissing
	}
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "hus_") {
		return signedSessionPayload{}, ErrTokenNotFound
	}
	rest := strings.TrimPrefix(token, "hus_")
	encoded, sig, ok := strings.Cut(rest, ".")
	if !ok || encoded == "" || sig == "" {
		return signedSessionPayload{}, ErrTokenNotFound
	}
	mac := hmac.New(sha256.New, s.SigningKey)
	_, _ = mac.Write([]byte(encoded))
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(actual, expected) {
		return signedSessionPayload{}, ErrTokenNotFound
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return signedSessionPayload{}, ErrTokenNotFound
	}
	var payload signedSessionPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return signedSessionPayload{}, ErrTokenNotFound
	}
	if payload.ID == "" || payload.TenantID <= 0 || payload.UserID <= 0 || payload.FamilyID == "" || payload.Generation <= 0 || payload.ExpiresAt <= 0 {
		return signedSessionPayload{}, ErrTokenNotFound
	}
	return payload, nil
}

func (s *Service) enforceDevicePolicy(ctx context.Context, tenantID, userID int64) error {
	if s.MaxActiveFamilies <= 0 {
		return nil
	}
	families, err := s.Store.ListFamilies(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	active := make([]SessionFamily, 0, len(families))
	for _, family := range families {
		if family.Status == FamilyStatusActive || family.Status == FamilyStatusSuspicious {
			active = append(active, family)
		}
	}
	if len(active) < s.MaxActiveFamilies {
		return nil
	}
	switch strings.TrimSpace(s.DevicePolicy) {
	case "revoke_oldest":
		oldest := active[0]
		for _, family := range active[1:] {
			if family.LastActiveAt.Before(oldest.LastActiveAt) {
				oldest = family
			}
		}
		_, err := s.Store.RevokeFamily(ctx, tenantID, oldest.ID, "device_limit_revoke_oldest", s.now())
		return err
	case "confirm":
		return ErrDeviceConfirmationRequired
	default:
		return ErrDeviceLimitExceeded
	}
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func firstDuration(values ...time.Duration) time.Duration {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}
