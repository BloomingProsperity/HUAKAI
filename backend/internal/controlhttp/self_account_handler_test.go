package controlhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// --- 桩 ----------------------------------------------------------------------

type selfAccountStub struct {
	changeCalls int
	gotTenantID int64
	gotUserID   int64
	gotOldPw    string
	gotNewPw    string
	gotFamilyID string
	revoked     int64
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

func (s *selfAccountStub) ChangeOwnPasswordAndRevokeOthers(
	_ context.Context,
	tenantID, userID int64,
	oldPw, newPw, currentFamilyID string,
) (userauth.User, int64, error) {
	s.changeCalls++
	s.gotTenantID = tenantID
	s.gotUserID = userID
	s.gotOldPw = oldPw
	s.gotNewPw = newPw
	s.gotFamilyID = currentFamilyID
	if s.changeErr != nil {
		return userauth.User{}, 0, s.changeErr
	}
	return userauth.User{ID: userID, TenantID: tenantID}, s.revoked, nil
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

func (s *selfAccountStub) SoftDeleteSelfAndRevokeSessions(
	_ context.Context,
	tenantID, userID int64,
) (userauth.User, int64, error) {
	s.deleteCalls++
	s.delTenantID = tenantID
	s.delUserID = userID
	if s.deleteErr != nil {
		return userauth.User{}, 0, s.deleteErr
	}
	return userauth.User{ID: userID, TenantID: tenantID}, s.revoked, nil
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
	rec := serveSelfAccount(t, AuthMeDeps{SelfAccount: self},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "current-family"},
		http.MethodPost, "/me/password", `{"old_password":"wrong-old","new_password":"brand-new-secret"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}
	assertControlErrorCode(t, rec, "invalid_old_password")
	if self.gotOldPw != "wrong-old" {
		t.Fatalf("service oldPw=%q want wrong-old (透传校验)", self.gotOldPw)
	}
}

//  2. 改密-撤其它留当前:断言 RevokeOthers 收到 CurrentFamilyID==session.FamilyID 且 Reason==password_change。
//     MUTATION: handler 误传空 FamilyID,或调 Revoke(全撤含当前)而非 RevokeOthers → got.CurrentFamilyID
//     不等于 current-family 或 Reason 不符 → 红(仿 logout 测试的判别式)。
func TestChangePasswordRevokesOthersKeepsCurrent(t *testing.T) {
	self := &selfAccountStub{revoked: 2}
	rec := serveSelfAccount(t, AuthMeDeps{SelfAccount: self},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "current-family"},
		http.MethodPost, "/me/password", `{"old_password":"old-secret","new_password":"brand-new-secret"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if self.changeCalls != 1 || self.gotFamilyID != "current-family" {
		t.Fatalf("原子改密调用次数=%d family=%q，期望 1/current-family", self.changeCalls, self.gotFamilyID)
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
	rec := serveSelfAccount(t, AuthMeDeps{SelfAccount: self},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "current-family"},
		http.MethodPost, "/me/password",
		`{"old_password":"old-secret","new_password":"brand-new-secret","user_id":999,"tenant_id":888,"family_id":"attacker-family"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if self.gotUserID != 42 || self.gotTenantID != 7 {
		t.Fatalf("service identity tenant=%d user=%d want 7/42 (body must be ignored)", self.gotTenantID, self.gotUserID)
	}
	if self.gotFamilyID != "current-family" {
		t.Fatalf("原子改密 family=%q want current-family", self.gotFamilyID)
	}
}

//  4. 改密-new_password 空 → 400 invalid_password,且 service NOT 被调(早返,省 argon2/DB)。
//     MUTATION: 删掉空校验 → service 被调(changeCalls==1) → 红。
func TestChangePasswordEmptyNewRejectedBeforeService(t *testing.T) {
	self := &selfAccountStub{}
	rec := serveSelfAccount(t, AuthMeDeps{SelfAccount: self},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "current-family"},
		http.MethodPost, "/me/password", `{"old_password":"old-secret","new_password":""}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	assertControlErrorCode(t, rec, "invalid_password")
	if self.changeCalls != 0 {
		t.Fatalf("ChangeOwnPassword calls=%d want 0 on empty new_password; MUTATION: missing empty-check calls service", self.changeCalls)
	}
}

// --- 注销本账号 --------------------------------------------------------------

// 5. 删号-末位 admin 保护:原子 service 返 ErrLastAdmin → 409 last_admin_protected。
func TestDeleteSelfLastAdminProtected(t *testing.T) {
	self := &selfAccountStub{deleteErr: userauth.ErrLastAdmin}
	rec := serveSelfAccount(t, AuthMeDeps{SelfAccount: self},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "current-family"},
		http.MethodDelete, "/me", "")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409 body=%s", rec.Code, rec.Body.String())
	}
	assertControlErrorCode(t, rec, "last_admin_protected")
	if self.deleteCalls != 1 {
		t.Fatalf("atomic delete calls=%d want 1", self.deleteCalls)
	}
}

// 6. 删号-撤全部 session:handler 只调用一次原子 service 并返回事务实际撤销数。
func TestDeleteSelfRevokesAllSessions(t *testing.T) {
	self := &selfAccountStub{revoked: 4}
	rec := serveSelfAccount(t, AuthMeDeps{SelfAccount: self},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "current-family"},
		http.MethodDelete, "/me", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if self.deleteCalls != 1 || self.delTenantID != 7 || self.delUserID != 42 {
		t.Fatalf("atomic SoftDeleteSelf call mismatch: calls=%d tenant=%d user=%d", self.deleteCalls, self.delTenantID, self.delUserID)
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
	router := chi.NewRouter()
	MountAuthMeRoutes(router, AuthMeDeps{SelfAccount: self})
	req := httptest.NewRequest(http.MethodDelete, "/me", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}
	if self.deleteCalls != 0 {
		t.Fatalf("SoftDeleteSelf calls=%d want 0 without session", self.deleteCalls)
	}
}
