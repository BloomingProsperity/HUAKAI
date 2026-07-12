package controlhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// --- 桩 ----------------------------------------------------------------------

type selfAccountStub struct {
	changeCalls int
	gotTenantID int64
	gotUserID   int64
	gotOldPw    string
	gotNewPw    string
	changeErr   error
	deleteCalls int
	delTenantID int64
	delUserID   int64
	deleteErr   error
}

func (s *selfAccountStub) ChangeOwnPassword(_ context.Context, tenantID, userID int64, oldPw, newPw string) (userauth.User, error) {
	s.changeCalls++
	s.gotTenantID = tenantID
	s.gotUserID = userID
	s.gotOldPw = oldPw
	s.gotNewPw = newPw
	if s.changeErr != nil {
		return userauth.User{}, s.changeErr
	}
	return userauth.User{ID: userID, TenantID: tenantID}, nil
}

func (s *selfAccountStub) SoftDeleteSelf(_ context.Context, tenantID, userID int64) (userauth.User, error) {
	s.deleteCalls++
	s.delTenantID = tenantID
	s.delUserID = userID
	if s.deleteErr != nil {
		return userauth.User{}, s.deleteErr
	}
	return userauth.User{ID: userID, TenantID: tenantID}, nil
}

type revokeOthersStub struct {
	calls   int
	got     usersession.RevokeOthersInput
	revoked int64
	err     error
}

func (s *revokeOthersStub) RevokeOthers(_ context.Context, in usersession.RevokeOthersInput) (int64, error) {
	s.calls++
	s.got = in
	if s.err != nil {
		return 0, s.err
	}
	return s.revoked, nil
}

func serveSelfAccount(t *testing.T, deps AuthMeDeps, ident sessionauth.SessionIdentity, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serveAuthAction(t, deps, ident, method, target, body)
}

// --- 修改密码 ----------------------------------------------------------------

//  1. 改密-校旧密:service 返 ErrInvalidCredentials → 401 invalid_old_password,
//     且 RevokeOthers 必须 NOT 被调(旧密错绝不能撤会话)。
//     MUTATION: handler 跳过 VerifyPassword(在 service 桩里恒通过)→ 本测改用 wrong-old fixture
//     触发 ErrInvalidCredentials;若 handler 忽略该 error 继续 RevokeOthers → revoke.calls!=0 红。
func TestChangePasswordWrongOldRejected(t *testing.T) {
	self := &selfAccountStub{changeErr: userauth.ErrInvalidCredentials}
	revoker := &revokeOthersStub{revoked: 3}
	rec := serveSelfAccount(t, AuthMeDeps{SelfAccount: self, SessionsOthers: revoker},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "current-family"},
		http.MethodPost, "/me/password", `{"old_password":"wrong-old","new_password":"brand-new-secret"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}
	assertControlErrorCode(t, rec, "invalid_old_password")
	if self.gotOldPw != "wrong-old" {
		t.Fatalf("service oldPw=%q want wrong-old (透传校验)", self.gotOldPw)
	}
	if revoker.calls != 0 {
		t.Fatalf("RevokeOthers calls=%d want 0 on wrong old password; MUTATION: ignoring ErrInvalidCredentials revokes sessions", revoker.calls)
	}
}

//  2. 改密-撤其它留当前:断言 RevokeOthers 收到 CurrentFamilyID==session.FamilyID 且 Reason==password_change。
//     MUTATION: handler 误传空 FamilyID,或调 Revoke(全撤含当前)而非 RevokeOthers → got.CurrentFamilyID
//     不等于 current-family 或 Reason 不符 → 红(仿 logout 测试的判别式)。
func TestChangePasswordRevokesOthersKeepsCurrent(t *testing.T) {
	self := &selfAccountStub{}
	revoker := &revokeOthersStub{revoked: 2}
	rec := serveSelfAccount(t, AuthMeDeps{SelfAccount: self, SessionsOthers: revoker},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "current-family"},
		http.MethodPost, "/me/password", `{"old_password":"old-secret","new_password":"brand-new-secret"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if revoker.calls != 1 {
		t.Fatalf("RevokeOthers calls=%d want 1", revoker.calls)
	}
	got := revoker.got
	if got.TenantID != 7 || got.UserID != 42 || got.CurrentFamilyID != "current-family" || got.Reason != "password_change" {
		t.Fatalf("RevokeOthers input=%+v want tenant=7 user=42 current-family password_change; MUTATION: empty FamilyID or full Revoke keeps this red", got)
	}
	var body struct {
		Changed         bool  `json:"changed"`
		SessionsRevoked int64 `json:"sessions_revoked"`
	}
	decodeControlBody(t, rec, &body)
	if !body.Changed || body.SessionsRevoked != 2 {
		t.Fatalf("body changed=%v revoked=%d want true/2", body.Changed, body.SessionsRevoked)
	}
}

//  3. 改密-body 不污染身份:body 夹带 user_id/tenant_id/family_id → 断言 service + RevokeOthers
//     收到的全是 session 身份(42/7/current-family),非 body 里的 999/888/attacker。
//     MUTATION: handler 从 body 读任何身份字段 → service.gotUserID==999 或 got.CurrentFamilyID==attacker → 红。
func TestChangePasswordIgnoresBodyIdentity(t *testing.T) {
	self := &selfAccountStub{}
	revoker := &revokeOthersStub{revoked: 0}
	rec := serveSelfAccount(t, AuthMeDeps{SelfAccount: self, SessionsOthers: revoker},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "current-family"},
		http.MethodPost, "/me/password",
		`{"old_password":"old-secret","new_password":"brand-new-secret","user_id":999,"tenant_id":888,"family_id":"attacker-family"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if self.gotUserID != 42 || self.gotTenantID != 7 {
		t.Fatalf("service identity tenant=%d user=%d want 7/42 (body must be ignored)", self.gotTenantID, self.gotUserID)
	}
	if revoker.got.CurrentFamilyID != "current-family" || revoker.got.UserID != 42 {
		t.Fatalf("RevokeOthers identity=%+v want session current-family/42; MUTATION: reading body family_id/user_id leaks here", revoker.got)
	}
}

//  4. 改密-new_password 空 → 400 invalid_password,且 service NOT 被调(早返,省 argon2/DB)。
//     MUTATION: 删掉空校验 → service 被调(changeCalls==1) → 红。
func TestChangePasswordEmptyNewRejectedBeforeService(t *testing.T) {
	self := &selfAccountStub{}
	revoker := &revokeOthersStub{}
	rec := serveSelfAccount(t, AuthMeDeps{SelfAccount: self, SessionsOthers: revoker},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "current-family"},
		http.MethodPost, "/me/password", `{"old_password":"old-secret","new_password":""}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	assertControlErrorCode(t, rec, "invalid_password")
	if self.changeCalls != 0 {
		t.Fatalf("ChangeOwnPassword calls=%d want 0 on empty new_password; MUTATION: missing empty-check calls service", self.changeCalls)
	}
	if revoker.calls != 0 {
		t.Fatalf("RevokeOthers calls=%d want 0", revoker.calls)
	}
}

// --- 注销本账号 --------------------------------------------------------------

//  5. 删号-末位 admin 保护:service 返 ErrLastAdmin → 409 last_admin_protected,
//     且不撤任何 session(删失败不能误撤会话)。
//     MUTATION: handler 忽略 ErrLastAdmin 继续删/撤 → 状态码非 409 或 Revoke 被调 → 红。
func TestDeleteSelfLastAdminProtected(t *testing.T) {
	self := &selfAccountStub{deleteErr: userauth.ErrLastAdmin}
	revoker := &authSessionRevokerStub{revoked: 5}
	rec := serveSelfAccount(t, AuthMeDeps{SelfAccount: self, Sessions: revoker},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "current-family"},
		http.MethodDelete, "/me", "")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409 body=%s", rec.Code, rec.Body.String())
	}
	assertControlErrorCode(t, rec, "last_admin_protected")
	if revoker.calls != 0 {
		t.Fatalf("Revoke calls=%d want 0 when delete rejected; MUTATION: ignoring ErrLastAdmin revokes sessions", revoker.calls)
	}
}

//  6. 删号-撤全部 session:断言 Revoke 收到 UserID==42 且无 FamilyID(全撤路径)+ Reason==account_deleted。
//     MUTATION: handler 传了 FamilyID(只撤一个)或用别的 reason → got.FamilyID!="" 或 Reason 不符 → 红。
func TestDeleteSelfRevokesAllSessions(t *testing.T) {
	self := &selfAccountStub{}
	revoker := &authSessionRevokerStub{revoked: 4}
	rec := serveSelfAccount(t, AuthMeDeps{SelfAccount: self, Sessions: revoker},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "current-family"},
		http.MethodDelete, "/me", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if self.deleteCalls != 1 || self.delTenantID != 7 || self.delUserID != 42 {
		t.Fatalf("SoftDeleteSelf call mismatch: calls=%d tenant=%d user=%d", self.deleteCalls, self.delTenantID, self.delUserID)
	}
	got := revoker.got
	if got.TenantID != 7 || got.UserID != 42 || got.FamilyID != "" || got.Reason != "account_deleted" {
		t.Fatalf("Revoke input=%+v want tenant=7 user=42 empty-family account_deleted; MUTATION: passing FamilyID (single-family revoke) keeps this red", got)
	}
	var body struct {
		Deleted         bool  `json:"deleted"`
		SessionsRevoked int64 `json:"sessions_revoked"`
	}
	decodeControlBody(t, rec, &body)
	if !body.Deleted || body.SessionsRevoked != 4 {
		t.Fatalf("body deleted=%v revoked=%d want true/4", body.Deleted, body.SessionsRevoked)
	}
}

//  7. 删号-需 session:无 session ctx → 401,SoftDeleteSelf NOT 被调。
//     MUTATION: handler 不校 session(直接读 ident 零值并删)→ deleteCalls!=0 → 红。
func TestDeleteSelfRequiresSession(t *testing.T) {
	self := &selfAccountStub{}
	revoker := &authSessionRevokerStub{}
	router := chi.NewRouter()
	MountAuthMeRoutes(router, AuthMeDeps{SelfAccount: self, Sessions: revoker})
	req := httptest.NewRequest(http.MethodDelete, "/me", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}
	if self.deleteCalls != 0 {
		t.Fatalf("SoftDeleteSelf calls=%d want 0 without session", self.deleteCalls)
	}
	if revoker.calls != 0 {
		t.Fatalf("Revoke calls=%d want 0 without session", revoker.calls)
	}
}
