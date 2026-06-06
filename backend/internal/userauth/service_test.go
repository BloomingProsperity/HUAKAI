package userauth

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAuthRegisterVerifyLoginAndResetReplay(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 8, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	rawInvite, inviteHash, err := GenerateInviteCode()
	if err != nil {
		t.Fatalf("GenerateInviteCode: %v", err)
	}
	store.invites[inviteHash] = InviteCode{Code: inviteHash, TenantID: 1, MaxUses: 1, Status: "active", CreatedAt: now, UpdatedAt: now}
	svc := NewService(store)
	svc.InviteRequired = true
	svc.PasswordPolicy = PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	svc.Now = func() time.Time { return now }

	registered, err := svc.Register(ctx, RegisterInput{
		TenantID: 1, Email: "USER@example.test", Password: "initial-secret", InviteCode: rawInvite,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if registered.User.InviteCodeUsed != inviteHash {
		t.Fatalf("invite hash not bound to user: got %q want %q", registered.User.InviteCodeUsed, inviteHash)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "user@example.test", Password: "initial-secret"}); !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("Authenticate before verify error = %v, want ErrEmailUnverified", err)
	}
	verified, err := svc.VerifyEmail(ctx, 1, registered.VerificationToken)
	if err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	if !verified.EmailVerified || verified.Status != UserStatusActive {
		t.Fatalf("verified user state = verified:%v status:%s", verified.EmailVerified, verified.Status)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "user@example.test", Password: "initial-secret"}); err != nil {
		t.Fatalf("Authenticate after verify: %v", err)
	}

	reset, err := svc.RequestPasswordReset(ctx, PasswordResetRequest{TenantID: 1, Email: "user@example.test"})
	if err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	if reset.Token == "" {
		t.Fatal("reset token is empty")
	}
	if _, err := svc.ResetPassword(ctx, PasswordResetConfirm{TenantID: 1, Token: reset.Token, NewPassword: "new-secret"}); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if _, err := svc.ResetPassword(ctx, PasswordResetConfirm{TenantID: 1, Token: reset.Token, NewPassword: "newer-secret"}); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("reset replay error = %v, want ErrTokenInvalid", err)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "user@example.test", Password: "initial-secret"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "user@example.test", Password: "new-secret"}); err != nil {
		t.Fatalf("new password auth: %v", err)
	}
}

func TestAuthRegisterCanSkipEmailVerificationByPolicy(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.PasswordPolicy = PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	svc.Now = func() time.Time { return now }
	svc.Verification = staticVerificationPolicy(false)

	registered, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "skip@example.test", Password: "secret"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if registered.VerificationToken != "" {
		t.Fatalf("verification token = %q, want empty when policy skips verification", registered.VerificationToken)
	}
	if !registered.User.EmailVerified || registered.User.Status != UserStatusActive {
		t.Fatalf("registered state = verified:%v status:%s", registered.User.EmailVerified, registered.User.Status)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "skip@example.test", Password: "secret"}); err != nil {
		t.Fatalf("Authenticate with verification disabled: %v", err)
	}
}

type staticVerificationPolicy bool

func (p staticVerificationPolicy) EmailVerificationEnabled(context.Context, int64) (bool, error) {
	return bool(p), nil
}

func TestUpdateProfile_SelfScoped(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.RequireVerified = false
	svc.PasswordPolicy = PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}

	userA, err := store.CreateUser(ctx, CreateUserParams{TenantID: 7, Email: "a@example.test", DisplayName: "Alice", Status: UserStatusActive})
	if err != nil {
		t.Fatalf("create user A: %v", err)
	}
	userB, err := store.CreateUser(ctx, CreateUserParams{TenantID: 7, Email: "b@example.test", DisplayName: "Bob", Status: UserStatusActive})
	if err != nil {
		t.Fatalf("create user B: %v", err)
	}

	updated, err := svc.UpdateProfile(ctx, 7, userA.ID, " Alice Updated ")
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if updated.DisplayName != "Alice Updated" {
		t.Fatalf("updated display_name=%q want trimmed Alice Updated", updated.DisplayName)
	}
	gotA, err := store.GetUserByID(ctx, 7, userA.ID)
	if err != nil {
		t.Fatalf("read user A: %v", err)
	}
	gotB, err := store.GetUserByID(ctx, 7, userB.ID)
	if err != nil {
		t.Fatalf("read user B: %v", err)
	}
	if gotA.DisplayName != "Alice Updated" || gotB.DisplayName != "Bob" {
		t.Fatalf("scope leak after update: A=%q B=%q; MUTATION: updating any user other than the requested self user should turn this red", gotA.DisplayName, gotB.DisplayName)
	}
}

func TestUpdateProfile_Validation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	user, err := store.CreateUser(ctx, CreateUserParams{TenantID: 7, Email: "valid@example.test", DisplayName: "Stable", Status: UserStatusActive})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	tooLong := strings.Repeat("a", MaxDisplayNameLength+1)
	cases := []struct {
		name        string
		displayName string
	}{
		{name: "empty_after_trim", displayName: "   "},
		{name: "too_long", displayName: tooLong},
		{name: "control_char", displayName: "bad\nname"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.UpdateProfile(ctx, 7, user.ID, tc.displayName); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("UpdateProfile err=%v want ErrInvalidInput; MUTATION: skipping display_name validation should write", err)
			}
			got, err := store.GetUserByID(ctx, 7, user.ID)
			if err != nil {
				t.Fatalf("read user after rejected update: %v", err)
			}
			if got.DisplayName != "Stable" {
				t.Fatalf("display_name changed to %q after rejected input, want Stable", got.DisplayName)
			}
		})
	}
}

func TestUpdateProfile_TenantScoped(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	user, err := store.CreateUser(ctx, CreateUserParams{TenantID: 7, Email: "tenant@example.test", DisplayName: "Tenant Seven", Status: UserStatusActive})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := svc.UpdateProfile(ctx, 8, user.ID, "Wrong Tenant"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("cross-tenant update err=%v want ErrUserNotFound; MUTATION: dropping tenant scope in UPDATE should write", err)
	}
	got, err := store.GetUserByID(ctx, 7, user.ID)
	if err != nil {
		t.Fatalf("read user: %v", err)
	}
	if got.DisplayName != "Tenant Seven" {
		t.Fatalf("cross-tenant update changed display_name to %q", got.DisplayName)
	}
}

func TestAT_AUTH_007_005_LockoutAndResetRequired(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.RequireVerified = false
	svc.LockoutThreshold = 2
	svc.PasswordPolicy = PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	svc.Now = func() time.Time { return now }
	registered, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "lock@example.test", Password: "secret"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "lock@example.test", Password: "bad"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("first bad password = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "lock@example.test", Password: "bad"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("second bad password = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "lock@example.test", Password: "secret"}); !errors.Is(err, ErrUserLocked) {
		t.Fatalf("locked correct password = %v, want ErrUserLocked", err)
	}
	user := store.users[registered.User.ID]
	user.Status = UserStatusResetRequired
	user.FailedLoginCount = 0
	store.users[user.ID] = user
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "lock@example.test", Password: "secret"}); !errors.Is(err, ErrPasswordResetRequired) {
		t.Fatalf("reset required login = %v, want ErrPasswordResetRequired", err)
	}
	reset, err := svc.RequestPasswordReset(ctx, PasswordResetRequest{TenantID: 1, Email: "lock@example.test"})
	if err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	if _, err := svc.ResetPassword(ctx, PasswordResetConfirm{TenantID: 1, Token: reset.Token, NewPassword: "new-secret"}); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "lock@example.test", Password: "new-secret"}); err != nil {
		t.Fatalf("login after reset: %v", err)
	}
}

func TestAT_AUTH_007_006_007_OAuthFlowUsesVerifiedProviderClaims(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 10, 30, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.PasswordPolicy = PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	svc.Now = func() time.Time { return now }
	provider := &fakeOAuthProvider{
		provider: SocialProviderGoogle,
		identity: VerifiedIdentity{
			Provider: SocialProviderGoogle, Subject: "google-subject", Email: "USER@example.test",
			DisplayName: "User", EmailVerified: true,
		},
	}
	svc.OAuth = NewOAuthService(provider)
	svc.AllowedRedirectURIs = []string{"https://huakai.example.test/callback"} // 显式允许该 caller redirect
	existing, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "user@example.test", Password: "secret"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	init, err := svc.StartOAuth(ctx, OAuthInitInput{TenantID: 1, Provider: SocialProviderGoogle, RedirectURI: "https://huakai.example.test/callback"})
	if err != nil {
		t.Fatalf("StartOAuth: %v", err)
	}
	if init.AuthURL == "" || init.State == "" || !strings.Contains(init.AuthURL, "code_challenge=") {
		t.Fatalf("oauth init missing state/auth url/pkce: %+v", init)
	}
	user, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{
		TenantID: 1, Provider: SocialProviderGoogle, State: init.State, Code: "code-from-provider",
	})
	if err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	if user.ID != existing.User.ID || user.Email != "user@example.test" || !user.EmailVerified || provider.lastVerifier == "" {
		t.Fatalf("oauth did not link verified provider identity to existing user: user=%+v verifier=%q", user, provider.lastVerifier)
	}
	if _, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{TenantID: 1, Provider: SocialProviderGoogle, State: init.State, Code: "replay"}); !errors.Is(err, ErrOAuthFlowExpired) && !errors.Is(err, ErrOAuthFlowNotFound) {
		t.Fatalf("oauth replay error = %v, want flow rejection", err)
	}

	svc.OAuth = NewOAuthService(&fakeOAuthProvider{
		provider: SocialProviderGitHub,
		identity: VerifiedIdentity{
			Provider: SocialProviderGitHub, Subject: "github-subject", Email: "new@example.test",
			DisplayName: "New", EmailVerified: true,
		},
	})
	init, err = svc.StartOAuth(ctx, OAuthInitInput{TenantID: 2, Provider: SocialProviderGitHub})
	if err != nil {
		t.Fatalf("StartOAuth github: %v", err)
	}
	created, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{TenantID: 2, Provider: SocialProviderGitHub, State: init.State, Code: "github-code"})
	if err != nil {
		t.Fatalf("CompleteOAuth github: %v", err)
	}
	if created.TenantID != 2 || created.Email != "new@example.test" || created.SocialLoginProvider != SocialProviderGitHub {
		t.Fatalf("github social signup mismatch: %+v", created)
	}
	init, err = svc.StartOAuth(ctx, OAuthInitInput{TenantID: 2, Provider: SocialProviderGitHub})
	if err != nil {
		t.Fatalf("StartOAuth github disabled: %v", err)
	}
	svc.SocialSignup = false
	svc.OAuth = NewOAuthService(&fakeOAuthProvider{
		provider: SocialProviderGitHub,
		identity: VerifiedIdentity{
			Provider: SocialProviderGitHub, Subject: "github-other", Email: "other@example.test",
			EmailVerified: true,
		},
	})
	if _, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{TenantID: 2, Provider: SocialProviderGitHub, State: init.State, Code: "github-code"}); !errors.Is(err, ErrSocialLoginRejected) {
		t.Fatalf("disabled social signup = %v, want ErrSocialLoginRejected", err)
	}
}

// TestStartOAuthRedirectAllowlist guards a caller-supplied redirect_uri must be rejected
// unless it exactly matches the configured allowlist (fail-closed); an empty redirect_uri is allowed
// and falls back to the provider's server-side RedirectURI. Prevents open-redirect / authorization
// -code hijack to attacker-controlled callbacks.
//
// Mutation check: delete the validateOAuthRedirectURI call in StartOAuth and the "not in allowlist"
// case is accepted → red. Discriminating: same provider/tenant, only the redirect_uri + allowlist vary.
func TestStartOAuthRedirectAllowlist(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 10, 30, 0, 0, time.UTC)
	newSvc := func(allow ...string) *Service {
		s := NewService(newMemoryAuthStore(now))
		s.Now = func() time.Time { return now }
		s.OAuth = NewOAuthService(&fakeOAuthProvider{
			provider: SocialProviderGoogle,
			identity: VerifiedIdentity{Provider: SocialProviderGoogle, Subject: "s", Email: "u@example.test", EmailVerified: true},
		})
		s.AllowedRedirectURIs = allow
		return s
	}
	// (1) caller redirect_uri not in (empty) allowlist → rejected fail-closed.
	if _, err := newSvc().StartOAuth(ctx, OAuthInitInput{TenantID: 1, Provider: SocialProviderGoogle, RedirectURI: "https://evil.example.test/steal"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("caller redirect_uri not in allowlist must be rejected; got %v", err)
	}
	// (2) caller redirect_uri exactly in allowlist → accepted.
	if _, err := newSvc("https://app.example.test/cb").StartOAuth(ctx, OAuthInitInput{TenantID: 1, Provider: SocialProviderGoogle, RedirectURI: "https://app.example.test/cb"}); err != nil {
		t.Fatalf("allowlisted redirect_uri must be accepted; got %v", err)
	}
	// (3) empty redirect_uri → accepted (server-side default callback used).
	if _, err := newSvc().StartOAuth(ctx, OAuthInitInput{TenantID: 1, Provider: SocialProviderGoogle}); err != nil {
		t.Fatalf("empty redirect_uri must be accepted (server default); got %v", err)
	}
}

func TestAuthOAuthRejectsUnverifiedProviderClaims(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 11, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.Now = func() time.Time { return now }
	provider := &fakeOAuthProvider{
		provider: SocialProviderGoogle,
		identity: VerifiedIdentity{Provider: SocialProviderGoogle, Subject: "sub", Email: "unverified@example.test", EmailVerified: false},
	}
	svc.OAuth = NewOAuthService(provider)
	init, err := svc.StartOAuth(ctx, OAuthInitInput{TenantID: 1, Provider: SocialProviderGoogle})
	if err != nil {
		t.Fatalf("StartOAuth: %v", err)
	}
	if _, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{TenantID: 1, Provider: SocialProviderGoogle, State: "attacker-state", Code: "code"}); !errors.Is(err, ErrOAuthFlowNotFound) {
		t.Fatalf("state mismatch = %v, want ErrOAuthFlowNotFound", err)
	}
	if provider.exchanges != 0 {
		t.Fatalf("state mismatch exchanged provider code %d times; want 0", provider.exchanges)
	}
	if _, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{TenantID: 1, Provider: SocialProviderGoogle, State: init.State, Code: "code"}); !errors.Is(err, ErrOAuthPendingEmailRequired) {
		t.Fatalf("unverified provider email = %v, want ErrOAuthPendingEmailRequired", err)
	}
	if len(store.users) != 0 {
		t.Fatalf("unverified social claim created users: %+v", store.users)
	}
}

func TestNormalizeSocialProviderAcceptsMultiOAuthNames(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"wechat", " WeChat ", SocialProviderWeChat},
		{"dingtalk", "DINGTALK", SocialProviderDingTalk},
		{"linuxdo", "linuxdo", SocialProviderLinuxDo},
		{"oidc", "OIDC", SocialProviderOIDC},
		{"oidc_slug", "oidc:corp-sso", SocialProviderOIDC},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeSocialProvider(tc.in); got != tc.want {
				t.Fatalf("normalizeSocialProvider(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStartOAuthOIDCSlugUsesStableProviderAndPKCENonce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.Now = func() time.Time { return now }
	svc.OAuth = NewOAuthService(&fakeOAuthProvider{
		provider: SocialProviderOIDC,
		identity: VerifiedIdentity{
			Provider: SocialProviderOIDC, Subject: "oidc-subject", Email: "oidc@example.test",
			DisplayName: "OIDC User", EmailVerified: true,
		},
	})

	init, err := svc.StartOAuth(ctx, OAuthInitInput{TenantID: 1, Provider: "oidc:corp-sso"})
	if err != nil {
		t.Fatalf("StartOAuth oidc slug: %v", err)
	}
	if init.Provider != SocialProviderOIDC {
		t.Fatalf("init provider = %q, want %q", init.Provider, SocialProviderOIDC)
	}
	if !strings.Contains(init.AuthURL, "code_challenge=") || !strings.Contains(init.AuthURL, "nonce=") {
		t.Fatalf("auth URL missing PKCE challenge or nonce: %s", init.AuthURL)
	}
	if len(store.oauthFlows) != 1 {
		t.Fatalf("stored oauth flow count = %d, want 1", len(store.oauthFlows))
	}
	for _, flow := range store.oauthFlows {
		if flow.Provider != SocialProviderOIDC {
			t.Fatalf("stored provider = %q, want %q", flow.Provider, SocialProviderOIDC)
		}
		if len(flow.NonceHash) == 0 || strings.TrimSpace(flow.PKCEVerifier) == "" {
			t.Fatalf("flow missing nonce hash or PKCE verifier: %+v", flow)
		}
	}

	user, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{
		TenantID: 1, Provider: "OIDC:corp-sso", State: init.State, Code: "oidc-code",
	})
	if err != nil {
		t.Fatalf("CompleteOAuth oidc slug: %v", err)
	}
	if user.TenantID != 1 || user.Email != "oidc@example.test" || user.SocialLoginProvider != SocialProviderOIDC {
		t.Fatalf("oidc slug flow linked wrong user: %+v", user)
	}
}

func TestApplyVerifiedSocialIdentityScopesNewProvidersByTenant(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 3, 9, 30, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.Now = func() time.Time { return now }

	first, err := svc.applyVerifiedSocialIdentity(ctx, 1, VerifiedIdentity{
		Provider: SocialProviderLinuxDo, Subject: "shared-subject", Email: "first@example.test",
		EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("apply linuxdo tenant 1: %v", err)
	}
	second, err := svc.applyVerifiedSocialIdentity(ctx, 2, VerifiedIdentity{
		Provider: SocialProviderLinuxDo, Subject: "shared-subject", Email: "second@example.test",
		EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("apply linuxdo tenant 2: %v", err)
	}
	if first.TenantID != 1 || second.TenantID != 2 || first.ID == second.ID {
		t.Fatalf("tenant-scoped linuxdo identities crossed tenants: first=%+v second=%+v", first, second)
	}
	linkedSecond, err := store.GetUserBySocialIdentity(ctx, 2, SocialProviderLinuxDo, "shared-subject")
	if err != nil {
		t.Fatalf("lookup tenant 2 linuxdo identity: %v", err)
	}
	if linkedSecond.ID != second.ID {
		t.Fatalf("tenant 2 identity resolved user %d, want %d", linkedSecond.ID, second.ID)
	}
}

func TestAuthVerifiedSocialLinkPreservesPasswordRecovery(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 11, 15, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.PasswordPolicy = PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	svc.Now = func() time.Time { return now }
	registered, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "link@example.test", Password: "secret"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	originalHash := registered.User.PasswordHash
	linked, err := svc.applyVerifiedSocialIdentity(ctx, 1, VerifiedIdentity{
		Provider: SocialProviderGoogle, Subject: "google-link",
		Email: "link@example.test", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("SocialLogin link: %v", err)
	}
	if linked.ID != registered.User.ID || linked.PasswordHash != originalHash || linked.Status != UserStatusActive {
		t.Fatalf("social link did not preserve local recovery path: %+v", linked)
	}
	linked, err = svc.applyVerifiedSocialIdentity(ctx, 1, VerifiedIdentity{
		Provider: SocialProviderGitHub, Subject: "github-link",
		Email: "link@example.test", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("second SocialLogin link: %v", err)
	}
	if linked.ID != registered.User.ID || linked.PasswordHash != originalHash || linked.SocialLoginProvider != SocialProviderGitHub {
		t.Fatalf("second social link changed wrong user or removed password recovery: %+v", linked)
	}
}

func TestInviteRedemptionRollsBackWhenUserCreateFails(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 11, 30, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	rawInvite, inviteHash, err := GenerateInviteCode()
	if err != nil {
		t.Fatalf("GenerateInviteCode: %v", err)
	}
	store.invites[inviteHash] = InviteCode{Code: inviteHash, TenantID: 1, MaxUses: 1, Status: "active", CreatedAt: now, UpdatedAt: now, CommunityInvitationID: 77, CreatedBy: 7001}
	store.failCreate = true
	svc := NewService(store)
	svc.InviteRequired = true
	svc.PasswordPolicy = PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	svc.Now = func() time.Time { return now }
	if _, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "rollback@example.test", Password: "secret", InviteCode: rawInvite}); err == nil {
		t.Fatal("Register should fail when CreateUser fails")
	}
	if invite := store.invites[inviteHash]; invite.UsedCount != 0 || invite.Status != "active" {
		t.Fatalf("invite consumed despite user create rollback: %+v", invite)
	}
	if len(store.bindings) != 0 || len(store.emailTokens) != 0 || len(store.communityReferrals) != 0 {
		t.Fatalf("rollback left bindings/tokens/referrals: bindings=%+v tokens=%+v referrals=%+v", store.bindings, store.emailTokens, store.communityReferrals)
	}
}

func TestRegisterDisabledPolicyRejectsPublicSignup(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.RegistrationMode = RegistrationModeDisabled
	svc.RequireVerified = false
	svc.PasswordPolicy = PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	svc.Now = func() time.Time { return now }

	_, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "blocked@example.test", Password: "secret"})
	if !errors.Is(err, ErrRegistrationDisabled) {
		t.Fatalf("Register under disabled registration mode = %v, want ErrRegistrationDisabled", err)
	}
	if len(store.users) != 0 {
		t.Fatalf("disabled registration persisted users: %+v", store.users)
	}
	if len(store.invites) != 0 || len(store.bindings) != 0 || len(store.communityReferrals) != 0 {
		t.Fatalf("disabled registration touched invite state: invites=%+v bindings=%+v referrals=%+v", store.invites, store.bindings, store.communityReferrals)
	}
}

func TestRegisterCommunityInvitationCreatesPendingReferral(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 8, 30, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	rawInvite := "HKI-COMM-1234"
	inviteHash := HashInviteCode(rawInvite)
	store.invites[inviteHash] = InviteCode{
		Code:                  inviteHash,
		TenantID:              1,
		CreatedBy:             9001,
		MaxUses:               1,
		Status:                "active",
		CreatedAt:             now,
		UpdatedAt:             now,
		CommunityInvitationID: 55,
	}
	svc := NewService(store)
	svc.RegistrationMode = RegistrationModeInviteRequired
	svc.RequireVerified = false
	svc.PasswordPolicy = PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	svc.Now = func() time.Time { return now }

	registered, err := svc.Register(ctx, RegisterInput{
		TenantID: 1, Email: "referred@example.test", Password: "secret", InviteCode: rawInvite,
	})
	if err != nil {
		t.Fatalf("Register with community invitation: %v", err)
	}
	if registered.User.InviteCodeUsed != inviteHash {
		t.Fatalf("registered invite hash = %q, want %q", registered.User.InviteCodeUsed, inviteHash)
	}
	if len(store.bindings) != 1 || store.bindings[0].InviteCode != inviteHash || store.bindings[0].UserID != registered.User.ID {
		t.Fatalf("invite binding not created for registered user: %+v", store.bindings)
	}
	if len(store.communityReferrals) != 1 {
		t.Fatalf("community referral count = %d, want 1; rows=%+v", len(store.communityReferrals), store.communityReferrals)
	}
	referral := store.communityReferrals[0]
	if referral.TenantID != 1 || referral.RefereeUserID != registered.User.ID || referral.ReferrerUserID != 9001 || referral.InvitationID != 55 {
		t.Fatalf("community referral mismatch: %+v, registered user=%+v", referral, registered.User)
	}
}

type memoryAuthStore struct {
	mu                 sync.Mutex
	now                time.Time
	nextID             int64
	users              map[int64]User
	byEmail            map[string]int64
	emailTokens        map[string]TokenChallenge
	resetTokens        map[string]resetChallenge
	invites            map[string]InviteCode
	oauthFlows         map[string]OAuthFlowSession
	socialLinks        map[string]int64
	bindings           []InviteBinding
	communityReferrals []communityReferralRecord
	failCreate         bool
}

type communityReferralRecord struct {
	TenantID       int64
	RefereeUserID  int64
	ReferrerUserID int64
	InvitationID   int64
}

type resetChallenge struct {
	TokenChallenge
	PasswordVersion int
	Consumed        bool
}

type fakeOAuthProvider struct {
	provider     string
	identity     VerifiedIdentity
	lastVerifier string
	exchanges    int
}

func (p *fakeOAuthProvider) Provider() string { return p.provider }

func (p *fakeOAuthProvider) AuthorizationURL(challenge OAuthFlowChallenge) (string, error) {
	return "https://auth.example.test/authorize?state=" + url.QueryEscape(challenge.State) +
		"&nonce=" + url.QueryEscape(challenge.Nonce) +
		"&code_challenge=" + url.QueryEscape(challenge.PKCEChallenge), nil
}

func (p *fakeOAuthProvider) ExchangeVerifiedIdentity(_ context.Context, flow OAuthFlowSession, code string) (VerifiedIdentity, error) {
	p.exchanges++
	if strings.TrimSpace(code) == "" || flow.PKCEVerifier == "" {
		return VerifiedIdentity{}, ErrSocialLoginRejected
	}
	p.lastVerifier = flow.PKCEVerifier
	return p.identity, nil
}

func newMemoryAuthStore(now time.Time) *memoryAuthStore {
	return &memoryAuthStore{
		now:         now,
		nextID:      1,
		users:       map[int64]User{},
		byEmail:     map[string]int64{},
		emailTokens: map[string]TokenChallenge{},
		resetTokens: map[string]resetChallenge{},
		invites:     map[string]InviteCode{},
		oauthFlows:  map[string]OAuthFlowSession{},
		socialLinks: map[string]int64{},
	}
}

func (s *memoryAuthStore) CreateUser(_ context.Context, in CreateUserParams) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failCreate {
		return User{}, errors.New("forced create user failure")
	}
	email := NormalizeEmail(in.Email)
	if _, exists := s.byEmail[emailKey(in.TenantID, email)]; exists {
		return User{}, ErrDuplicateUser
	}
	user := User{
		ID: in.TenantID*1000 + s.nextID, TenantID: in.TenantID, Email: email, DisplayName: in.DisplayName,
		PasswordHash: in.PasswordHash, EmailVerified: in.EmailVerified, InviteCodeUsed: in.InviteCodeUsed,
		SocialLoginProvider: in.SocialLoginProvider, Status: in.Status, PasswordVersion: 1,
		CreatedAt: s.now, UpdatedAt: s.now,
	}
	if user.Status == "" {
		user.Status = UserStatusPendingVerification
	}
	s.nextID++
	s.users[user.ID] = user
	s.byEmail[emailKey(user.TenantID, user.Email)] = user.ID
	return user, nil
}

func (s *memoryAuthStore) WithTx(_ context.Context, fn func(Store) error) error {
	s.mu.Lock()
	users := cloneUserMap(s.users)
	byEmail := cloneInt64Map(s.byEmail)
	emailTokens := cloneTokenMap(s.emailTokens)
	resetTokens := cloneResetMap(s.resetTokens)
	invites := cloneInviteMap(s.invites)
	oauthFlows := cloneOAuthFlowMap(s.oauthFlows)
	socialLinks := cloneInt64Map(s.socialLinks)
	bindings := append([]InviteBinding(nil), s.bindings...)
	communityReferrals := append([]communityReferralRecord(nil), s.communityReferrals...)
	nextID := s.nextID
	s.mu.Unlock()

	if err := fn(s); err != nil {
		s.mu.Lock()
		s.users = users
		s.byEmail = byEmail
		s.emailTokens = emailTokens
		s.resetTokens = resetTokens
		s.invites = invites
		s.oauthFlows = oauthFlows
		s.socialLinks = socialLinks
		s.bindings = bindings
		s.communityReferrals = communityReferrals
		s.nextID = nextID
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *memoryAuthStore) GetUserByEmail(_ context.Context, tenantID int64, email string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byEmail[emailKey(tenantID, NormalizeEmail(email))]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return s.users[id], nil
}

func (s *memoryAuthStore) GetUserByID(_ context.Context, tenantID, userID int64) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok || user.TenantID != tenantID {
		return User{}, ErrUserNotFound
	}
	return user, nil
}

func (s *memoryAuthStore) UpdateDisplayName(_ context.Context, tenantID, userID int64, displayName string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok || user.TenantID != tenantID {
		return User{}, ErrUserNotFound
	}
	user.DisplayName = displayName
	user.UpdatedAt = s.now
	s.users[user.ID] = user
	return user, nil
}

func (s *memoryAuthStore) MarkLoginSuccess(_ context.Context, tenantID, userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.users[userID]
	if user.TenantID == tenantID {
		user.FailedLoginCount = 0
		user.LockedUntil = nil
		s.users[userID] = user
	}
	return nil
}
func (s *memoryAuthStore) MarkLoginFailure(_ context.Context, tenantID, userID int64, threshold int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.users[userID]
	if user.TenantID == tenantID {
		user.FailedLoginCount++
		if user.FailedLoginCount >= threshold {
			user.Status = UserStatusLocked
		}
		s.users[userID] = user
	}
	return nil
}

func (s *memoryAuthStore) GetUserBySocialIdentity(_ context.Context, tenantID int64, provider, subject string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.socialLinks[emailKey(tenantID, normalizeSocialProvider(provider)+":"+strings.TrimSpace(subject))]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return s.users[id], nil
}

func (s *memoryAuthStore) LinkSocialIdentity(_ context.Context, tenantID, userID int64, provider, subject string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok || user.TenantID != tenantID {
		return User{}, ErrUserNotFound
	}
	user.SocialLoginProvider = provider
	user.EmailVerified = true
	user.Status = UserStatusActive
	s.users[user.ID] = user
	s.socialLinks[emailKey(tenantID, normalizeSocialProvider(provider)+":"+strings.TrimSpace(subject))] = user.ID
	return user, nil
}

func (s *memoryAuthStore) CreateEmailVerificationToken(_ context.Context, challenge TokenChallenge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emailTokens[string(challenge.TokenHash)] = challenge
	return nil
}

func (s *memoryAuthStore) ConsumeEmailVerificationToken(_ context.Context, tenantID int64, tokenHash []byte, now time.Time) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.emailTokens[string(tokenHash)]
	if !ok || challenge.TenantID != tenantID || !challenge.ExpiresAt.After(now) {
		return User{}, ErrTokenInvalid
	}
	delete(s.emailTokens, string(tokenHash))
	user := s.users[challenge.UserID]
	user.EmailVerified = true
	user.Status = UserStatusActive
	user.UpdatedAt = now
	s.users[user.ID] = user
	return user, nil
}

func (s *memoryAuthStore) CreatePasswordResetToken(_ context.Context, challenge TokenChallenge, passwordVersion int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetTokens[string(challenge.TokenHash)] = resetChallenge{TokenChallenge: challenge, PasswordVersion: passwordVersion}
	return nil
}

func (s *memoryAuthStore) ConsumePasswordResetToken(_ context.Context, tenantID int64, tokenHash []byte, passwordHash string, now time.Time) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.resetTokens[string(tokenHash)]
	if !ok || challenge.Consumed || challenge.TenantID != tenantID || !challenge.ExpiresAt.After(now) {
		return User{}, ErrTokenInvalid
	}
	user := s.users[challenge.UserID]
	if user.PasswordVersion != challenge.PasswordVersion {
		return User{}, ErrTokenInvalid
	}
	challenge.Consumed = true
	s.resetTokens[string(tokenHash)] = challenge
	user.PasswordHash = passwordHash
	user.PasswordVersion++
	user.Status = UserStatusActive
	user.UpdatedAt = now
	s.users[user.ID] = user
	return user, nil
}

func (s *memoryAuthStore) RedeemInvite(_ context.Context, tenantID int64, rawCode string, now time.Time) (InviteCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := HashInviteCode(rawCode)
	invite, ok := s.invites[hash]
	if !ok || invite.TenantID != tenantID || invite.Status != "active" || invite.UsedCount >= invite.MaxUses {
		return InviteCode{}, ErrInviteInvalid
	}
	if invite.ValidUntil != nil && !invite.ValidUntil.After(now) {
		return InviteCode{}, ErrInviteInvalid
	}
	invite.UsedCount++
	if invite.UsedCount >= invite.MaxUses {
		invite.Status = "exhausted"
	}
	invite.UpdatedAt = now
	s.invites[hash] = invite
	return invite, nil
}

func (s *memoryAuthStore) CreateInviteBinding(_ context.Context, tenantID, userID int64, inviteCodeHash string, redeemedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings = append(s.bindings, InviteBinding{
		ID: strconv.FormatInt(int64(len(s.bindings)+1), 10), TenantID: tenantID, UserID: userID,
		InviteCode: inviteCodeHash, RedeemedAt: redeemedAt,
	})
	return nil
}

func (s *memoryAuthStore) CreateCommunityReferral(_ context.Context, tenantID, refereeUserID, referrerUserID, invitationID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.communityReferrals = append(s.communityReferrals, communityReferralRecord{
		TenantID: tenantID, RefereeUserID: refereeUserID, ReferrerUserID: referrerUserID, InvitationID: invitationID,
	})
	return nil
}

func (s *memoryAuthStore) CreateOAuthFlowSession(_ context.Context, challenge OAuthFlowChallenge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oauthFlows[string(challenge.StateHash)] = OAuthFlowSession{
		ID: challenge.ID, TenantID: challenge.TenantID, Provider: challenge.Provider,
		StateHash: challenge.StateHash, NonceHash: challenge.NonceHash, PKCEVerifier: challenge.PKCEVerifier,
		RedirectURI: challenge.RedirectURI, ExpiresAt: challenge.ExpiresAt, CreatedAt: s.now,
	}
	return nil
}

func (s *memoryAuthStore) ConsumeOAuthFlowSession(_ context.Context, tenantID int64, provider string, stateHash []byte, now time.Time) (OAuthFlowSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	flow, ok := s.oauthFlows[string(stateHash)]
	if !ok || flow.TenantID != tenantID || flow.Provider != normalizeSocialProvider(provider) {
		return OAuthFlowSession{}, ErrOAuthFlowNotFound
	}
	if flow.ConsumedAt != nil || !flow.ExpiresAt.After(now) {
		return OAuthFlowSession{}, ErrOAuthFlowExpired
	}
	t := now.UTC()
	flow.ConsumedAt = &t
	s.oauthFlows[string(stateHash)] = flow
	return flow, nil
}

func emailKey(tenantID int64, email string) string {
	return strconv.FormatInt(tenantID, 10) + ":" + email
}

func cloneUserMap(in map[int64]User) map[int64]User {
	out := make(map[int64]User, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneInt64Map(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneTokenMap(in map[string]TokenChallenge) map[string]TokenChallenge {
	out := make(map[string]TokenChallenge, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneResetMap(in map[string]resetChallenge) map[string]resetChallenge {
	out := make(map[string]resetChallenge, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneInviteMap(in map[string]InviteCode) map[string]InviteCode {
	out := make(map[string]InviteCode, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneOAuthFlowMap(in map[string]OAuthFlowSession) map[string]OAuthFlowSession {
	out := make(map[string]OAuthFlowSession, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
