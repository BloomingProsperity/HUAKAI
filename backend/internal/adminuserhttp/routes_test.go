package adminuserhttp

import (
	"context"
	"encoding/json"
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
	if audit.arg.Action != "unlock_user" || audit.arg.TargetType != "user" || audit.arg.ActorID != "12" || audit.arg.ActorRole != admin.RoleTenantOperator {
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
