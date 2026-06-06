package adminuserhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
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

func TestAdminUsersNoMutation(t *testing.T) {
	store := &usersStoreStub{}
	methods := []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete}
	targets := []string{
		"/admin/v1/users",
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
	listRows     []admindb.AdminListUsersForTenantRow
	getRow       admindb.AdminGetUserForTenantRow
	historyRows  []admindb.AdminListUserBalanceHistoryForTenantRow
	listArg      admindb.AdminListUsersForTenantParams
	getArg       admindb.AdminGetUserForTenantParams
	historyArg   admindb.AdminListUserBalanceHistoryForTenantParams
	listCalls    int
	getCalls     int
	historyCalls int
}

func (s *usersStoreStub) AdminListUsersForTenant(_ context.Context, arg admindb.AdminListUsersForTenantParams) ([]admindb.AdminListUsersForTenantRow, error) {
	s.listCalls++
	s.listArg = arg
	return s.listRows, nil
}

func (s *usersStoreStub) AdminGetUserForTenant(_ context.Context, arg admindb.AdminGetUserForTenantParams) (admindb.AdminGetUserForTenantRow, error) {
	s.getCalls++
	s.getArg = arg
	return s.getRow, nil
}

func (s *usersStoreStub) AdminListUserBalanceHistoryForTenant(_ context.Context, arg admindb.AdminListUserBalanceHistoryForTenantParams) ([]admindb.AdminListUserBalanceHistoryForTenantRow, error) {
	s.historyCalls++
	s.historyArg = arg
	return s.historyRows, nil
}

func (s *usersStoreStub) calls() int {
	return s.listCalls + s.getCalls + s.historyCalls
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
