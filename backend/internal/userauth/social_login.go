package userauth

import (
	"context"
	"errors"
	"strings"
)

const (
	SocialProviderGoogle = "google"
	SocialProviderGitHub = "github"
)

type OAuthConfig struct {
	Provider     string
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	RedirectURI  string
	Scopes       []string
	UserURL      string
	EmailsURL    string
	JWKSURL      string
	Issuer       string
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
	if !identity.EmailVerified {
		return User{}, ErrSocialLoginRejected
	}
	if user, err := s.Store.GetUserBySocialIdentity(ctx, tenantID, provider, subject); err == nil {
		if err := ensureSocialLoginUserAllowed(user); err != nil {
			return User{}, err
		}
		return user, nil
	} else if err != nil && !errors.Is(err, ErrUserNotFound) {
		return User{}, err
	}
	existing, err := s.Store.GetUserByEmail(ctx, tenantID, email)
	if err == nil {
		user, err := s.Store.LinkSocialIdentity(ctx, existing.TenantID, existing.ID, provider, subject)
		if err != nil {
			return User{}, err
		}
		if err := ensureSocialLoginUserAllowed(user); err != nil {
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
	return s.Store.LinkSocialIdentity(ctx, user.TenantID, user.ID, provider, subject)
}

func ensureSocialLoginUserAllowed(user User) error {
	switch user.Status {
	case UserStatusDisabled, UserStatusDeleted:
		return ErrUserDisabled
	case UserStatusLocked:
		return ErrUserLocked
	case UserStatusResetRequired:
		return ErrPasswordResetRequired
	default:
		return nil
	}
}

func normalizeSocialProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case SocialProviderGoogle:
		return SocialProviderGoogle
	case "github":
		return SocialProviderGitHub
	default:
		return ""
	}
}
