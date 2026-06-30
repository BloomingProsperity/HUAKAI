package userauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	SocialProviderGoogle   = "google"
	SocialProviderGitHub   = "github"
	SocialProviderQQ       = "qq"
	SocialProviderWeChat   = "wechat"
	SocialProviderDingTalk = "dingtalk"
	SocialProviderNodeSeek = "nodeseek"
	SocialProviderLinuxDo  = "linuxdo"
	SocialProviderOIDC     = "oidc"
	SocialProviderDiscord  = "discord"
	SocialProviderTelegram = "telegram"
)

type OAuthConfig struct {
	Provider                 string
	ClientID                 string
	ClientSecret             string
	AuthURL                  string
	TokenURL                 string
	RedirectURI              string
	Scopes                   []string
	UserURL                  string
	EmailsURL                string
	OpenIDURL                string
	JWKSURL                  string
	Issuer                   string
	SubjectField             string
	EmailField               string
	EmailVerifiedField       string
	DisplayNameField         string
	MinimumNumericClaimField string
	MinimumNumericClaimValue int64
}

type OAuthStart struct {
	Provider string `json:"provider"`
	State    string `json:"state"`
	AuthURL  string `json:"auth_url"`
}

type OAuthProvider interface {
	Provider() string
	AuthorizationURL(challenge OAuthFlowChallenge) (string, error)
	ExchangeVerifiedIdentity(ctx context.Context, flow OAuthFlowSession, code string) (VerifiedIdentity, error)
}

type OAuthService struct {
	providers map[string]OAuthProvider
}

func NewOAuthService(providers ...OAuthProvider) *OAuthService {
	out := &OAuthService{providers: map[string]OAuthProvider{}}
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		name := normalizeSocialProvider(provider.Provider())
		if name != "" {
			out.providers[name] = provider
		}
	}
	return out
}

func (s *OAuthService) Provider(name string) (OAuthProvider, bool) {
	if s == nil {
		return nil, false
	}
	provider, ok := s.providers[normalizeSocialProvider(name)]
	return provider, ok
}

func (s *Service) StartOAuth(ctx context.Context, in OAuthInitInput) (OAuthInitResult, error) {
	if s == nil || s.Store == nil {
		return OAuthInitResult{}, ErrStoreNotConfigured
	}
	providerName := normalizeSocialProvider(in.Provider)
	if in.TenantID <= 0 || providerName == "" {
		return OAuthInitResult{}, ErrInvalidInput
	}
	provider, ok := s.OAuth.Provider(providerName)
	if !ok {
		return OAuthInitResult{}, ErrOAuthProviderMissing
	}
	// fail-closed 校验 caller 提供的 redirect_uri,杜绝 open-redirect/授权码被引导到攻击者地址。
	if err := s.validateOAuthRedirectURI(in.RedirectURI); err != nil {
		return OAuthInitResult{}, err
	}
	ttl := s.OAuthFlowTTL
	if ttl <= 0 {
		ttl = DefaultOAuthFlowTTL
	}
	challenge, err := NewOAuthFlowChallenge(in.TenantID, providerName, strings.TrimSpace(in.RedirectURI), ttl, s.now())
	if err != nil {
		return OAuthInitResult{}, err
	}
	authURL, err := provider.AuthorizationURL(challenge)
	if err != nil {
		return OAuthInitResult{}, err
	}
	if err := s.Store.CreateOAuthFlowSession(ctx, challenge); err != nil {
		return OAuthInitResult{}, err
	}
	return OAuthInitResult{Provider: providerName, State: challenge.State, AuthURL: authURL, ExpiresAt: challenge.ExpiresAt}, nil
}

// validateOAuthRedirectURI fail-closed 校验 caller 提供的 redirect_uri:为空 → 允许(后续用各 provider
// 服务端配置的固定 RedirectURI);非空 → 必须精确匹配管理员配置的 AllowedRedirectURIs 之一,否则拒绝。
// 默认白名单为空 → 任何非空 caller redirect 都被拒,杜绝把授权码/回调引导到攻击者控制的地址。
func (s *Service) validateOAuthRedirectURI(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, allowed := range s.AllowedRedirectURIs {
		if raw == strings.TrimSpace(allowed) {
			return nil
		}
	}
	return fmt.Errorf("%w: redirect_uri 不在允许白名单", ErrInvalidInput)
}

func (s *Service) CompleteOAuth(ctx context.Context, in OAuthCallbackInput) (User, error) {
	if s == nil || s.Store == nil {
		return User{}, ErrStoreNotConfigured
	}
	providerName := normalizeSocialProvider(in.Provider)
	if in.TenantID <= 0 || providerName == "" || strings.TrimSpace(in.State) == "" || strings.TrimSpace(in.Code) == "" {
		return User{}, ErrInvalidInput
	}
	provider, ok := s.OAuth.Provider(providerName)
	if !ok {
		return User{}, ErrOAuthProviderMissing
	}
	flow, err := s.Store.ConsumeOAuthFlowSession(ctx, in.TenantID, providerName, HashToken(in.State), s.now())
	if err != nil {
		return User{}, err
	}
	identity, err := provider.ExchangeVerifiedIdentity(ctx, flow, strings.TrimSpace(in.Code))
	if err != nil {
		return User{}, err
	}
	return s.applyVerifiedSocialIdentity(ctx, in.TenantID, identity)
}

func (s *Service) ApplyVerifiedSocialIdentity(ctx context.Context, tenantID int64, identity VerifiedIdentity) (User, error) {
	return s.applyVerifiedSocialIdentity(ctx, tenantID, identity)
}

func (s *Service) applyVerifiedSocialIdentity(ctx context.Context, tenantID int64, identity VerifiedIdentity) (User, error) {
	if s == nil || s.Store == nil {
		return User{}, ErrStoreNotConfigured
	}
	provider := normalizeSocialProvider(identity.Provider)
	email := NormalizeEmail(identity.Email)
	subject := strings.TrimSpace(identity.Subject)
	if tenantID <= 0 || provider == "" || subject == "" || email == "" {
		return User{}, ErrInvalidInput
	}
	// 既有绑定优先:一个已建立的社交身份是受信凭证,直接登录——不受本次身份是否带已验证邮箱影响。
	// 邮箱门(下方)只为防「用未验证邮箱 link 到既有真实账号」的接管,对已绑定身份不适用。
	// 这同时修复了原先「邮箱门在查绑定之前」导致已绑定的无邮箱身份(telegram/QQ,EmailVerified 恒 false)
	// 永远登不进的顺序缺陷——「先绑定后登录」模型的关键前提。
	if user, err := s.Store.GetUserBySocialIdentity(ctx, tenantID, provider, subject); err == nil {
		if err := ensureSocialLoginUserAllowed(user, s.now()); err != nil {
			return User{}, err
		}
		return user, nil
	} else if err != nil && !errors.Is(err, ErrUserNotFound) {
		return User{}, err
	}
	// 全新身份(无既有绑定):邮箱门拦下无已验证邮箱的源(telegram/QQ 等)。在「先绑定后登录」模型下,
	// 这类源须先由已登录用户在设置里绑定,未绑定不能凭空建号/登录。
	if !identity.EmailVerified {
		return User{}, ErrOAuthPendingEmailRequired
	}
	existing, err := s.Store.GetUserByEmail(ctx, tenantID, email)
	if err == nil {
		user, err := s.Store.LinkSocialIdentity(ctx, existing.TenantID, existing.ID, provider, subject)
		if err != nil {
			return User{}, err
		}
		if err := ensureSocialLoginUserAllowed(user, s.now()); err != nil {
			return User{}, err
		}
		return user, nil
	}
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return User{}, err
	}
	if !s.SocialSignup {
		return User{}, ErrSocialLoginRejected
	}
	// 走到这里说明是「全新用户首次社交注册」(社交身份与邮箱都查无既有用户)。社交流程
	// 没有邀请码输入通道, 必须与密码 Register 受同一公开注册/邀请闸约束。本检查只在新用户分支生效,
	// 既有用户的社交登录/绑定走上面的 link 路径不受影响。
	mode, err := s.registrationMode()
	if err != nil {
		return User{}, err
	}
	switch mode {
	case RegistrationModeDisabled:
		return User{}, ErrRegistrationDisabled
	case RegistrationModeInviteRequired:
		return User{}, ErrInviteRequired
	}
	user, err := s.Store.CreateUser(ctx, CreateUserParams{
		TenantID:            tenantID,
		Email:               email,
		DisplayName:         identity.DisplayName,
		EmailVerified:       true,
		SocialLoginProvider: provider,
		Status:              UserStatusActive,
	})
	if err != nil {
		return User{}, err
	}
	linkedUser, err := s.Store.LinkSocialIdentity(ctx, user.TenantID, user.ID, provider, subject)
	if err != nil {
		return User{}, err
	}
	s.issueSignupCredits(ctx, linkedUser.TenantID, linkedUser.ID, false)
	return linkedUser, nil
}

// LinkVerifiedSocialIdentity 把一个已校验的社交身份(provider+subject)绑定到指定的已登录用户。
// 「先绑定后登录」模型的绑定腿:无已验证邮箱的社交源(telegram/QQ 等)不能凭空建号,只能由已登录用户
// 在设置里主动绑定;绑定后再走 telegram-login 等端点凭既有绑定直接登录(见 applyVerifiedSocialIdentity
// 的既有绑定优先分支)。
//
// 调用方必须先用对应的 verifier(如 telegramauth.VerifyWidget)校验出可信的 identity,本方法不做凭证校验,
// 只负责「把已校验身份安全地落成本人绑定」。tenant/user 必须取自 session,绝不取自请求体。
//
// 接管保护:该 subject 已绑到「另一个」用户 → ErrSocialIdentityAlreadyBound;已绑到「本人」→ 幂等成功。
// 账号须存在且允许登录(banned/inactive 会被 ensureSocialLoginUserAllowed 拒)。
func (s *Service) LinkVerifiedSocialIdentity(ctx context.Context, tenantID, userID int64, identity VerifiedIdentity) (User, error) {
	if s == nil || s.Store == nil {
		return User{}, ErrStoreNotConfigured
	}
	provider := normalizeSocialProvider(identity.Provider)
	subject := strings.TrimSpace(identity.Subject)
	if tenantID <= 0 || userID <= 0 || provider == "" || subject == "" {
		return User{}, ErrInvalidInput
	}
	var linked User
	err := s.withStoreTx(ctx, func(store Store) error {
		user, err := store.GetUserByID(ctx, tenantID, userID)
		if err != nil {
			return err
		}
		if err := ensureSocialLoginUserAllowed(user, s.now()); err != nil {
			return err
		}
		// 接管保护:subject 已绑到他人则拒;已绑到本人则幂等(直接当成功)。
		existing, err := store.GetUserBySocialIdentity(ctx, tenantID, provider, subject)
		switch {
		case err == nil:
			if existing.ID != userID {
				return ErrSocialIdentityAlreadyBound
			}
		case !errors.Is(err, ErrUserNotFound):
			return err
		}
		linked, err = store.LinkSocialIdentity(ctx, tenantID, userID, provider, subject)
		return err
	})
	if err != nil {
		return User{}, err
	}
	return linked, nil
}

type socialIdentityUnlinkStore interface {
	CountUserSocialIdentityLinks(context.Context, int64, int64) (int, error)
	CountSocialIdentityLinks(context.Context, int64, int64, string) (int, error)
	UnlinkSocialIdentity(context.Context, int64, int64, string) (bool, error)
}

func (s *Service) UnlinkSocialIdentity(ctx context.Context, tenantID, userID int64, provider string) (bool, error) {
	if s == nil || s.Store == nil {
		return false, ErrStoreNotConfigured
	}
	provider = normalizeSocialProvider(provider)
	if tenantID <= 0 || userID <= 0 || provider == "" {
		return false, ErrInvalidInput
	}
	var unlinked bool
	err := s.withStoreTx(ctx, func(store Store) error {
		unlinker, ok := store.(socialIdentityUnlinkStore)
		if !ok {
			return ErrStoreNotConfigured
		}
		user, err := store.GetUserByID(ctx, tenantID, userID)
		if err != nil {
			return err
		}
		providerLinks, err := unlinker.CountSocialIdentityLinks(ctx, tenantID, userID, provider)
		if err != nil {
			return err
		}
		if providerLinks == 0 {
			unlinked = false
			return nil
		}
		if strings.TrimSpace(user.PasswordHash) == "" {
			totalLinks, err := unlinker.CountUserSocialIdentityLinks(ctx, tenantID, userID)
			if err != nil {
				return err
			}
			if totalLinks <= providerLinks {
				return ErrLastLoginMethod
			}
		}
		unlinked, err = unlinker.UnlinkSocialIdentity(ctx, tenantID, userID, provider)
		return err
	})
	if err != nil {
		return false, err
	}
	return unlinked, nil
}

// socialIdentityListStore 是 ListSocialIdentityLinks 所需的只读存储面;由 *PostgresStore 实现。
type socialIdentityListStore interface {
	ListSocialIdentityLinks(context.Context, int64, int64) ([]SocialIdentityLink, error)
}

// ListSocialIdentityLinks 返回已认证用户自己的社交登录绑定(只读)。tenant/user 必须取自 session。
// 每条 subject 在出 service 前脱敏,前端只需识别「已绑过哪个 provider」,不应拿到可关联第三方账号的原始
// subject(防 enumeration / 关联泄露)。
func (s *Service) ListSocialIdentityLinks(ctx context.Context, tenantID, userID int64) ([]SocialIdentityLink, error) {
	if s == nil || s.Store == nil {
		return nil, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return nil, ErrInvalidInput
	}
	lister, ok := s.Store.(socialIdentityListStore)
	if !ok {
		return nil, ErrStoreNotConfigured
	}
	links, err := lister.ListSocialIdentityLinks(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	for i := range links {
		links[i].Subject = maskSocialSubject(links[i].Subject)
	}
	return links, nil
}

// maskSocialSubject 脱敏上游 subject:保留首尾少量字符,中间以 * 占位;过短的整体以 * 替换。
// 目的是让用户能粗略辨认是哪个第三方账号、又不把完整可关联标识暴露出去。
func maskSocialSubject(subject string) string {
	subject = strings.TrimSpace(subject)
	runes := []rune(subject)
	switch n := len(runes); {
	case n == 0:
		return ""
	case n <= 2:
		return strings.Repeat("*", n)
	case n <= 6:
		return string(runes[0]) + strings.Repeat("*", n-1)
	default:
		return string(runes[:2]) + strings.Repeat("*", n-4) + string(runes[n-2:])
	}
}

// EnsureLoginEligible 校验账号是否允许登录:禁用/删除/锁定/待重置 + 时间型临时锁(locked_until
// 在未来)一律拒。这是与密码登录(service.go 的内联门)、social 登录一致的账号资格门;任何强认证
// 方式(passkey 等)在签发 session 之前都必须复用它,否则会出现"被管理员封禁/锁定的用户凭某种认证
// 方式仍能登录"的绕过——属 auth-core 访问控制不变量。注:邮箱未验证这类按租户策略生效的软门不在
// 此处(与 social 一致),由各调用方按需另行处理。
func EnsureLoginEligible(user User, now time.Time) error {
	switch user.Status {
	case UserStatusDisabled, UserStatusDeleted:
		return ErrUserDisabled
	case UserStatusLocked:
		return ErrUserLocked
	case UserStatusResetRequired:
		return ErrPasswordResetRequired
	}
	// 时间型临时锁:status 仍 active 但 locked_until 在未来时也必须拒(与密码门 service.go 一致),
	// 否则被时间锁的账号能经 passkey/social 绕过(failed_login_count 达阈值那一支已由 MarkLoginFailure
	// 原子翻成 status=locked,被上面的 UserStatusLocked 捕获,无独立窗口)。
	if user.LockedUntil != nil && now.Before(*user.LockedUntil) {
		return ErrUserLocked
	}
	return nil
}

func ensureSocialLoginUserAllowed(user User, now time.Time) error {
	return EnsureLoginEligible(user, now)
}

func normalizeSocialProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch {
	case p == SocialProviderGoogle:
		return SocialProviderGoogle
	case p == "github":
		return SocialProviderGitHub
	case p == SocialProviderQQ:
		return SocialProviderQQ
	case p == SocialProviderWeChat:
		return SocialProviderWeChat
	case p == SocialProviderDingTalk:
		return SocialProviderDingTalk
	case p == SocialProviderNodeSeek:
		return SocialProviderNodeSeek
	case p == SocialProviderLinuxDo:
		return SocialProviderLinuxDo
	case p == SocialProviderOIDC || strings.HasPrefix(p, SocialProviderOIDC+":"):
		return SocialProviderOIDC
	case p == SocialProviderDiscord:
		return SocialProviderDiscord
	case p == SocialProviderTelegram:
		return SocialProviderTelegram
	default:
		return ""
	}
}
