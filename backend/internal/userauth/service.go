package userauth

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BloomingProsperity/HUAKAI/internal/emailpolicy"
)

// verifyPasswordFn 是口令校验的可注入入口(生产即 VerifyPassword)。抽成变量是为了让测试
// 能观察「邮箱不存在/存在但无本地口令」路径是否也跑了一次等价 argon2 校验，这是登录时序等工的核心。
var verifyPasswordFn = VerifyPassword

// timingEqualizationHash 是一个无人知晓口令的合法 argon2id hash(常量, 默认 policy 成本 m=64MiB,t=3)。
// 仅用于 Authenticate 在「邮箱不存在」或「存在但无本地口令(social-only)」时做等工量 argon2 校验:
// 否则「走 VerifyPassword 跑 argon2」与「直接返回」的响应时延差会泄露邮箱是否已注册（用户枚举侧信道）。
// 硬编码常量而非运行时生成, 杜绝生成失败时 fail-open 成快速 parse-fail。
const timingEqualizationHash = "$argon2id$v=19$m=65536,t=3,p=1$0k8KjQ01TveJhg0daai5hw$m6FwG+zGw8X2YLWE1grPszMNN84IGcyd5xhFrEGMhIc"

const MaxDisplayNameLength = 100

// equalizeLoginWork 跑一次与正常口令校验等价成本的 argon2(结果丢弃)。用于让「因不存在 / 账号状态 /
// 无本地口令而提前返回」的登录失败路径与「口令错」路径耗时一致, 杜绝登录时序枚举侧信道。
// 有真实口令 hash 用真实 hash(成本与正常校验完全一致), 否则用硬编码的合法 dummy argon2id 常量。
func (s *Service) equalizeLoginWork(passwordHash, attempted string) {
	if passwordHash == "" {
		passwordHash = timingEqualizationHash
	}
	_, _ = verifyPasswordFn(passwordHash, attempted)
}

type Store interface {
	CreateUser(context.Context, CreateUserParams) (User, error)
	GetUserByEmail(context.Context, int64, string) (User, error)
	GetUserByID(context.Context, int64, int64) (User, error)
	ClearLockout(context.Context, int64, int64) (User, error)
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

type profileUpdateStore interface {
	UpdateDisplayName(context.Context, int64, int64, string) (User, error)
}

type EmailVerificationPolicy interface {
	EmailVerificationEnabled(context.Context, int64) (bool, error)
}

type RegistrationGate interface {
	PasswordRegistrationAllowed(context.Context, int64) (bool, error)
	PasswordLoginAllowed(context.Context, int64) (bool, error)
}

type EmailPolicy interface {
	EmailDomainAllowlist(context.Context, int64) (bool, string, error)
	EmailAliasRestrictionEnabled(context.Context, int64) (bool, error)
	ReservedEmailLocalparts(context.Context, int64) (string, error)
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
	RegistrationGate RegistrationGate
	EmailPolicy      EmailPolicy
	// AllowedRedirectURIs 是 social OAuth init 允许的 caller redirect_uri 精确白名单。空(默认)=
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

func NormalizeDisplayName(displayName string) (string, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || !utf8.ValidString(displayName) || utf8.RuneCountInString(displayName) > MaxDisplayNameLength {
		return "", ErrInvalidInput
	}
	for _, r := range displayName {
		if unicode.IsControl(r) {
			return "", ErrInvalidInput
		}
	}
	return displayName, nil
}

func (s *Service) GetProfile(ctx context.Context, tenantID, userID int64) (User, error) {
	if s == nil || s.Store == nil {
		return User{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return User{}, ErrInvalidInput
	}
	return s.Store.GetUserByID(ctx, tenantID, userID)
}

func (s *Service) UpdateProfile(ctx context.Context, tenantID, userID int64, displayName string) (User, error) {
	if s == nil || s.Store == nil {
		return User{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return User{}, ErrInvalidInput
	}
	normalized, err := NormalizeDisplayName(displayName)
	if err != nil {
		return User{}, err
	}
	updater, ok := s.Store.(profileUpdateStore)
	if !ok {
		return User{}, ErrStoreNotConfigured
	}
	return updater.UpdateDisplayName(ctx, tenantID, userID, normalized)
}

func (s *Service) UnlockUser(ctx context.Context, tenantID, userID int64) (User, error) {
	if s == nil || s.Store == nil {
		return User{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return User{}, ErrInvalidInput
	}
	return s.Store.ClearLockout(ctx, tenantID, userID)
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
	if err := s.checkPasswordRegistration(ctx, in.TenantID); err != nil {
		return RegistrationResult{}, err
	}
	if err := s.checkRegistrationEmailPolicy(ctx, in.TenantID, email); err != nil {
		return RegistrationResult{}, err
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
	if err := s.checkPasswordLogin(ctx, in.TenantID, in.Password); err != nil {
		return User{}, err
	}
	user, err := s.Store.GetUserByEmail(ctx, in.TenantID, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// 时序等工: 对不存在的邮箱也跑一次等价 argon2 校验再返回, 否则
			// 「存在(下面走 VerifyPassword 跑 argon2)」与「不存在(直接返回)」的响应时延差
			// 会暴露该邮箱是否已注册(用户枚举侧信道)。比较结果丢弃, 一律返 ErrInvalidCredentials。
			s.equalizeLoginWork("", in.Password)
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	// 时序等工: 以下「因账号状态而失败」的分支若直接返回, 耗时会明显短于「口令错」(跑 argon2)
	// 分支, 从而泄露「该邮箱存在且处于某状态」(时序枚举侧信道)。配合 handler 把这些状态对外统一成
	// generic invalid_credentials(消状态码 oracle), service 这里负责消时序: 每条返回前都跑一次与口令
	// 校验等价成本的 argon2。typed error 仍返回, 供 handler 审计归类真实 reason(对外不暴露)。
	if user.Status == UserStatusDisabled || user.Status == UserStatusDeleted {
		s.equalizeLoginWork(user.PasswordHash, in.Password)
		return User{}, ErrUserDisabled
	}
	if user.Status == UserStatusResetRequired {
		s.equalizeLoginWork(user.PasswordHash, in.Password)
		return User{}, ErrPasswordResetRequired
	}
	threshold := s.lockoutThreshold()
	if user.Status == UserStatusLocked || user.FailedLoginCount >= threshold || (user.LockedUntil != nil && s.now().Before(*user.LockedUntil)) {
		s.equalizeLoginWork(user.PasswordHash, in.Password)
		_ = s.Store.MarkLoginFailure(ctx, user.TenantID, user.ID, threshold)
		return User{}, ErrUserLocked
	}
	if s.requireEmailVerification(ctx, in.TenantID) && !user.EmailVerified {
		s.equalizeLoginWork(user.PasswordHash, in.Password)
		return User{}, ErrEmailUnverified
	}
	if user.PasswordHash == "" {
		// 时序等工: 存在但无本地口令(social-only)的用户也跑一次等价 argon2 再返回,
		// 否则其「快速返回」会与「不存在(已跑 dummy)」「口令错(跑真 argon2)」的时延不同, 仍暴露
		// 该邮箱已注册(social-only)。不 MarkLoginFailure(无本地口令可失败, 保留既有语义)。
		s.equalizeLoginWork("", in.Password)
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

func (s *Service) checkPasswordRegistration(ctx context.Context, tenantID int64) error {
	if s == nil || s.RegistrationGate == nil {
		return nil
	}
	allowed, err := s.RegistrationGate.PasswordRegistrationAllowed(ctx, tenantID)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrPasswordRegistrationDisabled
	}
	return nil
}

func (s *Service) checkPasswordLogin(ctx context.Context, tenantID int64, attempted string) error {
	if s == nil || s.RegistrationGate == nil {
		return nil
	}
	allowed, err := s.RegistrationGate.PasswordLoginAllowed(ctx, tenantID)
	if err != nil {
		return err
	}
	if !allowed {
		s.equalizeLoginWork("", attempted)
		return ErrPasswordLoginDisabled
	}
	return nil
}

func (s *Service) checkRegistrationEmailPolicy(ctx context.Context, tenantID int64, email string) error {
	if s == nil || s.EmailPolicy == nil {
		return nil
	}
	domainEnabled, domainList, err := s.EmailPolicy.EmailDomainAllowlist(ctx, tenantID)
	if err != nil {
		return err
	}
	if err := emailpolicy.CheckDomain(email, domainEnabled, domainList); err != nil {
		return err
	}
	aliasEnabled, err := s.EmailPolicy.EmailAliasRestrictionEnabled(ctx, tenantID)
	if err != nil {
		return err
	}
	if err := emailpolicy.CheckAlias(email, aliasEnabled); err != nil {
		return err
	}
	reservedList, err := s.EmailPolicy.ReservedEmailLocalparts(ctx, tenantID)
	if err != nil {
		return err
	}
	return emailpolicy.CheckReserved(email, reservedList)
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

// Clock exposes the service injected clock so HTTP handlers thread the same
// time source (deterministic tests; production uses the default time.Now).
func (s *Service) Clock() time.Time { return s.now() }

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
