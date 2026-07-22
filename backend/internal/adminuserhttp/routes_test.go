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
	links := &adminSocialLinkStub{unlinked: true}

	rec := invokeAdminUsers(t, Deps{
		Auth:        usersAuthStub{ident: tenantOperator(7)},
		Store:       store,
		SocialLinks: links,
	}, http.MethodDelete, "/admin/v1/users/101/account-bindings/github", nil)

	assertStatus(t, rec, http.StatusOK)
	if links.calls != 1 || links.gotTenantID != 7 || links.gotUserID != 101 || links.gotProvider != userauth.SocialProviderGitHub {
		t.Fatalf("unlink call mismatch: calls=%d tenant=%d user=%d provider=%q", links.calls, links.gotTenantID, links.gotUserID, links.gotProvider)
	}
	var body struct {
		Unlinked bool `json:"unlinked"`
	}
	decodeBody(t, rec, &body)
	if !body.Unlinked {
		t.Fatalf("unlinked=%v want true", body.Unlinked)
	}
}

func TestAdminUnlinkSocialIdentityRejectsLastLoginMethod(t *testing.T) {
	links := &adminSocialLinkStub{err: userauth.ErrLastLoginMethod}

	rec := invokeAdminUsers(t, Deps{
		Auth:        usersAuthStub{ident: tenantOperator(7)},
		Store:       &usersStoreStub{},
		SocialLinks: links,
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

type adminSocialLinkStub struct {
	calls       int
	gotTenantID int64
	gotUserID   int64
	gotProvider string
	unlinked    bool
	err         error
}

func (s *adminSocialLinkStub) UnlinkSocialIdentity(_ context.Context, tenantID, userID int64, provider string) (bool, error) {
	s.calls++
	s.gotTenantID = tenantID
	s.gotUserID = userID
	s.gotProvider = provider
	if s.err != nil {
		return false, s.err
	}
	return s.unlinked, nil
}

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

type twoFADisableStub struct {
	calls    int
	tenantID int64
	userID   int64
	err      error
}

func (s *twoFADisableStub) Disable(_ context.Context, tenantID, userID int64) error {
	s.calls++
	s.tenantID, s.userID = tenantID, userID
	return s.err
}

func TestAdminForceDisable2FATenantScopedAudited(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	disabler := &twoFADisableStub{}
	audit := &adminAuditStub{}

	rec := invokeAdminUsers(t, Deps{
		Auth:          usersAuthStub{ident: tenantOperator(7)},
		Store:         store,
		TwoFADisabler: disabler,
		Audit:         audit,
	}, http.MethodPost, "/admin/v1/users/101/2fa/force-disable", nil)

	assertStatus(t, rec, http.StatusOK)
	if disabler.calls != 1 || disabler.tenantID != 7 || disabler.userID != 101 {
		t.Fatalf("disable call mismatch: calls=%d tenant=%d user=%d", disabler.calls, disabler.tenantID, disabler.userID)
	}
	if audit.calls != 1 || audit.arg.Action != "force_disable_2fa" || audit.arg.TargetType != "user" {
		t.Fatalf("audit mismatch: %+v", audit.arg)
	}
	if audit.arg.TenantID == nil || *audit.arg.TenantID != 7 || audit.arg.TargetID == nil || *audit.arg.TargetID != 101 {
		t.Fatalf("audit scope mismatch: %+v", audit.arg)
	}
}

func TestAdminForceDisable2FARequiresAdmin(t *testing.T) {
	disabler := &twoFADisableStub{}
	audit := &adminAuditStub{}
	rec := invokeAdminUsers(t, Deps{
		Auth:          usersAuthStub{err: admin.ErrAdminUnauthorized},
		Store:         &usersStoreStub{},
		TwoFADisabler: disabler,
		Audit:         audit,
	}, http.MethodPost, "/admin/v1/users/101/2fa/force-disable", nil)

	assertStatus(t, rec, http.StatusUnauthorized)
	if disabler.calls != 0 || audit.calls != 0 {
		t.Fatalf("unauthorized force-disable touched deps: disabler=%+v audit=%+v", disabler, audit)
	}
}

func TestAdminForceDisable2FAUnknownUser404BeforeMutation(t *testing.T) {
	store := &usersStoreStub{getErr: pgx.ErrNoRows}
	disabler := &twoFADisableStub{}
	audit := &adminAuditStub{}
	rec := invokeAdminUsers(t, Deps{
		Auth:          usersAuthStub{ident: tenantOperator(7)},
		Store:         store,
		TwoFADisabler: disabler,
		Audit:         audit,
	}, http.MethodPost, "/admin/v1/users/101/2fa/force-disable", nil)

	assertStatus(t, rec, http.StatusNotFound)
	if disabler.calls != 0 || audit.calls != 0 {
		t.Fatalf("unknown user touched mutation deps: disabler=%+v audit=%+v", disabler, audit)
	}
}

type passkeyResetStub struct {
	calls    int
	tenantID int64
	userID   int64
	cleared  int
	err      error
}

func (s *passkeyResetStub) AdminClearCredentials(_ context.Context, tenantID, userID int64) (int, error) {
	s.calls++
	s.tenantID, s.userID = tenantID, userID
	return s.cleared, s.err
}

func TestAdminResetPasskeyTenantScopedAudited(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	resetter := &passkeyResetStub{cleared: 3}
	audit := &adminAuditStub{}
	rec := invokeAdminUsers(t, Deps{Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, PasskeyResetter: resetter, Audit: audit}, http.MethodDelete, "/admin/v1/users/101/passkeys", nil)
	assertStatus(t, rec, http.StatusOK)
	if resetter.calls != 1 || resetter.tenantID != 7 || resetter.userID != 101 {
		t.Fatalf("reset call mismatch: calls=%d tenant=%d user=%d", resetter.calls, resetter.tenantID, resetter.userID)
	}
	if audit.calls != 1 || audit.arg.Action != "reset_passkey" || audit.arg.TargetType != "user" {
		t.Fatalf("audit mismatch: %+v", audit.arg)
	}
	if audit.arg.TenantID == nil || *audit.arg.TenantID != 7 || audit.arg.TargetID == nil || *audit.arg.TargetID != 101 {
		t.Fatalf("audit scope mismatch: %+v", audit.arg)
	}
}

func TestAdminResetPasskeyRequiresAdmin(t *testing.T) {
	resetter := &passkeyResetStub{}
	audit := &adminAuditStub{}
	rec := invokeAdminUsers(t, Deps{Auth: usersAuthStub{err: admin.ErrAdminUnauthorized}, Store: &usersStoreStub{}, PasskeyResetter: resetter, Audit: audit}, http.MethodDelete, "/admin/v1/users/101/passkeys", nil)
	assertStatus(t, rec, http.StatusUnauthorized)
	if resetter.calls != 0 || audit.calls != 0 {
		t.Fatalf("unauthorized reset touched deps: resetter=%+v audit=%+v", resetter, audit)
	}
}

func TestAdminResetPasskeyUnknownUser404BeforeMutation(t *testing.T) {
	store := &usersStoreStub{getErr: pgx.ErrNoRows}
	resetter := &passkeyResetStub{}
	audit := &adminAuditStub{}
	rec := invokeAdminUsers(t, Deps{Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, PasskeyResetter: resetter, Audit: audit}, http.MethodDelete, "/admin/v1/users/101/passkeys", nil)
	assertStatus(t, rec, http.StatusNotFound)
	if resetter.calls != 0 || audit.calls != 0 {
		t.Fatalf("unknown user touched mutation deps: resetter=%+v audit=%+v", resetter, audit)
	}
}

type userGroupSetterStub struct {
	calls    int
	tenantID int64
	userID   int64
	group    string
	err      error
}

func (s *userGroupSetterStub) SetUserGroupForTenant(_ context.Context, tenantID, userID int64, group string) error {
	s.calls++
	s.tenantID, s.userID, s.group = tenantID, userID, group
	return s.err
}

func TestAdminSetUserGroupTenantScopedAudited(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	setter := &userGroupSetterStub{}
	audit := &adminAuditStub{}
	deps := Deps{Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserGroupSetter: setter, Audit: audit}
	router := chi.NewRouter()
	router.Route("/admin/v1/users", func(r chi.Router) { MountRoutes(r, deps) })
	req := httptest.NewRequest(http.MethodPut, "/admin/v1/users/101/group", strings.NewReader(`{"group":"premium"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	if setter.calls != 1 || setter.tenantID != 7 || setter.userID != 101 || setter.group != "premium" {
		t.Fatalf("set group mismatch: %+v", setter)
	}
	if audit.calls != 1 || audit.arg.Action != "set_user_group" || audit.arg.TargetType != "user" {
		t.Fatalf("audit mismatch: %+v", audit.arg)
	}
	if audit.arg.TenantID == nil || *audit.arg.TenantID != 7 || audit.arg.TargetID == nil || *audit.arg.TargetID != 101 {
		t.Fatalf("audit scope mismatch: %+v", audit.arg)
	}
}

func TestAdminSetUserGroupRequiresAdmin(t *testing.T) {
	setter := &userGroupSetterStub{}
	audit := &adminAuditStub{}
	rec := invokeAdminUsers(t, Deps{
		Auth: usersAuthStub{err: admin.ErrAdminUnauthorized}, Store: &usersStoreStub{}, UserGroupSetter: setter, Audit: audit,
	}, http.MethodPut, "/admin/v1/users/101/group", []byte(`{"group":"premium"}`))
	assertStatus(t, rec, http.StatusUnauthorized)
	if setter.calls != 0 || audit.calls != 0 {
		t.Fatalf("unauthorized set-group touched deps: setter=%+v audit=%+v", setter, audit)
	}
}

type userRemarkSetterStub struct {
	calls    int
	tenantID int64
	userID   int64
	remark   string
	err      error
}

func (s *userRemarkSetterStub) SetUserRemarkForTenant(_ context.Context, tenantID, userID int64, remark string) error {
	s.calls++
	s.tenantID, s.userID, s.remark = tenantID, userID, remark
	return s.err
}

func TestAdminSetUserRemarkTenantScopedAudited(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	setter := &userRemarkSetterStub{}
	audit := &adminAuditStub{}
	deps := Deps{Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserRemarkSetter: setter, Audit: audit}
	router := chi.NewRouter()
	router.Route("/admin/v1/users", func(r chi.Router) { MountRoutes(r, deps) })
	req := httptest.NewRequest(http.MethodPut, "/admin/v1/users/101/remark", strings.NewReader(`{"remark":"vip customer"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	if setter.calls != 1 || setter.tenantID != 7 || setter.userID != 101 || setter.remark != "vip customer" {
		t.Fatalf("set remark mismatch: %+v", setter)
	}
	if audit.calls != 1 || audit.arg.Action != "set_user_remark" || audit.arg.TargetType != "user" {
		t.Fatalf("audit mismatch: %+v", audit.arg)
	}
	if audit.arg.TenantID == nil || *audit.arg.TenantID != 7 || audit.arg.TargetID == nil || *audit.arg.TargetID != 101 {
		t.Fatalf("audit scope mismatch: %+v", audit.arg)
	}
}

func TestAdminSetUserRemarkRequiresAdmin(t *testing.T) {
	setter := &userRemarkSetterStub{}
	audit := &adminAuditStub{}
	rec := invokeAdminUsers(t, Deps{Auth: usersAuthStub{err: admin.ErrAdminUnauthorized}, Store: &usersStoreStub{}, UserRemarkSetter: setter, Audit: audit}, http.MethodPut, "/admin/v1/users/101/remark", nil)
	assertStatus(t, rec, http.StatusUnauthorized)
	if setter.calls != 0 || audit.calls != 0 {
		t.Fatalf("unauthorized set-remark touched deps: setter=%+v audit=%+v", setter, audit)
	}
}

// platformAdmin 构造 platform_admin 身份(ScopeTenantID=0,跨租户但须显式 ?tenant_id)。
func platformAdmin() admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 99, Role: admin.RolePlatformAdmin}
}

type userStatusSetterStub struct {
	calls    int
	tenantID int64
	userID   int64
	status   string
	affected int64
	err      error
}

func (s *userStatusSetterStub) SetUserStatusForTenant(_ context.Context, tenantID, userID int64, status string) (int64, error) {
	s.calls++
	s.tenantID = tenantID
	s.userID = userID
	s.status = status
	if s.err != nil {
		return 0, s.err
	}
	switch {
	case s.affected < 0: // 哨兵:模拟 UPDATE 命中 0 行(软删用户)
		return 0, nil
	case s.affected == 0: // 零值默认:1 行受影响(常态成功)
		return 1, nil
	default:
		return s.affected, nil
	}
}

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
// MUTATION: handler 不调 UserStatusSetter / 不写 audit → setter.calls/audit.calls 0 → 红。
func TestAdminSetUserStatusDisableAudited(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	setter := &userStatusSetterStub{}
	audit := &adminAuditStub{}
	rec := invokeAdminUsersBody(t, Deps{
		Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserStatusSetter: setter, Audit: audit,
	}, http.MethodPut, "/admin/v1/users/101/status", `{"status":"disabled","reason":"abuse"}`)
	assertStatus(t, rec, http.StatusOK)
	if setter.calls != 1 || setter.tenantID != 7 || setter.userID != 101 || setter.status != "disabled" {
		t.Fatalf("set status mismatch: %+v", setter)
	}
	if audit.calls != 1 || audit.arg.Action != "set_user_status" || audit.arg.TargetType != "user" {
		t.Fatalf("audit mismatch: %+v", audit.arg)
	}
	if audit.arg.TenantID == nil || *audit.arg.TenantID != 7 || audit.arg.TargetID == nil || *audit.arg.TargetID != 101 {
		t.Fatalf("audit scope mismatch: %+v", audit.arg)
	}
	if len(audit.arg.Payload) == 0 || !strings.Contains(string(audit.arg.Payload), "status_before") {
		t.Fatalf("audit payload 缺 before/after: %s", audit.arg.Payload)
	}
}

// TestAdminSetUserStatusDisable_RevokesSessions 封禁第三轴:置 disabled 必须撤该用户
// 全部既有会话(登录门与 API key 联查只挡新入口,已签发 bearer/refresh 不撤能活到自然过期);
// 重新启用(active)不撤;撤销失败映 503 不静默吞。
// MUTATION: 去掉 handler 里 status=="disabled" 的 SessionRevoker 调用 → rev.calls==0 → 红。
func TestAdminSetUserStatusDisable_RevokesSessions(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	setter := &userStatusSetterStub{}
	rev := &sessionRevokerStub{}
	audit := &adminAuditStub{}
	deps := Deps{
		Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store,
		UserStatusSetter: setter, SessionRevoker: rev, Audit: audit,
	}
	rec := invokeAdminUsersBody(t, deps, http.MethodPut, "/admin/v1/users/101/status", `{"status":"disabled"}`)
	assertStatus(t, rec, http.StatusOK)
	if rev.calls != 1 || rev.in.TenantID != 7 || rev.in.UserID != 101 || rev.in.Reason != "admin_user_disabled" {
		t.Fatalf("封禁未撤会话: calls=%d in=%+v", rev.calls, rev.in)
	}
	// 重新启用不撤会话。
	rec = invokeAdminUsersBody(t, deps, http.MethodPut, "/admin/v1/users/101/status", `{"status":"active"}`)
	assertStatus(t, rec, http.StatusOK)
	if rev.calls != 1 {
		t.Fatalf("启用不该撤会话: calls=%d want 1", rev.calls)
	}
	// 撤销失败 → 503(调用者可重试,RevokeUser 幂等)。
	deps.SessionRevoker = &sessionRevokerStub{err: errors.New("revoke backend down")}
	rec = invokeAdminUsersBody(t, deps, http.MethodPut, "/admin/v1/users/101/status", `{"status":"disabled"}`)
	assertStatus(t, rec, http.StatusServiceUnavailable)
}

// TestAdminSetUserStatusInvalidRejected 非 active/disabled 状态 → 400,不触达 store/audit。
// MUTATION: 删 status 白名单校验 → 'deleted'/'locked' 直写 → 红。
func TestAdminSetUserStatusInvalidRejected(t *testing.T) {
	for _, bad := range []string{"deleted", "locked", "", "ACTIVE", "banned"} {
		store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
		setter := &userStatusSetterStub{}
		audit := &adminAuditStub{}
		rec := invokeAdminUsersBody(t, Deps{
			Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserStatusSetter: setter, Audit: audit,
		}, http.MethodPut, "/admin/v1/users/101/status", `{"status":"`+bad+`"}`)
		assertStatus(t, rec, http.StatusBadRequest)
		if setter.calls != 0 || audit.calls != 0 {
			t.Fatalf("status=%q 非法却触达 deps: setter=%+v audit=%+v", bad, setter, audit)
		}
	}
}

// TestAdminSetUserStatusSoftDeletedNotFound store UPDATE 0 行(用户软删)→ 404,
// 不静默成功。MUTATION: 删 affected==0 守卫 → 报成功 200 → 红。
func TestAdminSetUserStatusSoftDeletedNotFound(t *testing.T) {
	store := &usersStoreStub{getRow: admindb.AdminGetUserForTenantRow{ID: 101, Status: "active"}}
	setter := &userStatusSetterStub{affected: -1} // 用 -1 哨兵表示"返回 0 affected"
	audit := &adminAuditStub{}
	rec := invokeAdminUsersBody(t, Deps{
		Auth: usersAuthStub{ident: tenantOperator(7)}, Store: store, UserStatusSetter: setter, Audit: audit,
	}, http.MethodPut, "/admin/v1/users/101/status", `{"status":"disabled"}`)
	assertStatus(t, rec, http.StatusNotFound)
	if audit.calls != 0 {
		t.Fatalf("0 affected 不应写审计,got audit.calls=%d", audit.calls)
	}
}
