package gatewayhttp

import (
	"bytes"
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
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestAuthAndSessionHandlersRegisterVerifyLoginRefreshList(t *testing.T) {
	now := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	authSvc.Now = func() time.Time { return now }
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	email := &captureAuthEmail{}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, Sessions: sessionSvc, EmailSender: email})
	})
	r.Route("/v1/sessions", func(r chi.Router) {
		r.Use(sessionauth.SessionMiddleware(sessionSvc))
		MountSessionRoutes(r, SessionHandlerDeps{Sessions: sessionSvc})
	})

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/register", map[string]any{
		"tenant_id": 1, "email": "user@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusCreated)
	if email.verification == "" {
		t.Fatal("verification token was not sent")
	}

	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]any{
		"tenant_id": 1, "token": email.verification,
	})
	assertHTTPStatus(t, rec, http.StatusOK)

	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "user@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusOK)
	var loginResp struct {
		Session usersession.IssuedTokens `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.Session.RefreshToken == "" {
		t.Fatal("login did not return refresh token")
	}

	now = now.Add(time.Minute)
	rec = serveJSON(t, r, http.MethodPost, "/v1/sessions/refresh", map[string]any{
		"refresh_token": loginResp.Session.RefreshToken,
	}, loginResp.Session.SessionToken)
	assertHTTPStatus(t, rec, http.StatusOK)
	var refreshResp struct {
		Session usersession.IssuedTokens `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refreshResp); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}

	rec = serveJSON(t, r, http.MethodPost, "/v1/sessions/list", map[string]any{
		"tenant_id": 999, "user_id": int64(999999),
	}, refreshResp.Session.SessionToken)
	assertHTTPStatus(t, rec, http.StatusOK)
}

func TestAT_SESSION_001_004_HandlersRequireBearerAndIgnoreBodyUser(t *testing.T) {
	now := time.Date(2026, 5, 16, 14, 0, 0, 0, time.UTC)
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	issued, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 7, UserID: 7001, IP: "10.1.1.1", UserAgent: "Chrome/1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	other, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 7, UserID: 7002, IP: "10.2.1.1", UserAgent: "Firefox/1",
	})
	if err != nil {
		t.Fatalf("Create other: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/v1/sessions", func(r chi.Router) {
		r.Use(sessionauth.SessionMiddleware(sessionSvc))
		MountSessionRoutes(r, SessionHandlerDeps{Sessions: sessionSvc})
	})

	rec := serveJSON(t, r, http.MethodPost, "/v1/sessions/list", map[string]any{"tenant_id": 7, "user_id": 7001})
	assertHTTPStatus(t, rec, http.StatusUnauthorized)

	rec = serveJSON(t, r, http.MethodPost, "/v1/sessions/list", map[string]any{"tenant_id": 999, "user_id": 7002}, issued.SessionToken)
	assertHTTPStatus(t, rec, http.StatusOK)
	var listResp struct {
		Families []usersession.SessionFamily `json:"families"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Families) != 1 || listResp.Families[0].UserID != 7001 {
		t.Fatalf("handler trusted body user instead of bearer context: %+v", listResp.Families)
	}

	rec = serveJSON(t, r, http.MethodPost, "/v1/sessions/revoke", map[string]any{"tenant_id": 7, "user_id": 7002}, issued.SessionToken)
	assertHTTPStatus(t, rec, http.StatusOK)
	if _, err := sessionSvc.Validate(context.Background(), other.SessionToken, "10.2.1.1", "Firefox/1"); err != nil {
		t.Fatalf("body user revoke should not revoke another user: %v", err)
	}
	if _, err := sessionSvc.Validate(context.Background(), issued.SessionToken, "10.1.1.1", "Chrome/1"); err == nil {
		t.Fatal("bearer user's own sessions should be revoked")
	}
}

func TestAT_AUTH_007_011_CrossUserRefreshRejected(t *testing.T) {
	now := time.Date(2026, 5, 16, 15, 0, 0, 0, time.UTC)
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	caller, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 7, UserID: 7001, IP: "192.0.2.1",
	})
	if err != nil {
		t.Fatalf("Create caller: %v", err)
	}
	target, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 7, UserID: 7002, IP: "192.0.2.1",
	})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/v1/sessions", func(r chi.Router) {
		r.Use(sessionauth.SessionMiddleware(sessionSvc))
		MountSessionRoutes(r, SessionHandlerDeps{Sessions: sessionSvc})
	})

	rec := serveJSON(t, r, http.MethodPost, "/v1/sessions/refresh", map[string]any{
		"refresh_token": target.RefreshToken,
	}, caller.SessionToken)
	assertHTTPStatus(t, rec, http.StatusUnauthorized)
	if strings.Contains(rec.Body.String(), target.RefreshToken) || strings.Contains(rec.Body.String(), caller.SessionToken) {
		t.Fatalf("response leaked token material: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "refresh_token_cross_user_attempt") {
		t.Fatalf("response did not expose safe audit code: %s", rec.Body.String())
	}

	rec = serveJSON(t, r, http.MethodPost, "/v1/sessions/refresh", map[string]any{
		"refresh_token": target.RefreshToken,
	}, target.SessionToken)
	assertHTTPStatus(t, rec, http.StatusUnauthorized)
	families, err := sessionSvc.List(context.Background(), 7, 7002)
	if err != nil {
		t.Fatalf("List target families: %v", err)
	}
	if len(families) != 1 || families[0].Status != usersession.FamilyStatusRevoked ||
		families[0].RevokedReason != "refresh_token_cross_user_attempt" {
		t.Fatalf("target family was not revoked after cross-user refresh attempt: %+v", families)
	}
}

type captureAuthEmail struct {
	verification string
	reset        string
}

func (c *captureAuthEmail) SendVerification(_ context.Context, _ userauth.User, token string) error {
	c.verification = token
	return nil
}

func (c *captureAuthEmail) SendPasswordReset(_ context.Context, _ userauth.User, token string) error {
	c.reset = token
	return nil
}

func serveJSON(t *testing.T, h http.Handler, method, target string, body any, bearer ...string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if len(bearer) > 0 && strings.TrimSpace(bearer[0]) != "" {
		req.Header.Set("Authorization", "Bearer "+bearer[0])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertHTTPStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d want %d body=%s", rec.Code, want, rec.Body.String())
	}
}

func newGatewayMemoryAuthStore(now time.Time) *gatewayMemoryAuthStore {
	return &gatewayMemoryAuthStore{
		now:         now,
		nextID:      1,
		users:       map[int64]userauth.User{},
		byEmail:     map[string]int64{},
		emailTokens: map[string]userauth.TokenChallenge{},
		resetTokens: map[string]gatewayResetChallenge{},
		invites:     map[string]userauth.InviteCode{},
		oauthFlows:  map[string]userauth.OAuthFlowSession{},
		socialLinks: map[string]int64{},
	}
}

type gatewayMemoryAuthStore struct {
	now         time.Time
	nextID      int64
	users       map[int64]userauth.User
	byEmail     map[string]int64
	emailTokens map[string]userauth.TokenChallenge
	resetTokens map[string]gatewayResetChallenge
	invites     map[string]userauth.InviteCode
	oauthFlows  map[string]userauth.OAuthFlowSession
	socialLinks map[string]int64
}

type gatewayResetChallenge struct {
	userauth.TokenChallenge
	PasswordVersion int
	Consumed        bool
}

func (s *gatewayMemoryAuthStore) CreateUser(_ context.Context, in userauth.CreateUserParams) (userauth.User, error) {
	email := userauth.NormalizeEmail(in.Email)
	user := userauth.User{
		ID: in.TenantID*1000 + s.nextID, TenantID: in.TenantID, Email: email, DisplayName: in.DisplayName,
		PasswordHash: in.PasswordHash, EmailVerified: in.EmailVerified, InviteCodeUsed: in.InviteCodeUsed,
		SocialLoginProvider: in.SocialLoginProvider, Status: in.Status, PasswordVersion: 1,
		CreatedAt: s.now, UpdatedAt: s.now,
	}
	if user.Status == "" {
		user.Status = userauth.UserStatusPendingVerification
	}
	s.nextID++
	s.users[user.ID] = user
	s.byEmail[email] = user.ID
	return user, nil
}

func (s *gatewayMemoryAuthStore) GetUserByEmail(_ context.Context, _ int64, email string) (userauth.User, error) {
	id, ok := s.byEmail[userauth.NormalizeEmail(email)]
	if !ok {
		return userauth.User{}, userauth.ErrUserNotFound
	}
	return s.users[id], nil
}

func (s *gatewayMemoryAuthStore) GetUserByID(_ context.Context, tenantID, userID int64) (userauth.User, error) {
	user, ok := s.users[userID]
	if !ok || user.TenantID != tenantID {
		return userauth.User{}, userauth.ErrUserNotFound
	}
	return user, nil
}

func (s *gatewayMemoryAuthStore) MarkLoginSuccess(_ context.Context, tenantID, userID int64) error {
	user := s.users[userID]
	if user.TenantID == tenantID {
		user.FailedLoginCount = 0
		s.users[userID] = user
	}
	return nil
}
func (s *gatewayMemoryAuthStore) MarkLoginFailure(_ context.Context, tenantID, userID int64, threshold int) error {
	user := s.users[userID]
	if user.TenantID == tenantID {
		user.FailedLoginCount++
		if user.FailedLoginCount >= threshold {
			user.Status = userauth.UserStatusLocked
		}
		s.users[userID] = user
	}
	return nil
}

func (s *gatewayMemoryAuthStore) GetUserBySocialIdentity(_ context.Context, tenantID int64, provider, subject string) (userauth.User, error) {
	id, ok := s.socialLinks[authTestKey(tenantID, userauth.NormalizeEmail(provider+":"+subject))]
	if !ok {
		return userauth.User{}, userauth.ErrUserNotFound
	}
	return s.users[id], nil
}

func (s *gatewayMemoryAuthStore) LinkSocialIdentity(_ context.Context, tenantID, userID int64, provider, subject string) (userauth.User, error) {
	user := s.users[userID]
	if user.TenantID != tenantID {
		return userauth.User{}, userauth.ErrUserNotFound
	}
	user.SocialLoginProvider = provider
	user.EmailVerified = true
	user.Status = userauth.UserStatusActive
	s.users[user.ID] = user
	s.socialLinks[authTestKey(tenantID, userauth.NormalizeEmail(provider+":"+subject))] = user.ID
	return user, nil
}

func (s *gatewayMemoryAuthStore) CreateEmailVerificationToken(_ context.Context, challenge userauth.TokenChallenge) error {
	s.emailTokens[string(challenge.TokenHash)] = challenge
	return nil
}

func (s *gatewayMemoryAuthStore) ConsumeEmailVerificationToken(_ context.Context, tenantID int64, tokenHash []byte, now time.Time) (userauth.User, error) {
	challenge, ok := s.emailTokens[string(tokenHash)]
	if !ok || challenge.TenantID != tenantID || !challenge.ExpiresAt.After(now) {
		return userauth.User{}, userauth.ErrTokenInvalid
	}
	delete(s.emailTokens, string(tokenHash))
	user := s.users[challenge.UserID]
	user.EmailVerified = true
	user.Status = userauth.UserStatusActive
	user.UpdatedAt = now
	s.users[user.ID] = user
	return user, nil
}

func (s *gatewayMemoryAuthStore) CreatePasswordResetToken(_ context.Context, challenge userauth.TokenChallenge, passwordVersion int) error {
	s.resetTokens[string(challenge.TokenHash)] = gatewayResetChallenge{TokenChallenge: challenge, PasswordVersion: passwordVersion}
	return nil
}

func (s *gatewayMemoryAuthStore) ConsumePasswordResetToken(_ context.Context, tenantID int64, tokenHash []byte, passwordHash string, now time.Time) (userauth.User, error) {
	challenge, ok := s.resetTokens[string(tokenHash)]
	if !ok || challenge.Consumed || challenge.TenantID != tenantID || !challenge.ExpiresAt.After(now) {
		return userauth.User{}, userauth.ErrTokenInvalid
	}
	user := s.users[challenge.UserID]
	user.PasswordHash = passwordHash
	user.PasswordVersion++
	user.UpdatedAt = now
	challenge.Consumed = true
	s.resetTokens[string(tokenHash)] = challenge
	s.users[user.ID] = user
	return user, nil
}

func (s *gatewayMemoryAuthStore) RedeemInvite(_ context.Context, tenantID int64, rawCode string, now time.Time) (userauth.InviteCode, error) {
	hash := userauth.HashInviteCode(rawCode)
	invite, ok := s.invites[hash]
	if !ok || invite.TenantID != tenantID || invite.Status != "active" || invite.UsedCount >= invite.MaxUses {
		return userauth.InviteCode{}, userauth.ErrInviteInvalid
	}
	if invite.ValidUntil != nil && !invite.ValidUntil.After(now) {
		return userauth.InviteCode{}, userauth.ErrInviteInvalid
	}
	invite.UsedCount++
	s.invites[hash] = invite
	return invite, nil
}

func (s *gatewayMemoryAuthStore) CreateInviteBinding(context.Context, int64, int64, string, time.Time) error {
	return nil
}

func (s *gatewayMemoryAuthStore) CreateOAuthFlowSession(_ context.Context, challenge userauth.OAuthFlowChallenge) error {
	s.oauthFlows[string(challenge.StateHash)] = userauth.OAuthFlowSession{
		ID: challenge.ID, TenantID: challenge.TenantID, Provider: challenge.Provider,
		StateHash: challenge.StateHash, NonceHash: challenge.NonceHash, PKCEVerifier: challenge.PKCEVerifier,
		RedirectURI: challenge.RedirectURI, ExpiresAt: challenge.ExpiresAt, CreatedAt: s.now,
	}
	return nil
}

func (s *gatewayMemoryAuthStore) ConsumeOAuthFlowSession(_ context.Context, tenantID int64, provider string, stateHash []byte, now time.Time) (userauth.OAuthFlowSession, error) {
	flow, ok := s.oauthFlows[string(stateHash)]
	if !ok || flow.TenantID != tenantID || flow.Provider != provider {
		return userauth.OAuthFlowSession{}, userauth.ErrOAuthFlowNotFound
	}
	if flow.ConsumedAt != nil || !flow.ExpiresAt.After(now) {
		return userauth.OAuthFlowSession{}, userauth.ErrOAuthFlowExpired
	}
	t := now.UTC()
	flow.ConsumedAt = &t
	s.oauthFlows[string(stateHash)] = flow
	return flow, nil
}

func authTestKey(tenantID int64, value string) string {
	return strconv.FormatInt(tenantID, 10) + ":" + strings.TrimSpace(value)
}

func testSessionSigningKey() []byte {
	return []byte("0123456789abcdef0123456789abcdef")
}
