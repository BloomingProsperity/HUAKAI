package passkeyhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/passkey"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestPasskeyStepUpRequiredForRegisterDelete(t *testing.T) {
	// 杀掉的变异: 从 register begin/finish/delete 移除 VerifyStepUp,
	// 会让持有被盗 bearer session 的攻击者得以添加或删除 passkey。
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 11, 0, 0, 0, time.UTC)
	users := httpFakeUsers{rows: map[httpUserKey]userauth.User{
		{tenantID: 1, userID: 101}: httpTestUser(1, 101, "alice@example.test"),
	}}
	passkeys := passkey.NewService(
		passkey.NewMemoryStore(),
		users,
		passkey.StaticConfigSource(httpTestConfig()),
		passkey.WithCeremonyEngine(&httpFakeEngine{loginCredentialID: []byte("cred-a"), assertedSignCount: 2}),
		passkey.WithNow(func() time.Time { return now }),
	)
	seeded, err := passkeys.StoreCredential(ctx, passkey.CredentialRecord{
		TenantID: 1, UserID: 101, CredentialID: []byte("cred-a"), PublicKey: []byte("pk-a"), SignCount: 1,
	})
	if err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	stepUp := &httpFakeStepUp{err: ErrStepUpRequired}
	deps := Deps{Passkeys: passkeys, StepUp: stepUp}

	registerReq := authedJSONRequest(http.MethodPost, "/v1/me/passkeys/register/begin", `{"name":"MacBook"}`)
	rec := httptest.NewRecorder()
	newRegisterBeginHandler(deps).ServeHTTP(rec, registerReq)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "passkey_step_up_required" {
		t.Fatalf("register without step-up status=%d body=%s", rec.Code, rec.Body.String())
	}

	finishReq := authedJSONRequest(http.MethodPost, "/v1/me/passkeys/register/finish", `{"session_id":"ceremony-a","credential":{"id":"new-cred"}}`)
	rec = httptest.NewRecorder()
	newRegisterFinishHandler(deps).ServeHTTP(rec, finishReq)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "passkey_step_up_required" {
		t.Fatalf("register finish without step-up status=%d body=%s", rec.Code, rec.Body.String())
	}

	deleteReq := authedJSONRequest(http.MethodDelete, "/v1/me/passkeys/"+strconvID(seeded.ID), `{}`)
	deleteReq = withChiParam(deleteReq, "id", strconvID(seeded.ID))
	rec = httptest.NewRecorder()
	newDeleteHandler(deps).ServeHTTP(rec, deleteReq)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "passkey_step_up_required" {
		t.Fatalf("delete without step-up status=%d body=%s", rec.Code, rec.Body.String())
	}
	if stepUp.calls != 3 {
		t.Fatalf("stepUp calls=%d want 3", stepUp.calls)
	}
	items, err := passkeys.ListCredentials(ctx, 1, 101)
	if err != nil {
		t.Fatalf("credential list after refused delete: %v", err)
	}
	if len(items) != 1 || items[0].ID != seeded.ID {
		t.Fatalf("credential changed after refused delete: %+v", items)
	}
}

func TestPasskeyLoginMintsSessionLikePassword(t *testing.T) {
	// 杀掉的变异: 用另一套并行的 token 铸造器替换 usersession.Service.Create,
	// 产生的响应 token 是 Validate 无法使用的。
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 11, 10, 0, 0, time.UTC)
	user := httpTestUser(1, 101, "alice@example.test")
	users := httpFakeUsers{rows: map[httpUserKey]userauth.User{{tenantID: 1, userID: 101}: user}}
	engine := &httpFakeEngine{loginCredentialID: []byte("cred-a"), assertedSignCount: 2}
	passkeys := passkey.NewService(
		passkey.NewMemoryStore(),
		users,
		passkey.StaticConfigSource(httpTestConfig()),
		passkey.WithCeremonyEngine(engine),
		passkey.WithNow(func() time.Time { return now }),
	)
	if _, err := passkeys.StoreCredential(ctx, passkey.CredentialRecord{
		TenantID: 1, UserID: 101, CredentialID: []byte("cred-a"), PublicKey: []byte("pk-a"), SignCount: 1,
	}); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	begin, err := passkeys.LoginBegin(ctx, passkey.LoginBeginInput{TenantID: 1})
	if err != nil {
		t.Fatalf("LoginBegin: %v", err)
	}
	sessions := usersession.NewService(usersession.NewMemoryStore())
	sessions.SigningKey = []byte("0123456789abcdef0123456789abcdef")
	sessions.Now = func() time.Time { return now }
	deps := Deps{Passkeys: passkeys, Sessions: sessions, ClientIPResolver: &clientip.Resolver{}}

	body := `{"tenant_id":1,"session_id":"` + begin.SessionID + `","credential":{"id":"cred-a"},"device_info":{"label":"test"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/passkey/login/finish", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:443"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://example.test")
	req.Header.Set("User-Agent", "Chrome/1")
	rec := httptest.NewRecorder()
	newLoginFinishHandler(deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login finish status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		User    map[string]any           `json:"user"`
		Session usersession.IssuedTokens `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Session.SessionToken == "" || resp.Session.RefreshToken == "" {
		t.Fatalf("session tokens missing: %+v", resp.Session)
	}
	if _, err := sessions.Validate(ctx, resp.Session.SessionToken, "192.0.2.10", "Chrome/1"); err != nil {
		t.Fatalf("session minted by passkey login is not valid through usersession.Validate: %v", err)
	}
	if got := int64(resp.User["id"].(float64)); got != 101 {
		t.Fatalf("response user id=%d want 101", got)
	}
}

func TestPasskeyLoginFinishDisabledUserReturnsGeneric403(t *testing.T) {
	// 守护:passkey 登录被账号状态门拒时,handler 对外只回 generic account_not_active(403),不泄露
	// 具体状态(disabled/locked/reset),且绝不签发 session。变异检查:把 handler 里 ErrUserDisabled/
	// Locked/ResetRequired 的 403 映射改回 default(503 passkey_backend_error),本用例转红。
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 11, 12, 0, 0, time.UTC)
	user := httpTestUser(1, 101, "alice@example.test")
	user.Status = userauth.UserStatusDisabled
	users := httpFakeUsers{rows: map[httpUserKey]userauth.User{{tenantID: 1, userID: 101}: user}}
	engine := &httpFakeEngine{loginCredentialID: []byte("cred-a"), assertedSignCount: 2}
	passkeys := passkey.NewService(
		passkey.NewMemoryStore(), users, passkey.StaticConfigSource(httpTestConfig()),
		passkey.WithCeremonyEngine(engine), passkey.WithNow(func() time.Time { return now }),
	)
	if _, err := passkeys.StoreCredential(ctx, passkey.CredentialRecord{
		TenantID: 1, UserID: 101, CredentialID: []byte("cred-a"), PublicKey: []byte("pk-a"), SignCount: 1,
	}); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	begin, err := passkeys.LoginBegin(ctx, passkey.LoginBeginInput{TenantID: 1})
	if err != nil {
		t.Fatalf("LoginBegin: %v", err)
	}
	sessions := usersession.NewService(usersession.NewMemoryStore())
	sessions.SigningKey = []byte("0123456789abcdef0123456789abcdef")
	sessions.Now = func() time.Time { return now }
	deps := Deps{Passkeys: passkeys, Sessions: sessions, ClientIPResolver: &clientip.Resolver{}}

	body := `{"tenant_id":1,"session_id":"` + begin.SessionID + `","credential":{"id":"cred-a"},"device_info":{"label":"test"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/passkey/login/finish", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:443"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://example.test")
	rec := httptest.NewRecorder()
	newLoginFinishHandler(deps).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled passkey login status=%d body=%s want 403", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "account_not_active" {
		t.Fatalf("error code=%q want account_not_active", code)
	}
	if b := strings.ToLower(rec.Body.String()); strings.Contains(b, "disabled") || strings.Contains(b, "locked") || strings.Contains(b, "reset") {
		t.Fatalf("响应泄露了具体账号状态: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "session_token") {
		t.Fatalf("禁用用户 passkey 登录不应签发 session: %s", rec.Body.String())
	}
}

type httpFakeEngine struct {
	loginCredentialID []byte
	assertedSignCount uint32
}

func (e *httpFakeEngine) BeginRegistration(context.Context, passkey.Config, passkey.WebAuthnUser, []passkey.CredentialRecord) (passkey.CeremonyOptions, []byte, error) {
	return passkey.CeremonyOptions(`{"challenge":"register"}`), []byte(`{"challenge":"register"}`), nil
}

func (e *httpFakeEngine) FinishRegistration(context.Context, passkey.Config, passkey.WebAuthnUser, []byte, []byte) (passkey.VerifiedCredential, error) {
	return passkey.VerifiedCredential{CredentialID: []byte("new-cred"), PublicKey: []byte("new-pk"), SignCount: 1}, nil
}

func (e *httpFakeEngine) BeginDiscoverableLogin(context.Context, passkey.Config) (passkey.CeremonyOptions, []byte, error) {
	return passkey.CeremonyOptions(`{"challenge":"login"}`), []byte(`{"challenge":"login"}`), nil
}

func (e *httpFakeEngine) FinishDiscoverableLogin(ctx context.Context, cfg passkey.Config, sessionData, credentialJSON []byte, resolve passkey.DiscoverableResolver) (passkey.DiscoverableLoginResult, error) {
	resolved, err := resolve(ctx, e.loginCredentialID, nil)
	if err != nil {
		return passkey.DiscoverableLoginResult{}, err
	}
	return passkey.DiscoverableLoginResult{
		User: resolved.User,
		Credential: passkey.VerifiedCredential{
			CredentialID: append([]byte(nil), e.loginCredentialID...),
			PublicKey:    append([]byte(nil), resolved.MatchedCredential.PublicKey...),
			SignCount:    e.assertedSignCount,
		},
		MatchedCredential: resolved.MatchedCredential,
		AssertedSignCount: e.assertedSignCount,
	}, nil
}

type httpFakeStepUp struct {
	err   error
	calls int
}

func (s *httpFakeStepUp) VerifyStepUp(context.Context, int64, int64, StepUpProof) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	return nil
}

type httpFakeUsers struct {
	rows map[httpUserKey]userauth.User
}

func (s httpFakeUsers) GetUserByID(_ context.Context, tenantID, userID int64) (userauth.User, error) {
	user, ok := s.rows[httpUserKey{tenantID: tenantID, userID: userID}]
	if !ok {
		return userauth.User{}, userauth.ErrUserNotFound
	}
	return user, nil
}

type httpUserKey struct {
	tenantID int64
	userID   int64
}

func authedJSONRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://example.test")
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{
		TenantID: 1, UserID: 101, FamilyID: "family", TokenID: "token", Generation: 1,
	}))
	return req
}

func withChiParam(req *http.Request, key, value string) *http.Request {
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v body=%s", err, rec.Body.String())
	}
	return body.Error.Code
}

func httpTestConfig() passkey.Config {
	return passkey.Config{
		Enabled: true, RegistrationEnabled: true, RPID: "example.test",
		RPDisplayName: "HUAKAI Test", RPOrigins: []string{"https://example.test"},
		ChallengeTTL: 5 * time.Minute,
	}
}

func httpTestUser(tenantID, userID int64, email string) userauth.User {
	return userauth.User{
		ID: userID, TenantID: tenantID, Email: email, DisplayName: email,
		EmailVerified: true, Status: userauth.UserStatusActive,
		CreatedAt: time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC),
	}
}

func strconvID(id int64) string {
	return strconv.FormatInt(id, 10)
}

var _ StepUpVerifier = (*httpFakeStepUp)(nil)
var _ passkey.UserReader = (*httpFakeUsers)(nil)
