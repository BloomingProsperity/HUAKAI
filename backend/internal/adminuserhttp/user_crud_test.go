package adminuserhttp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type userCreateStub struct {
	calls    int
	in       userCreateInput
	gotAudit unlockAuditInput
	out      userCreated
	err      error
}

func (s *userCreateStub) CreateUserWithAudit(_ context.Context, in userCreateInput, audit unlockAuditInput) (userCreated, error) {
	s.calls++
	s.in = in
	s.gotAudit = audit
	if s.err != nil {
		return userCreated{}, s.err
	}
	out := s.out
	if out.ID == 0 {
		out = userCreated{ID: 555, Email: in.Email, Role: in.Role, Status: "active", CreatedAt: "2026-06-11T00:00:00Z"}
	}
	return out, nil
}

type userSoftDeleteStub struct {
	calls     int
	gotTenant int64
	gotUser   int64
	affected  int64
	err       error
}

func (s *userSoftDeleteStub) SoftDeleteForTenant(_ context.Context, tenantID, userID int64) (int64, error) {
	s.calls++
	s.gotTenant = tenantID
	s.gotUser = userID
	if s.err != nil {
		return 0, s.err
	}
	return s.affected, nil
}

type sessionRevokerStub struct {
	calls int
	in    usersession.RevokeInput
	err   error
}

func (s *sessionRevokerStub) Revoke(_ context.Context, in usersession.RevokeInput) (int64, error) {
	s.calls++
	s.in = in
	if s.err != nil {
		return 0, s.err
	}
	return 1, nil
}

func createDeps(creator *userCreateStub, audit *adminAuditStub) Deps {
	return Deps{
		Auth:        usersAuthStub{ident: tenantOperator(7)},
		Store:       &usersStoreStub{},
		UserCreator: creator,
		Audit:       audit,
	}
}

// TestCreateUser_HappyPath:合法创建会持久化一个 role=user 账号,绝不存储
// 明文口令(CMB-5),并把 create_user 审计与建用户交给 store 的同一事务
// (审计记录的真实落库由 user_crud_integration_test 的真 PG 用例断言)。
func TestCreateUser_HappyPath(t *testing.T) {
	creator := &userCreateStub{}
	audit := &adminAuditStub{}
	rec := invokeAdminUsersBody(t, createDeps(creator, audit), http.MethodPost, "/admin/v1/users",
		`{"email":"new@x.test","password":"longenough1"}`)
	assertStatus(t, rec, http.StatusCreated)
	if creator.calls != 1 {
		t.Fatalf("creator calls=%d want 1", creator.calls)
	}
	if creator.in.Role != "user" {
		t.Fatalf("created role=%q want user", creator.in.Role)
	}
	// CMB-5:store 必须收到 argon2id 散列,绝不能收到明文。
	if creator.in.PasswordHash == "longenough1" || !strings.HasPrefix(creator.in.PasswordHash, "$argon2") {
		t.Fatalf("password not hashed: %q", creator.in.PasswordHash)
	}
	// 审计已随建用户进同事务:handler 必须把审计 actor(操作者身份)透传给 store。
	if creator.gotAudit.ActorRole == "" || creator.gotAudit.ActorID == "" {
		t.Fatalf("create audit actor not passed to store: %+v", creator.gotAudit)
	}
}

// TestCreateUser_RejectsAdminRole 是越权提权护栏:此端点绝不能创建 admin。
// 变异:去掉 setUserCreateRequest 中的 role!="user" 校验 → role=admin 得到
// 201 + creator 被调用 → 红。
func TestCreateUser_RejectsAdminRole(t *testing.T) {
	creator := &userCreateStub{}
	rec := invokeAdminUsersBody(t, createDeps(creator, &adminAuditStub{}), http.MethodPost, "/admin/v1/users",
		`{"email":"esc@x.test","password":"longenough1","role":"admin"}`)
	assertStatus(t, rec, http.StatusForbidden)
	if !strings.Contains(rec.Body.String(), "admin_role_forbidden") {
		t.Fatalf("body=%s want admin_role_forbidden", rec.Body.String())
	}
	if creator.calls != 0 {
		t.Fatalf("creator must NOT be called on forbidden role; calls=%d", creator.calls)
	}
}

// TestCreateUser_WeakPasswordRejectedBeforeStore:口令长度 < 下限 → 400,不触达 store。
func TestCreateUser_WeakPassword(t *testing.T) {
	creator := &userCreateStub{}
	rec := invokeAdminUsersBody(t, createDeps(creator, &adminAuditStub{}), http.MethodPost, "/admin/v1/users",
		`{"email":"w@x.test","password":"short"}`)
	assertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "weak_password") || creator.calls != 0 {
		t.Fatalf("body=%s creatorCalls=%d want weak_password/0", rec.Body.String(), creator.calls)
	}
}

// TestCreateUser_DuplicateMaps409:store 返回 ErrUserAlreadyExists → 409。
func TestCreateUser_Duplicate(t *testing.T) {
	creator := &userCreateStub{err: ErrUserAlreadyExists}
	rec := invokeAdminUsersBody(t, createDeps(creator, &adminAuditStub{}), http.MethodPost, "/admin/v1/users",
		`{"email":"dup@x.test","password":"longenough1"}`)
	assertStatus(t, rec, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), "admin_user_exists") {
		t.Fatalf("body=%s want admin_user_exists", rec.Body.String())
	}
}

func deleteDeps(getRow admindb.AdminGetUserForTenantRow, getErr error, del *userSoftDeleteStub, rev *sessionRevokerStub, audit *adminAuditStub) Deps {
	return Deps{
		Auth:            usersAuthStub{ident: tenantOperator(7)},
		Store:           &usersStoreStub{getRow: getRow, getErr: getErr},
		UserSoftDeleter: del,
		SessionRevoker:  rev,
		Audit:           audit,
	}
}

// TestDeleteUser_SoftDeletesAndRevokesSessions:删除一个 role=user 账号会
// 软删它,并撤销其会话(关闭删除后的访问窗口),并写入一条 delete_user 审计。
// 变异:去掉 SessionRevoker 调用 → revoker.calls==0 → 红。
func TestDeleteUser_SoftDeletesAndRevokesSessions(t *testing.T) {
	del := &userSoftDeleteStub{affected: 1}
	rev := &sessionRevokerStub{}
	audit := &adminAuditStub{}
	deps := deleteDeps(admindb.AdminGetUserForTenantRow{ID: 101, Role: "user", Status: "active"}, nil, del, rev, audit)
	rec := invokeAdminUsersBody(t, deps, http.MethodDelete, "/admin/v1/users/101", "")
	assertStatus(t, rec, http.StatusOK)
	if del.calls != 1 || del.gotUser != 101 || del.gotTenant != 7 {
		t.Fatalf("softdelete calls=%d user=%d tenant=%d want 1/101/7", del.calls, del.gotUser, del.gotTenant)
	}
	if rev.calls != 1 || rev.in.UserID != 101 {
		t.Fatalf("session revoke calls=%d user=%d want 1/101 (deleted user's sessions must be revoked)", rev.calls, rev.in.UserID)
	}
	if audit.calls != 1 || audit.arg.Action != "delete_user" {
		t.Fatalf("audit calls=%d action=%q want 1/delete_user", audit.calls, audit.arg.Action)
	}
}

// TestDeleteUser_RejectsAdminTarget 是防止经此端点删除 admin 的护栏。
// 变异:去掉 before.Role=="admin" 校验 → 软删在 admin 上执行 → 红。
func TestDeleteUser_RejectsAdminTarget(t *testing.T) {
	del := &userSoftDeleteStub{affected: 1}
	deps := deleteDeps(admindb.AdminGetUserForTenantRow{ID: 9, Role: "admin", Status: "active"}, nil, del, &sessionRevokerStub{}, &adminAuditStub{})
	rec := invokeAdminUsersBody(t, deps, http.MethodDelete, "/admin/v1/users/9", "")
	assertStatus(t, rec, http.StatusForbidden)
	if !strings.Contains(rec.Body.String(), "admin_cannot_delete_admin") {
		t.Fatalf("body=%s want admin_cannot_delete_admin", rec.Body.String())
	}
	if del.calls != 0 {
		t.Fatalf("admin target must NOT be soft-deleted; calls=%d", del.calls)
	}
}

// TestDeleteUser_NotFound:不存在的用户在任何改动前即返回 404。
func TestDeleteUser_NotFound(t *testing.T) {
	del := &userSoftDeleteStub{}
	deps := deleteDeps(admindb.AdminGetUserForTenantRow{}, pgx.ErrNoRows, del, &sessionRevokerStub{}, &adminAuditStub{})
	rec := invokeAdminUsersBody(t, deps, http.MethodDelete, "/admin/v1/users/404", "")
	assertStatus(t, rec, http.StatusNotFound)
	if del.calls != 0 {
		t.Fatalf("missing user must not be soft-deleted; calls=%d", del.calls)
	}
}
