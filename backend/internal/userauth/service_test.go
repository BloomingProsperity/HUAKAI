package userauth

import (
	"context"
	"errors"
	"github.com/BloomingProsperity/HUAKAI/internal/emailpolicy"
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

	registered, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "skip@example.test", Password: "secret12"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if registered.VerificationToken != "" {
		t.Fatalf("verification token = %q, want empty when policy skips verification", registered.VerificationToken)
	}
	if !registered.User.EmailVerified || registered.User.Status != UserStatusActive {
		t.Fatalf("registered state = verified:%v status:%s", registered.User.EmailVerified, registered.User.Status)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "skip@example.test", Password: "secret12"}); err != nil {
		t.Fatalf("Authenticate with verification disabled: %v", err)
	}
}

type staticVerificationPolicy bool

func (p staticVerificationPolicy) EmailVerificationEnabled(context.Context, int64) (bool, error) {
	return bool(p), nil
}

// erroringVerificationPolicy 是可配置的验证策略桩:返回预设的 (enabled, err)。
// 现有 staticVerificationPolicy 永远返回 nil err,无法覆盖"DB 真错"分支;本桩专为守护
// 邮箱门软化后的运行时安全不变量(DB 错时 fail-safe 要求验证、不绕过)。
type erroringVerificationPolicy struct {
	enabled bool
	err     error
}

func (p erroringVerificationPolicy) EmailVerificationEnabled(context.Context, int64) (bool, error) {
	return p.enabled, p.err
}

// TestRequireEmailVerificationFailsSafeOnError 守护邮箱门软化切片的核心运行时不变量:
// production 启动期 fail-loud 门撤掉后,整个安全论证转嫁到请求时 fail-safe——当
// EmailVerificationEnabled 因 DB 真错返回 (_, err) 时,必须落到 RequireVerified 要求验证,
// 绝不能把 DB 错误当作"验证关闭"而放行未验证用户注册为 active。
//
// 自证式 + 变异检查:同为 enabled=false,err!=nil 应得"要求验证"(true)、err==nil 应得
// "不要求"(false),两路结果必须相反;把 service.go 的 `if err == nil { return enabled }`
// 改成吞 err 的 `return enabled`,err!=nil 那路即由 true 变 false(本测试 RED)。
func TestRequireEmailVerificationFailsSafeOnError(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 23, 8, 0, 0, 0, time.UTC)
	svc := NewService(newMemoryAuthStore(now))
	svc.RequireVerified = true // 生产默认(service.go NewService 即此),fail-safe 的兜底值

	// DB 真错:必须 fail-safe 要求验证。
	svc.Verification = erroringVerificationPolicy{enabled: false, err: errors.New("settings store unavailable")}
	gotOnErr := svc.requireEmailVerification(ctx, 1)
	if !gotOnErr {
		t.Fatal("DB 错时必须 fail-safe 要求验证(不得把错误当\"验证关闭\"放行未验证用户)")
	}

	// 正常未配(无错、enabled=false):不要求验证,与上相反。
	svc.Verification = erroringVerificationPolicy{enabled: false, err: nil}
	gotOnOK := svc.requireEmailVerification(ctx, 1)
	if gotOnOK {
		t.Fatal("err==nil 且 enabled=false 时应不要求验证(正常未配走惰性放行)")
	}

	if gotOnErr == gotOnOK {
		t.Fatal("自证失败:DB 错与正常未配两路结果应相反,否则吞 err 的回退无法被判别")
	}
}

func TestPasswordRegisterToggle(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		gate    RegistrationGate
		wantErr error
	}{
		{name: "nil_gate_allows"},
		{name: "gate_true_allows", gate: staticRegistrationGate{registerAllowed: true, loginAllowed: true}},
		{name: "gate_false_rejects", gate: staticRegistrationGate{registerAllowed: false, loginAllowed: true}, wantErr: ErrPasswordRegistrationDisabled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryAuthStore(now)
			svc := NewService(store)
			svc.RequireVerified = false
			svc.PasswordPolicy = cheapPasswordPolicy()
			svc.Now = func() time.Time { return now }
			svc.RegistrationGate = tc.gate

			_, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "user@example.test", Password: "secret12"})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Register err=%v want %v; MUTATION: reversing the false gate into allow makes the reject case persist a user", err, tc.wantErr)
			}
			if tc.wantErr != nil && len(store.users) != 0 {
				t.Fatalf("password registration disabled persisted users: %+v", store.users)
			}
			if tc.wantErr == nil && len(store.users) != 1 {
				t.Fatalf("allowed registration user count=%d want 1", len(store.users))
			}
		})
	}
}

func TestPasswordLoginToggle(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 8, 30, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	gate := &mutableRegistrationGate{registerAllowed: true, loginAllowed: true}
	svc := NewService(store)
	svc.RequireVerified = false
	svc.PasswordPolicy = cheapPasswordPolicy()
	svc.Now = func() time.Time { return now }
	svc.RegistrationGate = gate

	if _, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "login@example.test", Password: "secret12"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	gate.loginAllowed = false

	orig := verifyPasswordFn
	t.Cleanup(func() { verifyPasswordFn = orig })
	var calls int
	var lastHash string
	verifyPasswordFn = func(encoded, password string) (bool, error) {
		calls++
		lastHash = encoded
		return orig(encoded, password)
	}

	_, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "login@example.test", Password: "secret12"})
	if !errors.Is(err, ErrPasswordLoginDisabled) {
		t.Fatalf("Authenticate with password login disabled err=%v want ErrPasswordLoginDisabled; MUTATION: checking only after successful credentials and allowing valid credentials turns this red", err)
	}
	if calls != 1 {
		t.Fatalf("disabled password login ran %d password verifications, want exactly 1 dummy argon2 equal-work call", calls)
	}
	if lastHash != timingEqualizationHash {
		t.Fatalf("disabled password login verified hash=%q want timingEqualizationHash; moving the check after GetUserByEmail/real hash reopens a switch-state timing oracle", lastHash)
	}
}

func TestRegisterEmailPolicyWiring(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		email   string
		policy  EmailPolicy
		wantErr error
	}{
		{name: "nil_policy_allows", email: "user@evil.test"},
		{name: "domain_rejected", email: "user@evil.test", policy: staticEmailPolicy{domainEnabled: true, domainList: "example.test"}, wantErr: emailpolicy.ErrEmailDomainNotAllowed},
		{name: "domain_allowed", email: "user@example.test", policy: staticEmailPolicy{domainEnabled: true, domainList: "example.test"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryAuthStore(now)
			svc := NewService(store)
			svc.RequireVerified = false
			svc.PasswordPolicy = cheapPasswordPolicy()
			svc.Now = func() time.Time { return now }
			svc.EmailPolicy = tc.policy

			_, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: tc.email, Password: "secret12"})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Register err=%v want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil && len(store.users) != 0 {
				t.Fatalf("rejected email policy persisted users: %+v", store.users)
			}
		})
	}
}

type staticRegistrationGate struct {
	registerAllowed bool
	loginAllowed    bool
}

func (g staticRegistrationGate) PasswordRegistrationAllowed(context.Context, int64) (bool, error) {
	return g.registerAllowed, nil
}

func (g staticRegistrationGate) PasswordLoginAllowed(context.Context, int64) (bool, error) {
	return g.loginAllowed, nil
}

type mutableRegistrationGate struct {
	registerAllowed bool
	loginAllowed    bool
}

func (g *mutableRegistrationGate) PasswordRegistrationAllowed(context.Context, int64) (bool, error) {
	return g.registerAllowed, nil
}

func (g *mutableRegistrationGate) PasswordLoginAllowed(context.Context, int64) (bool, error) {
	return g.loginAllowed, nil
}

type staticEmailPolicy struct {
	domainEnabled bool
	domainList    string
	aliasEnabled  bool
	reservedList  string
}

func (p staticEmailPolicy) EmailDomainAllowlist(context.Context, int64) (bool, string, error) {
	return p.domainEnabled, p.domainList, nil
}

func (p staticEmailPolicy) EmailAliasRestrictionEnabled(context.Context, int64) (bool, error) {
	return p.aliasEnabled, nil
}

func (p staticEmailPolicy) ReservedEmailLocalparts(context.Context, int64) (string, error) {
	return p.reservedList, nil
}

func cheapPasswordPolicy() PasswordPolicy {
	return PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
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
	registered, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "lock@example.test", Password: "secret12"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "lock@example.test", Password: "bad"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("first bad password = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "lock@example.test", Password: "bad"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("second bad password = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "lock@example.test", Password: "secret12"}); !errors.Is(err, ErrUserLocked) {
		t.Fatalf("locked correct password = %v, want ErrUserLocked", err)
	}
	user := store.users[registered.User.ID]
	user.Status = UserStatusResetRequired
	user.FailedLoginCount = 0
	store.users[user.ID] = user
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "lock@example.test", Password: "secret12"}); !errors.Is(err, ErrPasswordResetRequired) {
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

func TestUnlockUserClearsLockout(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.RequireVerified = false
	svc.LockoutThreshold = 2
	svc.PasswordPolicy = cheapPasswordPolicy()
	svc.Now = func() time.Time { return now }

	registered, err := svc.Register(ctx, RegisterInput{TenantID: 7, Email: "unlock@example.test", Password: "secret12"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 7, Email: "unlock@example.test", Password: "bad"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("first bad password = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 7, Email: "unlock@example.test", Password: "bad"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("second bad password = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 7, Email: "unlock@example.test", Password: "secret12"}); !errors.Is(err, ErrUserLocked) {
		t.Fatalf("locked login = %v, want ErrUserLocked", err)
	}

	unlocked, err := svc.UnlockUser(ctx, 7, registered.User.ID)
	if err != nil {
		t.Fatalf("UnlockUser: %v", err)
	}
	if unlocked.Status != UserStatusActive || unlocked.FailedLoginCount != 0 || unlocked.LockedUntil != nil {
		t.Fatalf("unlocked state = status:%s failed:%d locked_until:%v, want active/0/nil",
			unlocked.Status, unlocked.FailedLoginCount, unlocked.LockedUntil)
	}
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 7, Email: "unlock@example.test", Password: "secret12"}); err != nil {
		t.Fatalf("login after admin unlock: %v", err)
	}
	if _, err := svc.UnlockUser(ctx, 8, registered.User.ID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("cross-tenant unlock err=%v want ErrUserNotFound", err)
	}
	if _, err := svc.UnlockUser(ctx, 7, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid unlock id err=%v want ErrInvalidInput", err)
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
	existing, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "user@example.test", Password: "secret12"})
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

// TestStartOAuthRedirectAllowlist 守护一点: 调用方提供的 redirect_uri 必须被拒,
// 除非它与配置的 allowlist 精确匹配 (fail-closed); 空的 redirect_uri 允许,
// 此时回退到 provider 的服务端 RedirectURI。防止 open-redirect / 授权码
// 被劫持到攻击者可控的 callback。
//
// 变异检查: 删掉 StartOAuth 里的 validateOAuthRedirectURI 调用, "不在 allowlist 内"
// 的 case 就会被接受 → 红。判别性: provider/tenant 相同, 只有 redirect_uri + allowlist 变化。
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
	// (1) 调用方的 redirect_uri 不在 (空的) allowlist 内 → fail-closed 被拒。
	if _, err := newSvc().StartOAuth(ctx, OAuthInitInput{TenantID: 1, Provider: SocialProviderGoogle, RedirectURI: "https://evil.example.test/steal"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("caller redirect_uri not in allowlist must be rejected; got %v", err)
	}
	// (2) 调用方的 redirect_uri 精确命中 allowlist → 接受。
	if _, err := newSvc("https://app.example.test/cb").StartOAuth(ctx, OAuthInitInput{TenantID: 1, Provider: SocialProviderGoogle, RedirectURI: "https://app.example.test/cb"}); err != nil {
		t.Fatalf("allowlisted redirect_uri must be accepted; got %v", err)
	}
	// (3) 空的 redirect_uri → 接受 (使用服务端默认 callback)。
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
		{"discord", " DISCORD ", SocialProviderDiscord},
		{"telegram", "Telegram", SocialProviderTelegram},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeSocialProvider(tc.in); got != tc.want {
				t.Fatalf("normalizeSocialProvider(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDiscordOAuthCompleteUsesNormalizedProviderAndSubjectLink(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 9, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.Now = func() time.Time { return now }
	svc.OAuth = NewOAuthService(&fakeOAuthProvider{
		provider: SocialProviderDiscord,
		identity: VerifiedIdentity{
			Provider: SocialProviderDiscord, Subject: "discord-subject",
			Email: "discord@example.test", DisplayName: "Discord User", EmailVerified: true,
		},
	})
	init, err := svc.StartOAuth(ctx, OAuthInitInput{TenantID: 1, Provider: " DISCORD "})
	if err != nil {
		t.Fatalf("StartOAuth Discord: %v", err)
	}
	user, err := svc.CompleteOAuth(ctx, OAuthCallbackInput{
		TenantID: 1, Provider: "discord", State: init.State, Code: "discord-code",
	})
	if err != nil {
		t.Fatalf("CompleteOAuth Discord: %v", err)
	}
	if user.TenantID != 1 || user.Email != "discord@example.test" || user.SocialLoginProvider != SocialProviderDiscord {
		t.Fatalf("Discord social signup mismatch: %+v", user)
	}
	linked, err := store.GetUserBySocialIdentity(ctx, 1, SocialProviderDiscord, "discord-subject")
	if err != nil {
		t.Fatalf("lookup Discord social identity: %v", err)
	}
	if linked.ID != user.ID {
		t.Fatalf("Discord subject linked user %d, want %d", linked.ID, user.ID)
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

func TestApplyVerifiedSocialIdentityRejectsTelegramSyntheticEmailPendingVerification(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 9, 30, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.Now = func() time.Time { return now }

	_, err := svc.ApplyVerifiedSocialIdentity(ctx, 1, VerifiedIdentity{
		Provider: SocialProviderTelegram,
		Subject:  "424242",
		Email:    SyntheticOAuthEmail(SocialProviderTelegram, "424242"),
		// Telegram login-widget 并不能证明对 email 的所有权。这里必须保持
		// false, 让共享的 pending-email 路径拒绝, 而不是创建一个用户。
		EmailVerified: false,
	})
	if !errors.Is(err, ErrOAuthPendingEmailRequired) {
		t.Fatalf("Telegram synthetic email identity err=%v want ErrOAuthPendingEmailRequired", err)
	}
	if len(store.users) != 0 || len(store.socialLinks) != 0 {
		t.Fatalf("Telegram pending-email identity persisted users=%+v links=%+v", store.users, store.socialLinks)
	}
}

// TestApplyVerifiedSocialIdentityBoundEmaillessLogsIn 锁定「先绑定后登录」的关键 reorder:
// 一个已绑定的无邮箱社交身份(telegram,EmailVerified 恒 false)再次登录时,必须凭既有绑定直接登录、
// 拿到本人用户,而不是被邮箱门拦成 pending-email。
// 变异(把既有绑定查询改回邮箱门之后,即旧顺序)→ 已绑定的 telegram 身份会先撞 !EmailVerified 返回
// ErrOAuthPendingEmailRequired,本测试第一处断言 RED——正是修掉的「连已绑定用户都登不进」顺序缺陷。
func TestApplyVerifiedSocialIdentityBoundEmaillessLogsIn(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 9, 30, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.Now = func() time.Time { return now }

	// 先有一个真实账号(邮箱+密码),并已把 telegram 身份绑到它(模拟「先绑定」那一步已完成)。
	user, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: 1, Email: "bound@example.test", DisplayName: "Bound",
		PasswordHash: "argon2id-test-hash", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := store.LinkSocialIdentity(ctx, 1, user.ID, SocialProviderTelegram, "tg-bound-1"); err != nil {
		t.Fatalf("LinkSocialIdentity: %v", err)
	}

	// 现在用同一 telegram 身份「登录」(widget 恒 EmailVerified:false)。应直接登录到本人,而非 pending。
	got, err := svc.ApplyVerifiedSocialIdentity(ctx, 1, VerifiedIdentity{
		Provider:      SocialProviderTelegram,
		Subject:       "tg-bound-1",
		Email:         "",
		EmailVerified: false,
	})
	if err != nil {
		t.Fatalf("已绑定 telegram 身份登录应成功,得 err=%v", err)
	}
	if got.ID != user.ID {
		t.Fatalf("登录返回的用户=%d,应为已绑定的本人 %d", got.ID, user.ID)
	}

	// 判别性对照:未绑定的 telegram 身份(不同 subject)仍必须被邮箱门拦成 pending、不建号。
	if _, err := svc.ApplyVerifiedSocialIdentity(ctx, 1, VerifiedIdentity{
		Provider: SocialProviderTelegram, Subject: "tg-unbound-9",
		Email: SyntheticOAuthEmail(SocialProviderTelegram, "tg-unbound-9"), EmailVerified: false,
	}); !errors.Is(err, ErrOAuthPendingEmailRequired) {
		t.Fatalf("未绑定 telegram 身份 err=%v,应 ErrOAuthPendingEmailRequired", err)
	}
}

func TestApplyVerifiedSocialIdentityValidatesNewAccountFields(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.Now = func() time.Time { return now }

	if _, err := svc.ApplyVerifiedSocialIdentity(ctx, 1, VerifiedIdentity{
		Provider: SocialProviderGitHub, Subject: "bad-email", Email: "not-an-email", EmailVerified: true,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("已验证但非法的上游邮箱 err=%v want ErrInvalidInput", err)
	}
	if len(store.users) != 0 {
		t.Fatalf("非法邮箱不得落用户,users=%+v", store.users)
	}

	user, err := svc.ApplyVerifiedSocialIdentity(ctx, 1, VerifiedIdentity{
		Provider: SocialProviderGitHub, Subject: "bad-name", Email: "valid@example.test",
		DisplayName: "bad\x01name", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("上游可选名称异常不应阻断已验证身份建号: %v", err)
	}
	if user.DisplayName != "" {
		t.Fatalf("异常上游名称不得落库,display_name=%q", user.DisplayName)
	}

	if _, err := svc.CompleteSocialSignupWithVerifiedEmail(ctx, 1, VerifiedIdentity{
		Provider: SocialProviderQQ, Subject: "bad-completion-email",
	}, "still-not-an-email"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("补邮箱建号非法邮箱 err=%v want ErrInvalidInput", err)
	}
}

// TestCompleteSocialSignupWithVerifiedEmail 锁定「OAuth 无邮箱补全」建号方法的四条不变量:
// 建号+链身份+邮箱已验证;邮箱被占用拒;既有绑定幂等;注册关时拒。
func TestCompleteSocialSignupWithVerifiedEmail(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.Now = func() time.Time { return now }
	svc.SocialSignup = true

	idQQ := VerifiedIdentity{Provider: SocialProviderQQ, Subject: "qq-1", Email: SyntheticOAuthEmail(SocialProviderQQ, "qq-1"), EmailVerified: false}

	// ① 新身份 + 新邮箱 → 建号 + 链接 + 邮箱已验证(调用方已验码)。
	user, err := svc.CompleteSocialSignupWithVerifiedEmail(ctx, 1, idQQ, "alice@example.test")
	if err != nil {
		t.Fatalf("① 建号应成功: %v", err)
	}
	if user.Email != "alice@example.test" || !user.EmailVerified {
		t.Fatalf("① 建号 email/verified 不对: %+v", user)
	}
	// 链接已落:用 QQ 身份登录应直接到该用户。
	gotLogin, err := svc.ApplyVerifiedSocialIdentity(ctx, 1, idQQ)
	if err != nil || gotLogin.ID != user.ID {
		t.Fatalf("① 链接后登录应到本人,得 user=%d err=%v", gotLogin.ID, err)
	}

	// ② 邮箱已被占用(不同身份)→ ErrEmailExists,防抢注/接管。
	idGH := VerifiedIdentity{Provider: SocialProviderGitHub, Subject: "gh-9", Email: SyntheticOAuthEmail(SocialProviderGitHub, "gh-9"), EmailVerified: false}
	if _, err := svc.CompleteSocialSignupWithVerifiedEmail(ctx, 1, idGH, "alice@example.test"); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("② 邮箱被占用应 ErrEmailExists,得 %v", err)
	}

	// ③ 既有绑定 → 幂等返回本人(不因邮箱不同而重复建号)。
	again, err := svc.CompleteSocialSignupWithVerifiedEmail(ctx, 1, idQQ, "other@example.test")
	if err != nil || again.ID != user.ID {
		t.Fatalf("③ 既有绑定应幂等返回本人,得 user=%d err=%v", again.ID, err)
	}

	// ④ 注册关闭 → 拒(与密码 Register 同闸)。变异:去掉 registrationMode 闸 → 这里会建号,断言 RED。
	svc.RegistrationMode = RegistrationModeDisabled
	if _, err := svc.CompleteSocialSignupWithVerifiedEmail(ctx, 1, idGH, "bob@example.test"); !errors.Is(err, ErrRegistrationDisabled) {
		t.Fatalf("④ 注册关闭应 ErrRegistrationDisabled,得 %v", err)
	}
}

// TestLinkVerifiedSocialIdentity 锁定绑定腿的两条不变量:幂等 + 接管保护。
func TestLinkVerifiedSocialIdentity(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.Now = func() time.Time { return now }

	alice, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: 1, Email: "alice@example.test", PasswordHash: "h", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser alice: %v", err)
	}
	bob, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: 1, Email: "bob@example.test", PasswordHash: "h", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}
	tgIdentity := VerifiedIdentity{
		Provider: SocialProviderTelegram, Subject: "tg-777",
		Email: SyntheticOAuthEmail(SocialProviderTelegram, "tg-777"), EmailVerified: false,
	}

	// alice 首次绑定成功,且之后能凭该绑定登录到 alice。
	if _, err := svc.LinkVerifiedSocialIdentity(ctx, 1, alice.ID, tgIdentity); err != nil {
		t.Fatalf("alice 绑定应成功,得 err=%v", err)
	}
	loggedIn, err := svc.ApplyVerifiedSocialIdentity(ctx, 1, tgIdentity)
	if err != nil || loggedIn.ID != alice.ID {
		t.Fatalf("绑定后凭 telegram 登录应到 alice,得 user=%d err=%v", loggedIn.ID, err)
	}

	// 幂等:alice 再绑同一身份不报错。
	if _, err := svc.LinkVerifiedSocialIdentity(ctx, 1, alice.ID, tgIdentity); err != nil {
		t.Fatalf("alice 重复绑定应幂等成功,得 err=%v", err)
	}

	// 接管保护:bob 试图绑 alice 已占用的同一 telegram 身份 → 必须拒。
	// 变异(去掉 existing.ID != userID 的拒绝)→ bob 抢绑成功,本断言 RED(账号接管漏洞)。
	if _, err := svc.LinkVerifiedSocialIdentity(ctx, 1, bob.ID, tgIdentity); !errors.Is(err, ErrSocialIdentityAlreadyBound) {
		t.Fatalf("bob 抢绑他人已占身份 err=%v,应 ErrSocialIdentityAlreadyBound", err)
	}
	// 抢绑被拒后,该身份仍归 alice。
	if owner, err := store.GetUserBySocialIdentity(ctx, 1, SocialProviderTelegram, "tg-777"); err != nil || owner.ID != alice.ID {
		t.Fatalf("接管被拒后身份应仍归 alice,得 owner=%d err=%v", owner.ID, err)
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
	registered, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "link@example.test", Password: "secret12"})
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

func TestUnlinkSocialIdentityAllowsPasswordBackedUser(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	user, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: 1, Email: "unlink-password@example.test", DisplayName: "Linked",
		PasswordHash: "argon2id-test-hash", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := store.LinkSocialIdentity(ctx, 1, user.ID, SocialProviderGoogle, "google-subject"); err != nil {
		t.Fatalf("LinkSocialIdentity: %v", err)
	}

	unlinked, err := svc.UnlinkSocialIdentity(ctx, 1, user.ID, SocialProviderGoogle)
	if err != nil {
		t.Fatalf("UnlinkSocialIdentity: %v", err)
	}
	if !unlinked {
		t.Fatal("UnlinkSocialIdentity deleted=false, want true")
	}
	if _, err := store.GetUserBySocialIdentity(ctx, 1, SocialProviderGoogle, "google-subject"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("GetUserBySocialIdentity after unlink err=%v want ErrUserNotFound", err)
	}
	got, err := store.GetUserByID(ctx, 1, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID after unlink: %v", err)
	}
	if got.SocialLoginProvider != "" {
		t.Fatalf("social_login_provider=%q want empty after final link removal", got.SocialLoginProvider)
	}
}

func TestUnlinkSocialIdentityRejectsLastLoginMethod(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 10, 15, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	user, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: 1, Email: "social-only@example.test", DisplayName: "Social Only",
		EmailVerified: true, SocialLoginProvider: SocialProviderGoogle, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := store.LinkSocialIdentity(ctx, 1, user.ID, SocialProviderGoogle, "google-only"); err != nil {
		t.Fatalf("LinkSocialIdentity: %v", err)
	}

	if _, err := svc.UnlinkSocialIdentity(ctx, 1, user.ID, SocialProviderGoogle); !errors.Is(err, ErrLastLoginMethod) {
		t.Fatalf("UnlinkSocialIdentity err=%v want ErrLastLoginMethod; MUTATION: deleting the last-login-method guard makes this call succeed", err)
	}
	stillLinked, err := store.GetUserBySocialIdentity(ctx, 1, SocialProviderGoogle, "google-only")
	if err != nil {
		t.Fatalf("social-only link was removed despite lockout guard: %v", err)
	}
	if stillLinked.ID != user.ID {
		t.Fatalf("social identity resolved user=%d want %d", stillLinked.ID, user.ID)
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
	if _, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "rollback@example.test", Password: "secret12", InviteCode: rawInvite}); err == nil {
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

	_, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "blocked@example.test", Password: "secret12"})
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
		TenantID: 1, Email: "referred@example.test", Password: "secret12", InviteCode: rawInvite,
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
	failLink           bool
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

func (s *memoryAuthStore) ClearLockout(_ context.Context, tenantID, userID int64) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok || user.TenantID != tenantID {
		return User{}, ErrUserNotFound
	}
	user.FailedLoginCount = 0
	user.LockedUntil = nil
	if user.Status == UserStatusLocked {
		user.Status = UserStatusActive
	}
	user.UpdatedAt = s.now
	s.users[userID] = user
	return user, nil
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
	// failLink:注入「建用户成功、绑身份失败」以验证注册事务的回滚原子性。
	if s.failLink {
		return User{}, errors.New("forced link social identity failure")
	}
	provider = normalizeSocialProvider(provider)
	user, ok := s.users[userID]
	if !ok || user.TenantID != tenantID || provider == "" || strings.TrimSpace(subject) == "" {
		return User{}, ErrUserNotFound
	}
	user.SocialLoginProvider = provider
	user.EmailVerified = true
	user.Status = UserStatusActive
	s.users[user.ID] = user
	s.socialLinks[emailKey(tenantID, normalizeSocialProvider(provider)+":"+strings.TrimSpace(subject))] = user.ID
	return user, nil
}

func (s *memoryAuthStore) CountUserSocialIdentityLinks(_ context.Context, tenantID, userID int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	for _, linkedUserID := range s.socialLinks {
		if linkedUserID == userID {
			if user, ok := s.users[linkedUserID]; ok && user.TenantID == tenantID {
				count++
			}
		}
	}
	return count, nil
}

func (s *memoryAuthStore) CountSocialIdentityLinks(_ context.Context, tenantID, userID int64, provider string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	provider = normalizeSocialProvider(provider)
	if provider == "" {
		return 0, ErrInvalidInput
	}
	prefix := emailKey(tenantID, provider+":")
	var count int
	for key, linkedUserID := range s.socialLinks {
		if strings.HasPrefix(key, prefix) && linkedUserID == userID {
			count++
		}
	}
	return count, nil
}

func (s *memoryAuthStore) UnlinkSocialIdentity(_ context.Context, tenantID, userID int64, provider string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	provider = normalizeSocialProvider(provider)
	if provider == "" {
		return false, ErrInvalidInput
	}
	prefix := emailKey(tenantID, provider+":")
	var deleted bool
	for key, linkedUserID := range s.socialLinks {
		if strings.HasPrefix(key, prefix) && linkedUserID == userID {
			delete(s.socialLinks, key)
			deleted = true
		}
	}
	if !deleted {
		return false, nil
	}
	user, ok := s.users[userID]
	if !ok || user.TenantID != tenantID {
		return false, ErrUserNotFound
	}
	user.SocialLoginProvider = ""
	tenantPrefix := strconv.FormatInt(tenantID, 10) + ":"
	for key, linkedUserID := range s.socialLinks {
		if linkedUserID != userID || !strings.HasPrefix(key, tenantPrefix) {
			continue
		}
		rest := strings.TrimPrefix(key, tenantPrefix)
		nextProvider, _, ok := strings.Cut(rest, ":")
		if ok {
			user.SocialLoginProvider = nextProvider
			break
		}
	}
	user.UpdatedAt = s.now
	s.users[userID] = user
	return true, nil
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
