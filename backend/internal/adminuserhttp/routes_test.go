package adminuserhttp

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

func TestAdminListUsers_PaginationCapsAndOffset(t *testing.T) {
	store := &usersStoreStub{
		listRows: []admindb.AdminListUsersForTenantRow{{
			ID:        101,
			Email:     "alice@example.test",
			Role:      "user",
			Status:    "active",
			Balance:   "12.50000000",
			CreatedAt: pgTimestamp(time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)),
		}},
	}

	rec := invokeAdminUsers(t, Deps{
		Auth:  usersAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, http.MethodGet, "/admin/v1/users?limit=999&offset=12&q=alice", nil)

	assertStatus(t, rec, http.StatusOK)
	if store.listArg.TenantID != 7 || store.listArg.PageLimit != 100 || store.listArg.PageOffset != 12 || store.listArg.Query != "alice" {
		t.Fatalf("list args mismatch: %+v", store.listArg)
	}
	var body struct {
		Items []struct {
			ID      int64  `json:"id"`
			Email   string `json:"email"`
			Balance string `json:"balance"`
		} `json:"items"`
		Limit  int32 `json:"limit"`
		Offset int32 `json:"offset"`
	}
	decodeBody(t, rec, &body)
	if body.Limit != 100 || body.Offset != 12 || len(body.Items) != 1 || body.Items[0].Balance != "12.50000000" {
		t.Fatalf("list response mismatch: %+v", body)
	}
}

// TestAdminUsers_ProjectUserGroupAndRemark 守护 U18/U19:admin 用户 LIST 与
// 单用户 GET 都必须投影已存储的 users.user_group 与 users.remark 列,
// 使运营者无需打开每条记录即可看到用户的路由分组与 admin 备注。
// 变异:在任一 handler 去掉 UserGroup/Remark 映射(或从 SELECT 去掉该列)
// → 对应响应字段为空 → 红。两个子用例覆盖两个不同 handler。
func TestAdminUsers_ProjectUserGroupAndRemark(t *testing.T) {
	t.Run("list projects group+remark", func(t *testing.T) {
		store := &usersStoreStub{
			listRows: []admindb.AdminListUsersForTenantRow{{
				ID:        101,
				Email:     "alice@example.test",
				Role:      "user",
				Status:    "active",
				UserGroup: "vip",
				Remark:    "priority client",
				Balance:   "1.00000000",
				CreatedAt: pgTimestamp(time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)),
			}},
		}
		rec := invokeAdminUsers(t, Deps{Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store},
			http.MethodGet, "/admin/v1/users", nil)
		assertStatus(t, rec, http.StatusOK)
		var body struct {
			Items []struct {
				UserGroup string `json:"user_group"`
				Remark    string `json:"remark"`
			} `json:"items"`
		}
		decodeBody(t, rec, &body)
		if len(body.Items) != 1 || body.Items[0].UserGroup != "vip" || body.Items[0].Remark != "priority client" {
			t.Fatalf("list must project user_group+remark; got %+v", body)
		}
	})

	t.Run("single GET projects group+remark", func(t *testing.T) {
		store := &usersStoreStub{
			getRow: admindb.AdminGetUserForTenantRow{
				ID:        101,
				Email:     "alice@example.test",
				Role:      "user",
				Status:    "active",
				UserGroup: "staff",
				Remark:    "internal account",
				Balance:   "0.00000000",
				CreatedAt: pgTimestamp(time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)),
			},
		}
		rec := invokeAdminUsers(t, Deps{Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store},
			http.MethodGet, "/admin/v1/users/101", nil)
		assertStatus(t, rec, http.StatusOK)
		var body struct {
			UserGroup string `json:"user_group"`
			Remark    string `json:"remark"`
		}
		decodeBody(t, rec, &body)
		if body.UserGroup != "staff" || body.Remark != "internal account" {
			t.Fatalf("single GET must project user_group+remark; got %+v", body)
		}
	})
}

func TestAdminUsersAuthRequired(t *testing.T) {
	t.Run("missing admin credential returns 401 before store", func(t *testing.T) {
		store := &usersStoreStub{}
		rec := invokeAdminUsers(t, Deps{
			Auth:  usersAuthStub{err: admin.ErrAdminUnauthorized},
			Store: store,
		}, http.MethodGet, "/admin/v1/users", nil)

		assertStatus(t, rec, http.StatusUnauthorized)
		if store.calls() != 0 {
			t.Fatalf("unauthorized request touched store: %+v", store)
		}
	})

	t.Run("resolved non-admin role returns 403 before store", func(t *testing.T) {
		store := &usersStoreStub{}
		rec := invokeAdminUsers(t, Deps{
			Auth:  usersAuthStub{ident: admin.AdminIdentity{TokenID: 99, Role: "user", ScopeTenantID: 7}},
			Store: store,
		}, http.MethodGet, "/admin/v1/users", nil)

		assertStatus(t, rec, http.StatusForbidden)
		if store.calls() != 0 {
			t.Fatalf("non-admin request touched store: %+v", store)
		}
	})
}

func TestAdminTwoFAStats_TenantScopedRate(t *testing.T) {
	store := &usersStoreStub{
		twoFAStatsRow: admindb.AdminGetTwoFAAdoptionStatsForTenantRow{
			EnabledCount:   2,
			TotalUserCount: 3,
		},
	}

	rec := invokeAdminUsers(t, Deps{
		Auth:  usersAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, http.MethodGet, "/admin/v1/users/2fa-adoption-stats", nil)

	assertStatus(t, rec, http.StatusOK)
	if store.twoFAStatsTenantID != 7 {
		t.Fatalf("2fa stats tenant mismatch: got=%d want=7", store.twoFAStatsTenantID)
	}
	var body struct {
		EnabledUsers int64   `json:"enabled_users"`
		TotalUsers   int64   `json:"total_users"`
		EnabledRate  float64 `json:"enabled_rate"`
	}
	decodeBody(t, rec, &body)
	if body.EnabledUsers != 2 || body.TotalUsers != 3 || math.Abs(body.EnabledRate-(2.0/3.0)) > 0.0000001 {
		t.Fatalf("2fa stats response mismatch: %+v", body)
	}
}

func TestAdminUsersNoMutation(t *testing.T) {
	store := &usersStoreStub{}
	methods := []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete}
	targets := []string{
		"/admin/v1/users",
		"/admin/v1/users/2fa-adoption-stats",
		"/admin/v1/users/101",
		"/admin/v1/users/101/balance-history",
	}

	for _, method := range methods {
		for _, target := range targets {
			rec := invokeAdminUsers(t, Deps{
				Auth:  usersAuthStub{ident: tenantOperator(7)},
				Store: store,
			}, method, target, nil)
			if rec.Code == http.StatusOK || rec.Code == http.StatusCreated || rec.Code == http.StatusAccepted || rec.Code == http.StatusNoContent {
				t.Fatalf("%s %s unexpectedly succeeded with status=%d", method, target, rec.Code)
			}
		}
	}
	if store.calls() != 0 {
		t.Fatalf("mutation method touched read store: %+v", store)
	}
}

func TestAdminUnlinkSocialIdentityTenantScoped(t *testing.T) {
	store := &usersStoreStub{}
	links := &adminSocialLinkStub{unlinked: true, sessionsRevoked: 2}

	rec := invokeAdminUsers(t, Deps{
		Auth:          usersAuthStub{ident: tenantOperator(7)},
		Store:         store,
		UserMutations: links,
	}, http.MethodDelete, "/admin/v1/users/101/account-bindings/github", nil)

	assertStatus(t, rec, http.StatusOK)
	if links.calls != 1 || links.gotTenantID != 7 || links.gotUserID != 101 || links.gotProvider != userauth.SocialProviderGitHub {
		t.Fatalf("unlink call mismatch: calls=%d tenant=%d user=%d provider=%q", links.calls, links.gotTenantID, links.gotUserID, links.gotProvider)
	}
	var body struct {
		Unlinked        bool  `json:"unlinked"`
		SessionsRevoked int64 `json:"sessions_revoked"`
	}
	decodeBody(t, rec, &body)
	if !body.Unlinked || body.SessionsRevoked != 2 {
		t.Fatalf("unlink body=%+v want unlinked and two revoked sessions", body)
	}
}

func TestAdminUnlinkSocialIdentityRejectsLastLoginMethod(t *testing.T) {
	links := &adminSocialLinkStub{err: userauth.ErrLastLoginMethod}

	rec := invokeAdminUsers(t, Deps{
		Auth:          usersAuthStub{ident: tenantOperator(7)},
		Store:         &usersStoreStub{},
		UserMutations: links,
	}, http.MethodDelete, "/admin/v1/users/101/account-bindings/google", nil)

	assertStatus(t, rec, http.StatusConflict)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, rec, &body)
	if body.Error.Code != "last_login_method" {
		t.Fatalf("error code=%q want last_login_method", body.Error.Code)
	}
}

func TestAdminUnlockUserTenantScopedAudited(t *testing.T) {
	store := &usersStoreStub{
		getRow: admindb.AdminGetUserForTenantRow{
			ID:        101,
			Email:     "locked@example.test",
			Role:      "user",
			Status:    "locked",
			Balance:   "0.00000000",
			CreatedAt: pgTimestamp(time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)),
		},
	}
	unlocker := &adminUnlockStub{
		user: userauth.User{ID: 101, TenantID: 7, Email: "locked@example.test", Status: userauth.UserStatusActive},
	}
	audit := &adminAuditStub{}

	rec := invokeAdminUsers(t, Deps{
		Auth:     usersAuthStub{ident: tenantOperator(7)},
		Store:    store,
		Unlocker: unlocker,
		Audit:    audit,
	}, http.MethodPost, "/admin/v1/users/101/unlock", nil)

	assertStatus(t, rec, http.StatusOK)
	if store.getCalls != 1 || store.getArg.TenantID != 7 || store.getArg.UserID != 101 {
		t.Fatalf("existence check mismatch: calls=%d arg=%+v", store.getCalls, store.getArg)
	}
	if unlocker.calls != 1 || unlocker.tenantID != 7 || unlocker.userID != 101 {
		t.Fatalf("unlock call mismatch: calls=%d tenant=%d user=%d", unlocker.calls, unlocker.tenantID, unlocker.userID)
	}
	if audit.calls != 1 {
		t.Fatalf("audit calls=%d want 1", audit.calls)
	}
	if audit.arg.Action != "unlock_user" || audit.arg.TargetType != "user" || audit.arg.ActorID != "admin_token:12" || audit.arg.ActorRole != admin.RoleTenantOperator {
		t.Fatalf("audit metadata mismatch: %+v", audit.arg)
	}
	if audit.arg.TenantID == nil || *audit.arg.TenantID != 7 || audit.arg.TargetID == nil || *audit.arg.TargetID != 101 {
		t.Fatalf("audit scope mismatch: %+v", audit.arg)
	}
	if !strings.Contains(string(audit.arg.Payload), `"status_before":"locked"`) ||
		!strings.Contains(string(audit.arg.Payload), `"status_after":"active"`) {
		t.Fatalf("audit payload missing status transition: %s", string(audit.arg.Payload))
	}
	var body struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	decodeBody(t, rec, &body)
	if body.ID != 101 || body.Status != "active" {
		t.Fatalf("unlock body mismatch: %+v", body)
	}
}

func TestAdminUnlockRequiresAdmin(t *testing.T) {
	store := &usersStoreStub{}
	unlocker := &adminUnlockStub{}
	audit := &adminAuditStub{}

	rec := invokeAdminUsers(t, Deps{
		Auth:     usersAuthStub{err: admin.ErrAdminUnauthorized},
		Store:    store,
		Unlocker: unlocker,
		Audit:    audit,
	}, http.MethodPost, "/admin/v1/users/101/unlock", nil)

	assertStatus(t, rec, http.StatusUnauthorized)
	if store.calls() != 0 || unlocker.calls != 0 || audit.calls != 0 {
		t.Fatalf("unauthorized unlock touched dependencies: store=%+v unlocker=%+v audit=%+v", store, unlocker, audit)
	}
}

func TestAdminUnlockUnknownUser404BeforeMutation(t *testing.T) {
	store := &usersStoreStub{getErr: pgx.ErrNoRows}
	unlocker := &adminUnlockStub{}
	audit := &adminAuditStub{}

	rec := invokeAdminUsers(t, Deps{
		Auth:     usersAuthStub{ident: tenantOperator(7)},
		Store:    store,
		Unlocker: unlocker,
		Audit:    audit,
	}, http.MethodPost, "/admin/v1/users/101/unlock", nil)

	assertStatus(t, rec, http.StatusNotFound)
	if store.getCalls != 1 || store.getArg.TenantID != 7 || store.getArg.UserID != 101 {
		t.Fatalf("existence check mismatch: calls=%d arg=%+v", store.getCalls, store.getArg)
	}
	if unlocker.calls != 0 || audit.calls != 0 {
		t.Fatalf("unknown user touched mutation dependencies: unlocker=%+v audit=%+v", unlocker, audit)
	}
}

func TestAdminUnlockUsesAtomicStoreWhenConfigured(t *testing.T) {
	store := &usersStoreStub{
		getRow: admindb.AdminGetUserForTenantRow{
			ID:        101,
			Status:    "locked",
			CreatedAt: pgTimestamp(time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)),
		},
	}
	atomic := &adminUnlockAuditStub{
		user: userauth.User{ID: 101, TenantID: 7, Status: userauth.UserStatusActive},
	}
	unlocker := &adminUnlockStub{}
	audit := &adminAuditStub{}

	rec := invokeAdminUsers(t, Deps{
		Auth:        usersAuthStub{ident: tenantOperator(7)},
		Store:       store,
		UnlockAudit: atomic,
		Unlocker:    unlocker,
		Audit:       audit,
	}, http.MethodPost, "/admin/v1/users/101/unlock", nil)

	assertStatus(t, rec, http.StatusOK)
	if atomic.calls != 1 || atomic.tenantID != 7 || atomic.userID != 101 || atomic.input.BeforeStatus != "locked" {
		t.Fatalf("atomic unlock mismatch: %+v", atomic)
	}
	if unlocker.calls != 0 || audit.calls != 0 {
		t.Fatalf("atomic path used fallback unlock/audit: unlocker=%+v audit=%+v", unlocker, audit)
	}
}

type usersAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s usersAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

type usersStoreStub struct {
	listRows           []admindb.AdminListUsersForTenantRow
	getRow             admindb.AdminGetUserForTenantRow
	historyRows        []admindb.AdminListUserBalanceHistoryForTenantRow
	twoFAStatsRow      admindb.AdminGetTwoFAAdoptionStatsForTenantRow
	getErr             error
	listArg            admindb.AdminListUsersForTenantParams
	getArg             admindb.AdminGetUserForTenantParams
	historyArg         admindb.AdminListUserBalanceHistoryForTenantParams
	twoFAStatsTenantID int64
	listCalls          int
	getCalls           int
	historyCalls       int
	twoFAStatsCalls    int
}

func (s *usersStoreStub) AdminListUsersForTenant(_ context.Context, arg admindb.AdminListUsersForTenantParams) ([]admindb.AdminListUsersForTenantRow, error) {
	s.listCalls++
	s.listArg = arg
	return s.listRows, nil
}

func (s *usersStoreStub) AdminGetUserForTenant(_ context.Context, arg admindb.AdminGetUserForTenantParams) (admindb.AdminGetUserForTenantRow, error) {
	s.getCalls++
	s.getArg = arg
	if s.getErr != nil {
		return admindb.AdminGetUserForTenantRow{}, s.getErr
	}
	return s.getRow, nil
}

func (s *usersStoreStub) AdminListUserBalanceHistoryForTenant(_ context.Context, arg admindb.AdminListUserBalanceHistoryForTenantParams) ([]admindb.AdminListUserBalanceHistoryForTenantRow, error) {
	s.historyCalls++
	s.historyArg = arg
	return s.historyRows, nil
}

func (s *usersStoreStub) AdminGetTwoFAAdoptionStatsForTenant(_ context.Context, tenantID int64) (admindb.AdminGetTwoFAAdoptionStatsForTenantRow, error) {
	s.twoFAStatsCalls++
	s.twoFAStatsTenantID = tenantID
	return s.twoFAStatsRow, nil
}

func (s *usersStoreStub) calls() int {
	return s.listCalls + s.getCalls + s.historyCalls + s.twoFAStatsCalls
}

type userMutationStub struct {
	calls           int
	operation       string
	tenantID        int64
	userID          int64
	gotTenantID     int64
	gotUserID       int64
	gotProvider     string
	group           string
	remark          string
	status          string
	reason          string
	audit           unlockAuditInput
	unlinked        bool
	cleared         int
	sessionsRevoked int64
	affected        int64
	err             error
}

func (s *userMutationStub) record(operation string, tenantID, userID int64, audit unlockAuditInput) error {
	s.calls++
	s.operation = operation
	s.tenantID = tenantID
	s.userID = userID
	s.gotTenantID = tenantID
	s.gotUserID = userID
	s.audit = audit
	if s.err != nil {
		return s.err
	}
	if s.affected < 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *userMutationStub) UnlinkSocialIdentityWithAudit(
	_ context.Context,
	tenantID, userID int64,
	provider string,
	audit unlockAuditInput,
) (bool, int64, error) {
	s.gotProvider = provider
	if err := s.record("unlink_social_identity", tenantID, userID, audit); err != nil {
		return false, 0, err
	}
	return s.unlinked, s.sessionsRevoked, nil
}

func (s *userMutationStub) ForceDisableTwoFAWithAudit(
	_ context.Context,
	tenantID, userID int64,
	audit unlockAuditInput,
) (int64, error) {
	if err := s.record("force_disable_2fa", tenantID, userID, audit); err != nil {
		return 0, err
	}
	return s.sessionsRevoked, nil
}

func (s *userMutationStub) ResetPasskeysWithAudit(
	_ context.Context,
	tenantID, userID int64,
	audit unlockAuditInput,
) (int, int64, error) {
	if err := s.record("reset_passkey", tenantID, userID, audit); err != nil {
		return 0, 0, err
	}
	return s.cleared, s.sessionsRevoked, nil
}

func (s *userMutationStub) SetUserGroupWithAudit(
	_ context.Context,
	tenantID, userID int64,
	group string,
	audit unlockAuditInput,
) error {
	s.group = group
	return s.record("set_user_group", tenantID, userID, audit)
}

func (s *userMutationStub) SetUserRemarkWithAudit(
	_ context.Context,
	tenantID, userID int64,
	remark string,
	audit unlockAuditInput,
) error {
	s.remark = remark
	return s.record("set_user_remark", tenantID, userID, audit)
}

func (s *userMutationStub) SetUserStatusWithAudit(
	_ context.Context,
	tenantID, userID int64,
	status, reason string,
	audit unlockAuditInput,
) (int64, error) {
	s.status = status
	s.reason = reason
	if err := s.record("set_user_status", tenantID, userID, audit); err != nil {
		return 0, err
	}
	return s.sessionsRevoked, nil
}

func (s *userMutationStub) SoftDeleteUserWithAudit(
	_ context.Context,
	tenantID, userID int64,
	audit unlockAuditInput,
) (int64, error) {
	if err := s.record("delete_user", tenantID, userID, audit); err != nil {
		return 0, err
	}
	return s.sessionsRevoked, nil
}

type adminSocialLinkStub = userMutationStub

type adminUnlockStub struct {
	calls    int
	tenantID int64
	userID   int64
	user     userauth.User
	err      error
}

func (s *adminUnlockStub) UnlockUser(_ context.Context, tenantID, userID int64) (userauth.User, error) {
	s.calls++
	s.tenantID = tenantID
	s.userID = userID
	if s.err != nil {
		return userauth.User{}, s.err
	}
	return s.user, nil
}

type adminAuditStub struct {
	calls int
	arg   admindb.InsertAdminAuditEventParams
	err   error
}

func (s *adminAuditStub) InsertAdminAuditEvent(_ context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	s.calls++
	s.arg = arg
	if s.err != nil {
		return admindb.InsertAdminAuditEventRow{}, s.err
	}
	return admindb.InsertAdminAuditEventRow{ID: int64(s.calls)}, nil
}

type adminUnlockAuditStub struct {
	calls    int
	tenantID int64
	userID   int64
	input    unlockAuditInput
	user     userauth.User
	err      error
}

func (s *adminUnlockAuditStub) UnlockUserWithAudit(_ context.Context, tenantID, userID int64, input unlockAuditInput) (userauth.User, error) {
	s.calls++
	s.tenantID = tenantID
	s.userID = userID
	s.input = input
	if s.err != nil {
		return userauth.User{}, s.err
	}
	return s.user, nil
}

func invokeAdminUsers(t *testing.T, deps Deps, method, target string, _ any) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/admin/v1/users", NewListHandler(deps))
	r.Route("/admin/v1/users", func(r chi.Router) {
		MountRoutes(r, deps)
	})
	req := httptest.NewRequest(method, target, strings.NewReader(""))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body: %v body=%s", err, strings.TrimSpace(rec.Body.String()))
	}
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, strings.TrimSpace(rec.Body.String()))
	}
}

func tenantOperator(tenantID int64) admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 12, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID}
}

func pgTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

type twoFADisableStub = userMutationStub

func TestAdminForceDisable2FATenantScopedAudited(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	disabler := &twoFADisableStub{sessionsRevoked: 2}

	rec := invokeAdminUsers(t, Deps{
		Auth:          usersAuthStub{ident: tenantOperator(7)},
		Store:         store,
		UserMutations: disabler,
	}, http.MethodPost, "/admin/v1/users/101/2fa/force-disable", nil)

	assertStatus(t, rec, http.StatusOK)
	if disabler.calls != 1 || disabler.tenantID != 7 || disabler.userID != 101 {
		t.Fatalf("disable call mismatch: calls=%d tenant=%d user=%d", disabler.calls, disabler.tenantID, disabler.userID)
	}
	if disabler.operation != "force_disable_2fa" || disabler.audit.ActorID != "admin_token:12" {
		t.Fatalf("事务日志输入不完整: %+v", disabler)
	}
	if !strings.Contains(rec.Body.String(), `"sessions_revoked":2`) {
		t.Fatalf("force-disable response missing session count: %s", rec.Body.String())
	}
}

func TestAdminForceDisable2FARequiresAdmin(t *testing.T) {
	disabler := &twoFADisableStub{}
	rec := invokeAdminUsers(t, Deps{
		Auth:          usersAuthStub{err: admin.ErrAdminUnauthorized},
		Store:         &usersStoreStub{},
		UserMutations: disabler,
	}, http.MethodPost, "/admin/v1/users/101/2fa/force-disable", nil)

	assertStatus(t, rec, http.StatusUnauthorized)
	if disabler.calls != 0 {
		t.Fatalf("unauthorized force-disable touched deps: disabler=%+v", disabler)
	}
}

func TestAdminForceDisable2FAUnknownUser404BeforeMutation(t *testing.T) {
	store := &usersStoreStub{}
	disabler := &twoFADisableStub{err: pgx.ErrNoRows}
	rec := invokeAdminUsers(t, Deps{
		Auth:          usersAuthStub{ident: tenantOperator(7)},
		Store:         store,
		UserMutations: disabler,
	}, http.MethodPost, "/admin/v1/users/101/2fa/force-disable", nil)

	assertStatus(t, rec, http.StatusNotFound)
	if disabler.calls != 1 {
		t.Fatalf("unknown user must be decided inside atomic store: disabler=%+v", disabler)
	}
}

type passkeyResetStub = userMutationStub

func TestAdminResetPasskeyTenantScopedAudited(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	resetter := &passkeyResetStub{cleared: 3, sessionsRevoked: 2}
	rec := invokeAdminUsers(t, Deps{Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserMutations: resetter}, http.MethodDelete, "/admin/v1/users/101/passkeys", nil)
	assertStatus(t, rec, http.StatusOK)
	if resetter.calls != 1 || resetter.tenantID != 7 || resetter.userID != 101 {
		t.Fatalf("reset call mismatch: calls=%d tenant=%d user=%d", resetter.calls, resetter.tenantID, resetter.userID)
	}
	if resetter.operation != "reset_passkey" || resetter.audit.ActorID != "admin_token:12" {
		t.Fatalf("事务日志输入不完整: %+v", resetter)
	}
	if !strings.Contains(rec.Body.String(), `"sessions_revoked":2`) {
		t.Fatalf("passkey reset response missing session count: %s", rec.Body.String())
	}
}

func TestAdminResetPasskeyRequiresAdmin(t *testing.T) {
	resetter := &passkeyResetStub{}
	rec := invokeAdminUsers(t, Deps{Auth: usersAuthStub{err: admin.ErrAdminUnauthorized}, Store: &usersStoreStub{}, UserMutations: resetter}, http.MethodDelete, "/admin/v1/users/101/passkeys", nil)
	assertStatus(t, rec, http.StatusUnauthorized)
	if resetter.calls != 0 {
		t.Fatalf("unauthorized reset touched deps: resetter=%+v", resetter)
	}
}

func TestAdminResetPasskeyUnknownUser404BeforeMutation(t *testing.T) {
	store := &usersStoreStub{}
	resetter := &passkeyResetStub{err: pgx.ErrNoRows}
	rec := invokeAdminUsers(t, Deps{Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserMutations: resetter}, http.MethodDelete, "/admin/v1/users/101/passkeys", nil)
	assertStatus(t, rec, http.StatusNotFound)
	if resetter.calls != 1 {
		t.Fatalf("unknown user must be decided inside atomic store: resetter=%+v", resetter)
	}
}

type userGroupSetterStub = userMutationStub

func TestAdminSetUserGroupTenantScopedAudited(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	setter := &userGroupSetterStub{}
	deps := Deps{Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserMutations: setter}
	router := chi.NewRouter()
	router.Route("/admin/v1/users", func(r chi.Router) { MountRoutes(r, deps) })
	req := httptest.NewRequest(http.MethodPut, "/admin/v1/users/101/group", strings.NewReader(`{"group":"premium"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	if setter.calls != 1 || setter.tenantID != 7 || setter.userID != 101 || setter.group != "premium" {
		t.Fatalf("set group mismatch: %+v", setter)
	}
	if setter.operation != "set_user_group" || setter.audit.ActorID != "admin_token:12" {
		t.Fatalf("事务日志输入不完整: %+v", setter)
	}
}

func TestAdminSetUserGroupRequiresAdmin(t *testing.T) {
	setter := &userGroupSetterStub{}
	rec := invokeAdminUsers(t, Deps{
		Auth: usersAuthStub{err: admin.ErrAdminUnauthorized}, Store: &usersStoreStub{}, UserMutations: setter,
	}, http.MethodPut, "/admin/v1/users/101/group", []byte(`{"group":"premium"}`))
	assertStatus(t, rec, http.StatusUnauthorized)
	if setter.calls != 0 {
		t.Fatalf("unauthorized set-group touched deps: setter=%+v", setter)
	}
}

type userRemarkSetterStub = userMutationStub

func TestAdminSetUserRemarkTenantScopedAudited(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	setter := &userRemarkSetterStub{}
	deps := Deps{Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserMutations: setter}
	router := chi.NewRouter()
	router.Route("/admin/v1/users", func(r chi.Router) { MountRoutes(r, deps) })
	req := httptest.NewRequest(http.MethodPut, "/admin/v1/users/101/remark", strings.NewReader(`{"remark":"vip customer"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	if setter.calls != 1 || setter.tenantID != 7 || setter.userID != 101 || setter.remark != "vip customer" {
		t.Fatalf("set remark mismatch: %+v", setter)
	}
	if setter.operation != "set_user_remark" || setter.audit.ActorID != "admin_token:12" {
		t.Fatalf("事务日志输入不完整: %+v", setter)
	}
}

func TestAdminSetUserRemarkRequiresAdmin(t *testing.T) {
	setter := &userRemarkSetterStub{}
	rec := invokeAdminUsers(t, Deps{Auth: usersAuthStub{err: admin.ErrAdminUnauthorized}, Store: &usersStoreStub{}, UserMutations: setter}, http.MethodPut, "/admin/v1/users/101/remark", nil)
	assertStatus(t, rec, http.StatusUnauthorized)
	if setter.calls != 0 {
		t.Fatalf("unauthorized set-remark touched deps: setter=%+v", setter)
	}
}

// platformAdmin 构造 platform_admin 身份(ScopeTenantID=0,跨租户但须显式 ?tenant_id)。
func platformAdmin() admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 99, Role: admin.RolePlatformAdmin}
}

type userStatusSetterStub = userMutationStub

func invokeAdminUsersBody(t *testing.T, deps Deps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Get("/admin/v1/users", NewListHandler(deps))
	router.Route("/admin/v1/users", func(r chi.Router) { MountRoutes(r, deps) })
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestAdminUsersPlatformAdminAllowedWithTenantID 平台租户归属守卫:
// platform_admin 仅可管理配置明确的平台自有租户。
// MUTATION: 把 resolveTenantIdentity 的 platform_admin 分支改回硬 403 →
// 本测试拿 403 → 红。
func TestAdminUsersPlatformAdminAllowedWithTenantID(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	rec := invokeAdminUsersBody(t, Deps{
		Auth:             usersAuthStub{ident: platformAdmin()},
		Store:            store,
		PlatformTenantID: 1,
	}, http.MethodGet, "/admin/v1/users/101?tenant_id=1", "")
	assertStatus(t, rec, http.StatusOK)
	if store.getArg.TenantID != 1 {
		t.Fatalf("platform_admin ?tenant_id=1 应解析为 tenant 1,got %d", store.getArg.TenantID)
	}
}

// TestAdminUsersPlatformAdminRequiresTenantID platform_admin 不带 ?tenant_id → 400
// (RBAC:跨租户必须指名)。MUTATION: 放行分支不要求 ?tenant_id → 拿到 200/落回某租户 → 红。
func TestAdminUsersPlatformAdminRequiresTenantID(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	rec := invokeAdminUsersBody(t, Deps{
		Auth:             usersAuthStub{ident: platformAdmin()},
		Store:            store,
		PlatformTenantID: 1,
	}, http.MethodGet, "/admin/v1/users/101", "")
	assertStatus(t, rec, http.StatusBadRequest)
	if store.getCalls != 0 {
		t.Fatalf("缺 ?tenant_id 不应触达 store,got getCalls=%d", store.getCalls)
	}
}

// TestAdminUsersTenantOperatorCrossTenantForbidden tenant_operator 带别租户
// ?tenant_id → 403(CanIssueForTenant 越权守卫,RBAC 语义不松动)。
// MUTATION: tenantFromQueryOrScope 漏 CanIssueForTenant 校验 → 跨租户读到 200 → 红。
func TestAdminUsersTenantOperatorCrossTenantForbidden(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	rec := invokeAdminUsersBody(t, Deps{
		Auth:  usersAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, http.MethodGet, "/admin/v1/users/101?tenant_id=8", "")
	assertStatus(t, rec, http.StatusForbidden)
	if store.getCalls != 0 {
		t.Fatalf("跨租户被拒不应触达 store,got getCalls=%d", store.getCalls)
	}
}

// TestAdminSetUserStatusDisableAudited 封禁主路径:tenant_operator 设 disabled,
// store 写入 + 审计 set_user_status(before/after payload)。
// MUTATION: handler 绕过原子 mutation store → setter.calls 0 → 红。
func TestAdminSetUserStatusDisableAudited(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	setter := &userStatusSetterStub{}
	rec := invokeAdminUsersBody(t, Deps{
		Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserMutations: setter,
	}, http.MethodPut, "/admin/v1/users/101/status", `{"status":"disabled","reason":"abuse"}`)
	assertStatus(t, rec, http.StatusOK)
	if setter.calls != 1 || setter.tenantID != 7 || setter.userID != 101 || setter.status != "disabled" {
		t.Fatalf("set status mismatch: %+v", setter)
	}
	if setter.operation != "set_user_status" || setter.reason != "abuse" || setter.audit.ActorID != "admin_token:12" {
		t.Fatalf("事务日志输入不完整: %+v", setter)
	}
}

// TestAdminSetUserStatusDisableReportsAtomicSessionRevocation 证明 handler 使用事务存储
// 返回的撤销数量，不再在状态提交后另走一条可能失败的会话路径。
func TestAdminSetUserStatusDisable_RevokesSessions(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	setter := &userStatusSetterStub{sessionsRevoked: 2}
	deps := Deps{
		Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store,
		UserMutations: setter,
	}
	rec := invokeAdminUsersBody(t, deps, http.MethodPut, "/admin/v1/users/101/status", `{"status":"disabled"}`)
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"sessions_revoked":2`) {
		t.Fatalf("封禁响应未返回事务内会话结果: %s", rec.Body.String())
	}
	// 重新启用不撤会话。
	setter.sessionsRevoked = 0
	rec = invokeAdminUsersBody(t, deps, http.MethodPut, "/admin/v1/users/101/status", `{"status":"active"}`)
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"sessions_revoked":0`) {
		t.Fatalf("启用响应的撤销数必须为 0: %s", rec.Body.String())
	}
	// 事务内撤销失败由 mutation store 整体回滚并返回 503。
	setter.err = errors.New("atomic session revoke failed")
	rec = invokeAdminUsersBody(t, deps, http.MethodPut, "/admin/v1/users/101/status", `{"status":"disabled"}`)
	assertStatus(t, rec, http.StatusServiceUnavailable)
}

// TestAdminSetUserStatusInvalidRejected 非 active/disabled 状态 → 400,不触达 store/audit。
// MUTATION: 删 status 白名单校验 → 'deleted'/'locked' 直写 → 红。
func TestAdminSetUserStatusInvalidRejected(t *testing.T) {
	for _, bad := range []string{"deleted", "locked", "", "ACTIVE", "banned"} {
		store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
		setter := &userStatusSetterStub{}
		rec := invokeAdminUsersBody(t, Deps{
			Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserMutations: setter,
		}, http.MethodPut, "/admin/v1/users/101/status", `{"status":"`+bad+`"}`)
		assertStatus(t, rec, http.StatusBadRequest)
		if setter.calls != 0 {
			t.Fatalf("status=%q 非法却触达 deps: setter=%+v", bad, setter)
		}
	}
}

func TestAdminSetUserStatusRecoveryConflictMaps409(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "reset_required"}}
	setter := &userStatusSetterStub{err: errUserStatusTransitionConflict}
	rec := invokeAdminUsersBody(t, Deps{
		Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserMutations: setter,
	}, http.MethodPut, "/admin/v1/users/101/status", `{"status":"active"}`)
	assertStatus(t, rec, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), `"admin_user_status_conflict"`) {
		t.Fatalf("body=%s want stable admin_user_status_conflict", rec.Body.String())
	}
}

// TestAdminSetUserStatusSoftDeletedNotFound store UPDATE 0 行(用户软删)→ 404,
// 不静默成功。MUTATION: 删 affected==0 守卫 → 报成功 200 → 红。
func TestAdminSetUserStatusSoftDeletedNotFound(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	setter := &userStatusSetterStub{affected: -1} // 用 -1 哨兵表示"返回 0 affected"
	rec := invokeAdminUsersBody(t, Deps{
		Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserMutations: setter,
	}, http.MethodPut, "/admin/v1/users/101/status", `{"status":"disabled"}`)
	assertStatus(t, rec, http.StatusNotFound)
}
