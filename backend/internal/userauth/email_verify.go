package userauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultEmailVerificationTTL = 24 * time.Hour
	DefaultPasswordResetTTL     = 30 * time.Minute
	DefaultOAuthFlowTTL         = 10 * time.Minute
	DefaultLockoutThreshold     = 5
)

func GenerateToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return sum[:]
}

func NewTokenChallenge(tenantID, userID int64, ttl time.Duration, now time.Time) (TokenChallenge, error) {
	raw, hash, err := GenerateToken()
	if err != nil {
		return TokenChallenge{}, err
	}
	return TokenChallenge{
		ID:        uuid.NewString(),
		TenantID:  tenantID,
		UserID:    userID,
		RawToken:  raw,
		TokenHash: hash,
		ExpiresAt: now.UTC().Add(ttl),
	}, nil
}

func (s *Service) StartEmailVerification(ctx context.Context, user User) (TokenChallenge, error) {
	if s == nil || s.Store == nil {
		return TokenChallenge{}, ErrStoreNotConfigured
	}
	return s.startEmailVerificationWithStore(ctx, s.Store, user)
}

func (s *Service) startEmailVerificationWithStore(ctx context.Context, store Store, user User) (TokenChallenge, error) {
	if store == nil {
		return TokenChallenge{}, ErrStoreNotConfigured
	}
	ttl := s.VerificationTTL
	if ttl <= 0 {
		ttl = DefaultEmailVerificationTTL
	}
	challenge, err := NewTokenChallenge(user.TenantID, user.ID, ttl, s.now())
	if err != nil {
		return TokenChallenge{}, err
	}
	if err := store.CreateEmailVerificationToken(ctx, challenge); err != nil {
		return TokenChallenge{}, err
	}
	return challenge, nil
}

func (s *Service) VerifyEmail(ctx context.Context, tenantID int64, token string) (User, error) {
	if s == nil || s.Store == nil {
		return User{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || strings.TrimSpace(token) == "" {
		return User{}, ErrInvalidInput
	}
	return s.Store.ConsumeEmailVerificationToken(ctx, tenantID, HashToken(token), s.now())
}
