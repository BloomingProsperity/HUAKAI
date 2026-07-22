package userauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
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

// ResendEmailVerification 为一个待验证(pending_verification 且未验证)用户重新签发验证 token。
// 防枚举:邮箱不存在、已验证、或非待验证状态时,一律返回 (零 User, "", found=false, nil)——调用方
// 对所有情形回同一种成功信封,绝不暴露该邮箱是否注册/是否已验证。仅当真为待验证用户时 found=true
// 并带出可发信的 user + rawToken。复用 startEmailVerificationWithStore(与注册同一 token 签发路径)。
func (s *Service) ResendEmailVerification(ctx context.Context, tenantID int64, rawEmail string) (User, string, bool, error) {
	if s == nil || s.Store == nil {
		return User{}, "", false, ErrStoreNotConfigured
	}
	email := NormalizeEmail(rawEmail)
	if tenantID <= 0 || email == "" {
		// 明显非法入参(非枚举信号)才报错;存在性差异不经此暴露。
		return User{}, "", false, ErrInvalidInput
	}
	user, err := s.Store.GetUserByEmail(ctx, tenantID, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// 邮箱不存在:静默成功(不发信),防枚举。
			return User{}, "", false, nil
		}
		return User{}, "", false, err
	}
	// 仅待验证且未验证的用户才重发;已验证/其它状态一律静默成功不发信。
	if user.EmailVerified || user.Status != UserStatusPendingVerification {
		return User{}, "", false, nil
	}
	challenge, err := s.startEmailVerificationWithStore(ctx, s.Store, user)
	if err != nil {
		return User{}, "", false, err
	}
	return user, challenge.RawToken, true, nil
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
