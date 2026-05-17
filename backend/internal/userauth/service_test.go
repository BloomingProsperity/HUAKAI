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

func TestAT_AUTH_007_010_OAuthRejectsUnverifiedProviderClaims(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 11, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.Now = func() time.Time { return now }
	svc.OAuth = NewOAuthService(&fakeOAuthProvider{
		provider: SocialProviderGoogle,
		identity: VerifiedIdentity{Provider: SocialProviderGoogle, Subject: "sub", Email: "unverified@example.test", EmailVerified: false},
	})
	init, err := svc.StartOAuth(ctx, OAuthInitInput{TenantID: 1, Provider: SocialProviderGoogle})
	if err != nil {
		t.Fatalf("StartOAuth: %v", err)
	}
	if _, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{TenantID: 1, Provider: SocialProviderGoogle, State: "attacker-state", Code: "code"}); !errors.Is(err, ErrOAuthFlowNotFound) {
		t.Fatalf("state mismatch = %v, want ErrOAuthFlowNotFound", err)
	}
	if _, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{TenantID: 1, Provider: SocialProviderGoogle, State: init.State, Code: "code"}); !errors.Is(err, ErrSocialLoginRejected) {
		t.Fatalf("unverified provider email = %v, want ErrSocialLoginRejected", err)
	}
	if len(store.users) != 0 {
		t.Fatalf("unverified social claim created users: %+v", store.users)
	}
}

func TestAT_AUTH_007_009_VerifiedSocialLinkPreservesPasswordRecovery(t *testing.T) {
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
	store.invites[inviteHash] = InviteCode{Code: inviteHash, TenantID: 1, MaxUses: 1, Status: "active", CreatedAt: now, UpdatedAt: now}
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
	if len(store.bindings) != 0 || len(store.emailTokens) != 0 {
		t.Fatalf("rollback left bindings/tokens: bindings=%+v tokens=%+v", store.bindings, store.emailTokens)
	}
}

type memoryAuthStore struct {
	mu          sync.Mutex
	now         time.Time
	nextID      int64
	users       map[int64]User
	byEmail     map[string]int64
	emailTokens map[string]TokenChallenge
	resetTokens map[string]resetChallenge
	invites     map[string]InviteCode
	oauthFlows  map[string]OAuthFlowSession
	socialLinks map[string]int64
	bindings    []InviteBinding
	failCreate  bool
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
}

func (p *fakeOAuthProvider) Provider() string { return p.provider }

func (p *fakeOAuthProvider) AuthorizationURL(challenge OAuthFlowChallenge) (string, error) {
	return "https://auth.example.test/authorize?state=" + url.QueryEscape(challenge.State) +
		"&nonce=" + url.QueryEscape(challenge.Nonce) +
		"&code_challenge=" + url.QueryEscape(challenge.PKCEChallenge), nil
}

func (p *fakeOAuthProvider) ExchangeVerifiedIdentity(_ context.Context, flow OAuthFlowSession, code string) (VerifiedIdentity, error) {
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
