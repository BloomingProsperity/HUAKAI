package userauth

import (
	"context"
	"errors"
	"strings"
	"time"
)

// verifyPasswordFn 是口令校验的可注入入口(生产即 VerifyPassword)。抽成变量是为了让测试
// 能观察「邮箱不存在 / 存在但无本地口令」路径是否也跑了一次等价 argon2 校验 —— 这是 S2-048 时序等工的核心。
var verifyPasswordFn = VerifyPassword

// timingEqualizationHash 是一个无人知晓口令的合法 argon2id hash(常量, 默认 policy 成本 m=64MiB,t=3)。
// 仅用于 Authenticate 在「邮箱不存在」或「存在但无本地口令(social-only)」时做等工量 argon2 校验:
// 否则「走 VerifyPassword 跑 argon2」与「直接返回」的响应时延差会泄露邮箱是否已注册(用户枚举侧信道, S2-048)。
// 硬编码常量而非运行时生成, 杜绝生成失败时 fail-open 成快速 parse-fail(S2-048 R1 codex 跟进)。
const timingEqualizationHash = "$argon2id$v=19$m=65536,t=3,p=1$0k8KjQ01TveJhg0daai5hw$m6FwG+zGw8X2YLWE1grPszMNN84IGcyd5xhFrEGMhIc"

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
	CreateCommunityReferral(context.Context, int64, int64, int64, int64) error
	CreateOAuthFlowSession(context.Context, OAuthFlowChallenge) error
	ConsumeOAuthFlowSession(context.Context, int64, string, []byte, time.Time) (OAuthFlowSession, error)
}

type passwordResetPreparationStore interface {
	PreparePasswordResetTokenUser(context.Context, int64, []byte, time.Time) (User, error)
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
	RegistrationMode RegistrationMode
	InviteRequired   bool
	RequireVerified  bool
	SocialSignup     bool
	LockoutThreshold int
	Now              func() time.Time
	OAuth            *OAuthService
	Verification     EmailVerificationPolicy
	// AllowedRedirectURIs 是 social OAuth init 允许的 caller redirect_uri 精确白名单(S2-009)。空(默认)=
	// 不接受任何 caller 提供的 redirect_uri,只能用各 provider 服务端配置的固定 RedirectURI,fail-closed 防
	// open-redirect / 授权码外泄。
	AllowedRedirectURIs []string
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
	mode, err := s.registrationMode()
	if err != nil {
		return RegistrationResult{}, err
	}
	if mode == RegistrationModeDisabled {
		return RegistrationResult{}, ErrRegistrationDisabled
	}
	passwordHash, err := HashPassword(in.Password, s.PasswordPolicy)
	if err != nil {
		return RegistrationResult{}, err
	}
	var out RegistrationResult
	requireVerification := s.requireEmailVerification(ctx, in.TenantID)
	if err := s.withStoreTx(ctx, func(store Store) error {
		var inviteHash string
		var redeemedInvite InviteCode
		if strings.TrimSpace(in.InviteCode) != "" {
			invite, err := store.RedeemInvite(ctx, in.TenantID, in.InviteCode, s.now())
			if err != nil {
				return err
			}
			inviteHash = invite.Code
			redeemedInvite = invite
		} else if mode == RegistrationModeInviteRequired {
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
			if redeemedInvite.CommunityInvitationID > 0 && redeemedInvite.CreatedBy > 0 && redeemedInvite.CreatedBy != user.ID {
				if err := store.CreateCommunityReferral(ctx, user.TenantID, user.ID, redeemedInvite.CreatedBy, redeemedInvite.CommunityInvitationID); err != nil {
					return err
				}
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
			// S2-048 时序等工: 对不存在的邮箱也跑一次等价 argon2 校验再返回, 否则
			// 「存在(下面走 VerifyPassword 跑 argon2)」与「不存在(直接返回)」的响应时延差
			// 会暴露该邮箱是否已注册(用户枚举侧信道)。比较结果丢弃, 一律返 ErrInvalidCredentials。
			_, _ = verifyPasswordFn(timingEqualizationHash, in.Password)
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
		// S2-048 时序等工: 存在但无本地口令(social-only)的用户也跑一次等价 argon2 再返回,
		// 否则其「快速返回」会与「不存在(已跑 dummy)」「口令错(跑真 argon2)」的时延不同, 仍暴露
		// 该邮箱已注册(social-only)。不 MarkLoginFailure(无本地口令可失败, 保留既有语义)。
		_, _ = verifyPasswordFn(timingEqualizationHash, in.Password)
		return User{}, ErrInvalidCredentials
	}
	ok, verifyErr := verifyPasswordFn(user.PasswordHash, in.Password)
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

func (s *Service) registrationMode() (RegistrationMode, error) {
	if s == nil {
		return RegistrationModeOpen, nil
	}
	switch s.RegistrationMode {
	case "":
		if s.InviteRequired {
			return RegistrationModeInviteRequired, nil
		}
		return RegistrationModeOpen, nil
	case RegistrationModeOpen, RegistrationModeInviteRequired, RegistrationModeDisabled:
		return s.RegistrationMode, nil
	default:
		return "", ErrInvalidInput
	}
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

func (s *Service) PreparePasswordReset(ctx context.Context, in PasswordResetConfirm) (User, error) {
	if s == nil || s.Store == nil {
		return User{}, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 || strings.TrimSpace(in.Token) == "" || strings.TrimSpace(in.NewPassword) == "" {
		return User{}, ErrInvalidInput
	}
	if _, err := HashPassword(in.NewPassword, s.PasswordPolicy); err != nil {
		return User{}, err
	}
	store, ok := s.Store.(passwordResetPreparationStore)
	if !ok {
		return User{}, ErrStoreNotConfigured
	}
	return store.PreparePasswordResetTokenUser(ctx, in.TenantID, HashToken(in.Token), s.now())
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
