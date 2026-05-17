package userauth

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Store interface {
	CreateUser(context.Context, CreateUserParams) (User, error)
	GetUserByEmail(context.Context, int64, string) (User, error)
	GetUserByID(context.Context, int64, int64) (User, error)
	MarkLoginSuccess(context.Context, int64, int64) error
	MarkLoginFailure(context.Context, int64, int64, int) error
	GetUserBySocialIdentity(context.Context, int64, string, string) (User, error)
	LinkSocialIdentity(context.Context, int64, int64, string, string) (User, error)
	CreateEmailVerificationToken(context.Context, TokenChallenge) error
	ConsumeEmailVerificationToken(context.Context, int64, []byte, time.Time) (User, error)
	CreatePasswordResetToken(context.Context, TokenChallenge, int) error
	ConsumePasswordResetToken(context.Context, int64, []byte, string, time.Time) (User, error)
	RedeemInvite(context.Context, int64, string, time.Time) (InviteCode, error)
	CreateInviteBinding(context.Context, int64, int64, string, time.Time) error
	CreateOAuthFlowSession(context.Context, OAuthFlowChallenge) error
	ConsumeOAuthFlowSession(context.Context, int64, string, []byte, time.Time) (OAuthFlowSession, error)
}

type EmailVerificationPolicy interface {
	EmailVerificationEnabled(context.Context, int64) (bool, error)
}

type Service struct {
	Store            Store
	PasswordPolicy   PasswordPolicy
	VerificationTTL  time.Duration
	PasswordResetTTL time.Duration
	OAuthFlowTTL     time.Duration
	InviteRequired   bool
	RequireVerified  bool
	SocialSignup     bool
	LockoutThreshold int
	Now              func() time.Time
	OAuth            *OAuthService
	Verification     EmailVerificationPolicy
}

func NewService(store Store) *Service {
	return &Service{
		Store:            store,
		PasswordPolicy:   DefaultPasswordPolicy(),
		VerificationTTL:  DefaultEmailVerificationTTL,
		PasswordResetTTL: DefaultPasswordResetTTL,
		OAuthFlowTTL:     DefaultOAuthFlowTTL,
		RequireVerified:  true,
		SocialSignup:     true,
		LockoutThreshold: DefaultLockoutThreshold,
		Now:              time.Now,
	}
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *Service) Register(ctx context.Context, in RegisterInput) (RegistrationResult, error) {
	if s == nil || s.Store == nil {
		return RegistrationResult{}, ErrStoreNotConfigured
	}
	email := NormalizeEmail(in.Email)
	if in.TenantID <= 0 || email == "" || strings.TrimSpace(in.Password) == "" {
		return RegistrationResult{}, ErrInvalidInput
	}
	passwordHash, err := HashPassword(in.Password, s.PasswordPolicy)
	if err != nil {
		return RegistrationResult{}, err
	}
	var out RegistrationResult
	requireVerification := s.requireEmailVerification(ctx, in.TenantID)
	if err := s.withStoreTx(ctx, func(store Store) error {
		var inviteHash string
		if strings.TrimSpace(in.InviteCode) != "" {
			invite, err := store.RedeemInvite(ctx, in.TenantID, in.InviteCode, s.now())
			if err != nil {
				return err
			}
			inviteHash = invite.Code
		} else if s.InviteRequired {
			return ErrInviteRequired
		}
		status := UserStatusActive
		if requireVerification {
			status = UserStatusPendingVerification
		}
		user, err := store.CreateUser(ctx, CreateUserParams{
			TenantID:       in.TenantID,
			Email:          email,
			DisplayName:    strings.TrimSpace(in.DisplayName),
			PasswordHash:   passwordHash,
			EmailVerified:  !requireVerification,
			InviteCodeUsed: inviteHash,
			Status:         status,
		})
		if err != nil {
			return err
		}
		if inviteHash != "" {
			if err := store.CreateInviteBinding(ctx, user.TenantID, user.ID, inviteHash, s.now()); err != nil {
				return err
			}
		}
		var token string
		if requireVerification {
			challenge, err := s.startEmailVerificationWithStore(ctx, store, user)
			if err != nil {
				return err
			}
			token = challenge.RawToken
		}
		out = RegistrationResult{User: user, VerificationToken: token}
		return nil
	}); err != nil {
		return RegistrationResult{}, err
	}
	return out, nil
}

func (s *Service) Authenticate(ctx context.Context, in LoginInput) (User, error) {
	if s == nil || s.Store == nil {
		return User{}, ErrStoreNotConfigured
	}
	email := NormalizeEmail(in.Email)
	if in.TenantID <= 0 || email == "" || strings.TrimSpace(in.Password) == "" {
		return User{}, ErrInvalidInput
	}
	user, err := s.Store.GetUserByEmail(ctx, in.TenantID, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	if user.Status == UserStatusDisabled || user.Status == UserStatusDeleted {
		return User{}, ErrUserDisabled
	}
	if user.Status == UserStatusResetRequired {
		return User{}, ErrPasswordResetRequired
	}
	threshold := s.lockoutThreshold()
	if user.Status == UserStatusLocked || user.FailedLoginCount >= threshold || (user.LockedUntil != nil && s.now().Before(*user.LockedUntil)) {
		_ = s.Store.MarkLoginFailure(ctx, user.TenantID, user.ID, threshold)
		return User{}, ErrUserLocked
	}
	if s.requireEmailVerification(ctx, in.TenantID) && !user.EmailVerified {
		return User{}, ErrEmailUnverified
	}
	if user.PasswordHash == "" {
		return User{}, ErrInvalidCredentials
	}
	ok, verifyErr := VerifyPassword(user.PasswordHash, in.Password)
	if verifyErr != nil || !ok {
		_ = s.Store.MarkLoginFailure(ctx, user.TenantID, user.ID, threshold)
		return User{}, ErrInvalidCredentials
	}
	_ = s.Store.MarkLoginSuccess(ctx, user.TenantID, user.ID)
	return user, nil
}

type txCapableStore interface {
	WithTx(context.Context, func(Store) error) error
}

func (s *Service) withStoreTx(ctx context.Context, fn func(Store) error) error {
	if txStore, ok := s.Store.(txCapableStore); ok {
		return txStore.WithTx(ctx, fn)
	}
	return fn(s.Store)
}

func (s *Service) lockoutThreshold() int {
	if s != nil && s.LockoutThreshold > 0 {
		return s.LockoutThreshold
	}
	return DefaultLockoutThreshold
}

func (s *Service) RequestPasswordReset(ctx context.Context, in PasswordResetRequest) (PasswordResetResult, error) {
	if s == nil || s.Store == nil {
		return PasswordResetResult{}, ErrStoreNotConfigured
	}
	email := NormalizeEmail(in.Email)
	if in.TenantID <= 0 || email == "" {
		return PasswordResetResult{}, ErrInvalidInput
	}
	user, err := s.Store.GetUserByEmail(ctx, in.TenantID, email)
	if errors.Is(err, ErrUserNotFound) {
		return PasswordResetResult{}, nil
	}
	if err != nil {
		return PasswordResetResult{}, err
	}
	ttl := s.PasswordResetTTL
	if ttl <= 0 {
		ttl = DefaultPasswordResetTTL
	}
	challenge, err := NewTokenChallenge(user.TenantID, user.ID, ttl, s.now())
	if err != nil {
		return PasswordResetResult{}, err
	}
	if err := s.Store.CreatePasswordResetToken(ctx, challenge, user.PasswordVersion); err != nil {
		return PasswordResetResult{}, err
	}
	return PasswordResetResult{UserID: user.ID, Token: challenge.RawToken}, nil
}

func (s *Service) ResetPassword(ctx context.Context, in PasswordResetConfirm) (User, error) {
	if s == nil || s.Store == nil {
		return User{}, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 || strings.TrimSpace(in.Token) == "" || strings.TrimSpace(in.NewPassword) == "" {
		return User{}, ErrInvalidInput
	}
	passwordHash, err := HashPassword(in.NewPassword, s.PasswordPolicy)
	if err != nil {
		return User{}, err
	}
	return s.Store.ConsumePasswordResetToken(ctx, in.TenantID, HashToken(in.Token), passwordHash, s.now())
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) requireEmailVerification(ctx context.Context, tenantID int64) bool {
	if s != nil && s.Verification != nil {
		enabled, err := s.Verification.EmailVerificationEnabled(ctx, tenantID)
		if err == nil {
			return enabled
		}
	}
	return s == nil || s.RequireVerified
}
