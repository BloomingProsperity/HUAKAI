package controlhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestAuthSocialUnlinkSelfScopesToSession(t *testing.T) {
	svc := &authSocialLinkStub{unlinked: true}
	rec := serveAuthAction(t, AuthMeDeps{SocialLinks: svc}, sessionauth.SessionIdentity{
		TenantID: 7, UserID: 42, FamilyID: "family-1",
	}, http.MethodDelete, "/account-bindings/google", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if svc.calls != 1 || svc.gotTenantID != 7 || svc.gotUserID != 42 || svc.gotProvider != userauth.SocialProviderGoogle {
		t.Fatalf("unlink call mismatch: calls=%d tenant=%d user=%d provider=%q", svc.calls, svc.gotTenantID, svc.gotUserID, svc.gotProvider)
	}
	var body struct {
		Unlinked bool `json:"unlinked"`
	}
	decodeControlBody(t, rec, &body)
	if !body.Unlinked {
		t.Fatalf("unlinked=%v want true", body.Unlinked)
	}
}

func TestAuthSocialUnlinkRejectsLastLoginMethod(t *testing.T) {
	svc := &authSocialLinkStub{err: userauth.ErrLastLoginMethod}
	rec := serveAuthAction(t, AuthMeDeps{SocialLinks: svc}, sessionauth.SessionIdentity{
		TenantID: 7, UserID: 42, FamilyID: "family-1",
	}, http.MethodDelete, "/account-bindings/google", "")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409 body=%s", rec.Code, rec.Body.String())
	}
	assertControlErrorCode(t, rec, "last_login_method")
}

func TestAuthLogoutRevokesCurrentFamilyOnly(t *testing.T) {
	revoker := &authSessionRevokerStub{revoked: 1}
	rec := serveAuthAction(t, AuthMeDeps{Sessions: revoker}, sessionauth.SessionIdentity{
		TenantID: 7, UserID: 42, FamilyID: "current-family",
	}, http.MethodPost, "/logout", `{"family_id":"attacker-family"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if revoker.calls != 1 {
		t.Fatalf("revoke calls=%d want 1", revoker.calls)
	}
	got := revoker.got
	if got.TenantID != 7 || got.UserID != 42 || got.FamilyID != "current-family" || got.Reason != "logout" {
		t.Fatalf("revoke input=%+v want session tenant/user/current-family/logout; MUTATION: reading family_id from body or skipping Revoke keeps this red", got)
	}
	var body struct {
		Revoked int64 `json:"revoked"`
	}
	decodeControlBody(t, rec, &body)
	if body.Revoked != 1 {
		t.Fatalf("revoked=%d want 1", body.Revoked)
	}
}

func TestAuthLogoutRequiresSession(t *testing.T) {
	revoker := &authSessionRevokerStub{revoked: 1}
	router := chi.NewRouter()
	MountAuthMeRoutes(router, AuthMeDeps{Sessions: revoker})
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}
	if revoker.calls != 0 {
		t.Fatalf("revoke calls=%d want 0 without session", revoker.calls)
	}
}

type authSocialLinkStub struct {
	calls       int
	gotTenantID int64
	gotUserID   int64
	gotProvider string
	unlinked    bool
	err         error
}

func (s *authSocialLinkStub) UnlinkSocialIdentity(_ context.Context, tenantID, userID int64, provider string) (bool, error) {
	s.calls++
	s.gotTenantID = tenantID
	s.gotUserID = userID
	s.gotProvider = provider
	if s.err != nil {
		return false, s.err
	}
	return s.unlinked, nil
}

type authSessionRevokerStub struct {
	calls   int
	got     usersession.RevokeInput
	revoked int64
	err     error
}

func (s *authSessionRevokerStub) Revoke(_ context.Context, in usersession.RevokeInput) (int64, error) {
	s.calls++
	s.got = in
	if s.err != nil {
		return 0, s.err
	}
	return s.revoked, nil
}

func serveAuthAction(t *testing.T, deps AuthMeDeps, ident sessionauth.SessionIdentity, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	MountAuthMeRoutes(router, deps)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), ident))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeControlBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body: %v body=%s", err, strings.TrimSpace(rec.Body.String()))
	}
}

func assertControlErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeControlBody(t, rec, &body)
	if body.Error.Code != want {
		t.Fatalf("error code=%q want %q body=%s", body.Error.Code, want, rec.Body.String())
	}
}
