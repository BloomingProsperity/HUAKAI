package passkey

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

func TestPasskeyChallengeSingleUse(t *testing.T) {
	// Mutation killed: if ConsumeCeremonySession is replaced with a read-only lookup,
	// the second LoginFinish reuses the same challenge and this test stops failing.
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	engine := &fakeEngine{loginCredentialID: []byte("cred-a"), assertedSignCount: 2}
	users := fakeUsers{rows: map[userKey]userauth.User{
		{tenantID: 1, userID: 101}: testUser(1, 101, "alice@example.test"),
	}}
	svc := NewService(NewMemoryStore(), users, StaticConfigSource(testConfig()), WithCeremonyEngine(engine), WithNow(func() time.Time { return now }))
	if _, err := svc.StoreCredential(ctx, CredentialRecord{
		TenantID: 1, UserID: 101, CredentialID: []byte("cred-a"), PublicKey: []byte("pk-a"), SignCount: 1,
	}); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	begin, err := svc.LoginBegin(ctx, LoginBeginInput{TenantID: 1})
	if err != nil {
		t.Fatalf("LoginBegin: %v", err)
	}
	if _, err := svc.LoginFinish(ctx, LoginFinishInput{TenantID: 1, SessionID: begin.SessionID, CredentialJSON: []byte(`{"id":"cred-a"}`)}); err != nil {
		t.Fatalf("first LoginFinish: %v", err)
	}
	if _, err := svc.LoginFinish(ctx, LoginFinishInput{TenantID: 1, SessionID: begin.SessionID, CredentialJSON: []byte(`{"id":"cred-a"}`)}); !errors.Is(err, ErrCeremonyNotFound) {
		t.Fatalf("second LoginFinish err=%v want ErrCeremonyNotFound", err)
	}
	if engine.finishLoginCalls != 1 {
		t.Fatalf("finishLoginCalls=%d want 1; replay must fail before WebAuthn validation", engine.finishLoginCalls)
	}
}

func TestPasskeyRegisterThenLoginSuccess(t *testing.T) {
	// Mutation killed: if RegisterFinish does not persist the verified credential,
	// the later discoverable LoginFinish cannot resolve the credential owner.
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 10, 2, 0, 0, time.UTC)
	engine := &fakeEngine{loginCredentialID: []byte("registered-cred"), assertedSignCount: 2}
	user := testUser(1, 101, "alice@example.test")
	users := fakeUsers{rows: map[userKey]userauth.User{{tenantID: 1, userID: 101}: user}}
	svc := NewService(NewMemoryStore(), users, StaticConfigSource(testConfig()), WithCeremonyEngine(engine), WithNow(func() time.Time { return now }))

	registerBegin, err := svc.RegisterBegin(ctx, RegisterBeginInput{TenantID: 1, User: user, Name: "MacBook"})
	if err != nil {
		t.Fatalf("RegisterBegin: %v", err)
	}
	registered, err := svc.RegisterFinish(ctx, RegisterFinishInput{
		TenantID: 1, User: user, SessionID: registerBegin.SessionID, CredentialJSON: []byte(`{"id":"registered-cred"}`), Name: "MacBook",
	})
	if err != nil {
		t.Fatalf("RegisterFinish: %v", err)
	}
	if registered.ID == 0 || !bytes.Equal(registered.CredentialID, []byte("registered-cred")) {
		t.Fatalf("registered credential not persisted correctly: %+v", registered)
	}

	loginBegin, err := svc.LoginBegin(ctx, LoginBeginInput{TenantID: 1})
	if err != nil {
		t.Fatalf("LoginBegin: %v", err)
	}
	result, err := svc.LoginFinish(ctx, LoginFinishInput{TenantID: 1, SessionID: loginBegin.SessionID, CredentialJSON: []byte(`{"id":"registered-cred"}`)})
	if err != nil {
		t.Fatalf("LoginFinish: %v", err)
	}
	if result.User.ID != 101 || result.Credential.SignCount != 2 {
		t.Fatalf("login result user=%+v credential=%+v want user 101 sign_count 2", result.User, result.Credential)
	}
}

// TestPasskeyLoginRejectsDisabledUser 守护:passkey 登录在签发 session 前必须复用账号资格门——
// 被管理员禁用的用户即便持有有效 passkey 也不能登录(否则账号停用形同虚设,属 auth-core 访问控制绕过)。
//
// 自证式:同一注册凭据,active 用户 LoginFinish 成功、disabled 用户 LoginFinish 被拒(返
// userauth.ErrUserDisabled 且不返回 user)。变异检查:删掉 LoginFinish 里的 EnsureLoginEligible 门,
// disabled 用例会由"被拒"变成"成功签发",本测试转红。
func TestPasskeyLoginRejectsDisabledUser(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 10, 5, 0, 0, time.UTC)
	engine := &fakeEngine{loginCredentialID: []byte("registered-cred"), assertedSignCount: 2}
	user := testUser(1, 101, "alice@example.test")
	users := fakeUsers{rows: map[userKey]userauth.User{{tenantID: 1, userID: 101}: user}}
	svc := NewService(NewMemoryStore(), users, StaticConfigSource(testConfig()), WithCeremonyEngine(engine), WithNow(func() time.Time { return now }))

	rb, err := svc.RegisterBegin(ctx, RegisterBeginInput{TenantID: 1, User: user, Name: "MacBook"})
	if err != nil {
		t.Fatalf("RegisterBegin: %v", err)
	}
	if _, err := svc.RegisterFinish(ctx, RegisterFinishInput{TenantID: 1, User: user, SessionID: rb.SessionID, CredentialJSON: []byte(`{"id":"registered-cred"}`), Name: "MacBook"}); err != nil {
		t.Fatalf("RegisterFinish: %v", err)
	}

	// 模拟管理员封禁:把该用户置为 disabled,持有效 passkey 登录必须被拒。
	// (不先跑 active 登录,避免 signCount 被顶高后撞 clone 检测,从而让"无资格门则成功签发"
	//  成为干净的变异目标——删掉门后本用例会变成登录成功 err==nil。)
	disabled := user
	disabled.Status = userauth.UserStatusDisabled
	users.rows[userKey{tenantID: 1, userID: 101}] = disabled

	lb2, err := svc.LoginBegin(ctx, LoginBeginInput{TenantID: 1})
	if err != nil {
		t.Fatalf("LoginBegin(disabled): %v", err)
	}
	result, err := svc.LoginFinish(ctx, LoginFinishInput{TenantID: 1, SessionID: lb2.SessionID, CredentialJSON: []byte(`{"id":"registered-cred"}`)})
	if !errors.Is(err, userauth.ErrUserDisabled) {
		t.Fatalf("disabled 用户 LoginFinish err=%v want userauth.ErrUserDisabled", err)
	}
	if result.User.ID != 0 {
		t.Fatalf("disabled 用户不应返回 user(更不应签发 session), got user=%+v", result.User)
	}
}

// TestPasskeyLoginRejectsTimeLockedUser 守护:passkey 资格门与密码门一致,也拒"时间型临时锁"
// (status 仍 active 但 locked_until 在未来)。变异检查:删掉 EnsureLoginEligible 里的 LockedUntil
// 分支,本用例由"被拒(ErrUserLocked)"变成"成功签发(err==nil)",转红。
func TestPasskeyLoginRejectsTimeLockedUser(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 10, 6, 0, 0, time.UTC)
	engine := &fakeEngine{loginCredentialID: []byte("registered-cred"), assertedSignCount: 2}
	user := testUser(1, 101, "alice@example.test")
	users := fakeUsers{rows: map[userKey]userauth.User{{tenantID: 1, userID: 101}: user}}
	svc := NewService(NewMemoryStore(), users, StaticConfigSource(testConfig()), WithCeremonyEngine(engine), WithNow(func() time.Time { return now }))

	rb, err := svc.RegisterBegin(ctx, RegisterBeginInput{TenantID: 1, User: user, Name: "MacBook"})
	if err != nil {
		t.Fatalf("RegisterBegin: %v", err)
	}
	if _, err := svc.RegisterFinish(ctx, RegisterFinishInput{TenantID: 1, User: user, SessionID: rb.SessionID, CredentialJSON: []byte(`{"id":"registered-cred"}`), Name: "MacBook"}); err != nil {
		t.Fatalf("RegisterFinish: %v", err)
	}

	// status 仍 active,但被设置了未来的 locked_until(时间型临时锁);passkey 登录必须与密码门一致地拒。
	locked := user
	until := now.Add(time.Hour)
	locked.LockedUntil = &until
	users.rows[userKey{tenantID: 1, userID: 101}] = locked

	lb, err := svc.LoginBegin(ctx, LoginBeginInput{TenantID: 1})
	if err != nil {
		t.Fatalf("LoginBegin: %v", err)
	}
	result, err := svc.LoginFinish(ctx, LoginFinishInput{TenantID: 1, SessionID: lb.SessionID, CredentialJSON: []byte(`{"id":"registered-cred"}`)})
	if !errors.Is(err, userauth.ErrUserLocked) {
		t.Fatalf("时间锁用户 LoginFinish err=%v want userauth.ErrUserLocked", err)
	}
	if result.User.ID != 0 {
		t.Fatalf("时间锁用户不应签发 session, got user=%+v", result.User)
	}
}

func TestPasskeyRegisterChallengeSingleUse(t *testing.T) {
	// Mutation killed: if RegisterFinish reads but does not delete the ceremony
	// row, replaying the same attestation creates a duplicate credential attempt.
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 10, 3, 0, 0, time.UTC)
	user := testUser(1, 101, "alice@example.test")
	users := fakeUsers{rows: map[userKey]userauth.User{{tenantID: 1, userID: 101}: user}}
	svc := NewService(NewMemoryStore(), users, StaticConfigSource(testConfig()), WithCeremonyEngine(&fakeEngine{}), WithNow(func() time.Time { return now }))
	begin, err := svc.RegisterBegin(ctx, RegisterBeginInput{TenantID: 1, User: user})
	if err != nil {
		t.Fatalf("RegisterBegin: %v", err)
	}
	if _, err := svc.RegisterFinish(ctx, RegisterFinishInput{TenantID: 1, User: user, SessionID: begin.SessionID, CredentialJSON: []byte(`{"id":"registered-cred"}`)}); err != nil {
		t.Fatalf("first RegisterFinish: %v", err)
	}
	if _, err := svc.RegisterFinish(ctx, RegisterFinishInput{TenantID: 1, User: user, SessionID: begin.SessionID, CredentialJSON: []byte(`{"id":"registered-cred"}`)}); !errors.Is(err, ErrCeremonyNotFound) {
		t.Fatalf("second RegisterFinish err=%v want ErrCeremonyNotFound", err)
	}
}

func TestPasskeyOriginBoundToConfig(t *testing.T) {
	// Mutation killed: if request Origin is ignored or accepted from request
	// body, an evil origin can start or finish a passkey ceremony.
	ctx := context.Background()
	users := fakeUsers{rows: map[userKey]userauth.User{{tenantID: 1, userID: 101}: testUser(1, 101, "alice@example.test")}}
	svc := NewService(NewMemoryStore(), users, StaticConfigSource(testConfig()), WithCeremonyEngine(&fakeEngine{}))
	if err := svc.CheckOrigin(ctx, "https://evil.example.test", false); !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("evil origin err=%v want ErrOriginNotAllowed", err)
	}
	if err := svc.CheckOrigin(ctx, "https://example.test", false); err != nil {
		t.Fatalf("configured origin rejected: %v", err)
	}
}

func TestPasskeySignCountRegressionRejected(t *testing.T) {
	// Mutation killed: removing the assertedSignCount <= stored SignCount guard
	// accepts a replayed/cloned authenticator assertion and updates last_used_at.
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 10, 5, 0, 0, time.UTC)
	engine := &fakeEngine{loginCredentialID: []byte("cred-a"), assertedSignCount: 7}
	users := fakeUsers{rows: map[userKey]userauth.User{
		{tenantID: 1, userID: 101}: testUser(1, 101, "alice@example.test"),
	}}
	svc := NewService(NewMemoryStore(), users, StaticConfigSource(testConfig()), WithCeremonyEngine(engine), WithNow(func() time.Time { return now }))
	if _, err := svc.StoreCredential(ctx, CredentialRecord{
		TenantID: 1, UserID: 101, CredentialID: []byte("cred-a"), PublicKey: []byte("pk-a"), SignCount: 7,
	}); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	begin, err := svc.LoginBegin(ctx, LoginBeginInput{TenantID: 1})
	if err != nil {
		t.Fatalf("LoginBegin: %v", err)
	}
	_, err = svc.LoginFinish(ctx, LoginFinishInput{TenantID: 1, SessionID: begin.SessionID, CredentialJSON: []byte(`{"id":"cred-a"}`)})
	if !errors.Is(err, ErrCloneDetected) {
		t.Fatalf("LoginFinish err=%v want ErrCloneDetected", err)
	}
	stored, err := svc.store.GetCredentialByCredentialID(ctx, 1, []byte("cred-a"))
	if err != nil {
		t.Fatalf("GetCredentialByCredentialID: %v", err)
	}
	if !stored.CloneWarning {
		t.Fatal("clone_warning was not set after sign-count regression")
	}
	if stored.SignCount != 7 {
		t.Fatalf("sign_count=%d want unchanged 7 after rejected clone assertion", stored.SignCount)
	}
	if stored.LastUsedAt != nil {
		t.Fatalf("last_used_at=%v want nil for rejected clone assertion", stored.LastUsedAt)
	}
}

func TestPasskeyCrossUserIsolation(t *testing.T) {
	// Mutation killed: dropping user_id from DeleteCredential allows user A to
	// delete user B's credential; dropping credential-owner resolution logs in A.
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 10, 10, 0, 0, time.UTC)
	engine := &fakeEngine{loginCredentialID: []byte("cred-b"), assertedSignCount: 3}
	users := fakeUsers{rows: map[userKey]userauth.User{
		{tenantID: 1, userID: 101}: testUser(1, 101, "alice@example.test"),
		{tenantID: 1, userID: 202}: testUser(1, 202, "bob@example.test"),
	}}
	svc := NewService(NewMemoryStore(), users, StaticConfigSource(testConfig()), WithCeremonyEngine(engine), WithNow(func() time.Time { return now }))
	a, err := svc.StoreCredential(ctx, CredentialRecord{
		TenantID: 1, UserID: 101, CredentialID: []byte("cred-a"), PublicKey: []byte("pk-a"), SignCount: 1,
	})
	if err != nil {
		t.Fatalf("seed A credential: %v", err)
	}
	b, err := svc.StoreCredential(ctx, CredentialRecord{
		TenantID: 1, UserID: 202, CredentialID: []byte("cred-b"), PublicKey: []byte("pk-b"), SignCount: 2,
	})
	if err != nil {
		t.Fatalf("seed B credential: %v", err)
	}

	if err := svc.DeleteCredential(ctx, DeleteCredentialInput{TenantID: 1, UserID: 101, ID: b.ID}); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("user A deleting user B credential err=%v want ErrCredentialNotFound", err)
	}
	if _, err := svc.store.GetCredentialByID(ctx, 1, b.ID); err != nil {
		t.Fatalf("B credential was deleted by A: %v", err)
	}
	if err := svc.DeleteCredential(ctx, DeleteCredentialInput{TenantID: 1, UserID: 101, ID: a.ID}); err != nil {
		t.Fatalf("user A deleting own credential: %v", err)
	}

	begin, err := svc.LoginBegin(ctx, LoginBeginInput{TenantID: 1})
	if err != nil {
		t.Fatalf("LoginBegin: %v", err)
	}
	result, err := svc.LoginFinish(ctx, LoginFinishInput{TenantID: 1, SessionID: begin.SessionID, CredentialJSON: []byte(`{"id":"cred-b"}`)})
	if err != nil {
		t.Fatalf("LoginFinish with B credential: %v", err)
	}
	if result.User.ID != 202 {
		t.Fatalf("resolved user_id=%d want B user 202", result.User.ID)
	}
}

type fakeEngine struct {
	loginCredentialID []byte
	assertedSignCount uint32
	finishLoginCalls  int
}

func (e *fakeEngine) BeginRegistration(context.Context, Config, WebAuthnUser, []CredentialRecord) (CeremonyOptions, []byte, error) {
	return CeremonyOptions(`{"challenge":"register"}`), []byte(`{"challenge":"register"}`), nil
}

func (e *fakeEngine) FinishRegistration(context.Context, Config, WebAuthnUser, []byte, []byte) (VerifiedCredential, error) {
	return VerifiedCredential{
		CredentialID: []byte("registered-cred"), PublicKey: []byte("registered-pk"), SignCount: 1,
	}, nil
}

func (e *fakeEngine) BeginDiscoverableLogin(context.Context, Config) (CeremonyOptions, []byte, error) {
	return CeremonyOptions(`{"challenge":"login"}`), []byte(`{"challenge":"login"}`), nil
}

func (e *fakeEngine) FinishDiscoverableLogin(ctx context.Context, cfg Config, sessionData, credentialJSON []byte, resolve DiscoverableResolver) (DiscoverableLoginResult, error) {
	e.finishLoginCalls++
	resolved, err := resolve(ctx, e.loginCredentialID, nil)
	if err != nil {
		return DiscoverableLoginResult{}, err
	}
	return DiscoverableLoginResult{
		User: resolved.User,
		Credential: VerifiedCredential{
			CredentialID: append([]byte(nil), e.loginCredentialID...),
			PublicKey:    append([]byte(nil), resolved.MatchedCredential.PublicKey...),
			SignCount:    e.assertedSignCount,
		},
		MatchedCredential: resolved.MatchedCredential,
		AssertedSignCount: e.assertedSignCount,
	}, nil
}

type fakeUsers struct {
	rows map[userKey]userauth.User
}

func (s fakeUsers) GetUserByID(_ context.Context, tenantID, userID int64) (userauth.User, error) {
	user, ok := s.rows[userKey{tenantID: tenantID, userID: userID}]
	if !ok {
		return userauth.User{}, userauth.ErrUserNotFound
	}
	return user, nil
}

type userKey struct {
	tenantID int64
	userID   int64
}

func testConfig() Config {
	return Config{
		Enabled:             true,
		RegistrationEnabled: true,
		RPID:                "example.test",
		RPDisplayName:       "HUAKAI Test",
		RPOrigins:           []string{"https://example.test"},
		ChallengeTTL:        5 * time.Minute,
	}
}

func testUser(tenantID, userID int64, email string) userauth.User {
	return userauth.User{
		ID: userID, TenantID: tenantID, Email: email, DisplayName: email,
		EmailVerified: true, Status: userauth.UserStatusActive,
		CreatedAt: time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC),
	}
}

func TestPasskeyUserHandleIsTenantScopedAndStable(t *testing.T) {
	// Mutation killed: deriving the WebAuthn user handle from user_id only
	// makes same numeric IDs collide across tenants.
	a := WebAuthnUserHandle(1, 42)
	b := WebAuthnUserHandle(2, 42)
	c := WebAuthnUserHandle(1, 42)
	if bytes.Equal(a, b) {
		t.Fatalf("tenant-scoped user handles collided: %x", a)
	}
	if !bytes.Equal(a, c) {
		t.Fatalf("stable user handle changed: first=%x second=%x", a, c)
	}
}
