package gatewayhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

type adminPoolsStoreStub struct {
	insert    *db.InsertPoolParams
	get       *db.GetPoolParams
	list      *db.ListPoolsParams
	update    *db.UpdatePoolParams
	insertErr error
	getErr    error
	updateErr error
	pool      db.PoolGroup
	items     []db.PoolGroup
}

func (s *adminPoolsStoreStub) InsertPool(_ context.Context, arg db.InsertPoolParams) (db.PoolGroup, error) {
	s.insert = &arg
	if s.insertErr != nil {
		return db.PoolGroup{}, s.insertErr
	}
	return poolOrDefault(s.pool, arg.TenantID, arg.Name), nil
}

func (s *adminPoolsStoreStub) GetPool(_ context.Context, arg db.GetPoolParams) (db.PoolGroup, error) {
	s.get = &arg
	if s.getErr != nil {
		return db.PoolGroup{}, s.getErr
	}
	return poolOrDefault(s.pool, arg.TenantID, "primary"), nil
}

func (s *adminPoolsStoreStub) ListPools(_ context.Context, arg db.ListPoolsParams) ([]db.PoolGroup, error) {
	s.list = &arg
	if s.items != nil {
		return s.items, nil
	}
	return []db.PoolGroup{{ID: 7, TenantID: arg.TenantID, Name: "primary", Enabled: true}}, nil
}

func (s *adminPoolsStoreStub) UpdatePool(_ context.Context, arg db.UpdatePoolParams) (db.PoolGroup, error) {
	s.update = &arg
	if s.updateErr != nil {
		return db.PoolGroup{}, s.updateErr
	}
	name := "primary"
	if arg.Name != nil {
		name = *arg.Name
	}
	enabled := true
	if arg.Enabled != nil {
		enabled = *arg.Enabled
	}
	return db.PoolGroup{ID: arg.ID, TenantID: arg.TenantID, Name: name, Enabled: enabled}, nil
}

func TestAdminPools_ListSuccessUsesDefaultLimit(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodGet, "/admin/v1/pools", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.list == nil || store.list.TenantID != defaultAdminPoolsTenantID || store.list.LimitCount != defaultAdminPoolsLimit {
		t.Fatalf("list params mismatch: %+v", store.list)
	}
}

func TestAdminPools_CreateSuccessInsertsTrimmedName(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodPost, "/admin/v1/pools/",
		`{"name":" primary ","description":"owner visible label"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil || store.insert.TenantID != defaultAdminPoolsTenantID || store.insert.Name != "primary" {
		t.Fatalf("insert params mismatch: %+v", store.insert)
	}
}

func TestAdminPools_GetSuccess(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodGet, "/admin/v1/pools/77", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.get == nil || store.get.ID != 77 || store.get.TenantID != defaultAdminPoolsTenantID {
		t.Fatalf("get params mismatch: %+v", store.get)
	}
}

func TestAdminPools_UpdateSuccessPatchesNameAndEnabled(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodPatch, "/admin/v1/pools/77",
		`{"name":" updated ","enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.update == nil || store.update.ID != 77 || store.update.TenantID != defaultAdminPoolsTenantID {
		t.Fatalf("update params mismatch: %+v", store.update)
	}
	if store.update.Name == nil || *store.update.Name != "updated" || store.update.Enabled == nil || *store.update.Enabled {
		t.Fatalf("update fields mismatch: %+v", store.update)
	}
}

func TestAdminPools_CreateMissingNameReturns400(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodPost, "/admin/v1/pools/", `{"description":"missing"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil {
		t.Fatalf("bad request touched store: %+v", store.insert)
	}
}

func TestAdminPools_CreateDuplicateNameReturns409(t *testing.T) {
	store := &adminPoolsStoreStub{insertErr: &pgconn.PgError{Code: "23505"}}
	rec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodPost, "/admin/v1/pools/", `{"name":"primary"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPools_GetNotFoundReturns404(t *testing.T) {
	store := &adminPoolsStoreStub{getErr: pgx.ErrNoRows}
	rec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodGet, "/admin/v1/pools/404", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPools_UnauthorizedReturns401(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAuthStub{err: admin.ErrAdminUnauthorized}, http.MethodGet, "/admin/v1/pools/", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.list != nil {
		t.Fatalf("unauthorized request touched store: %+v", store.list)
	}
}

func TestAdminPools_CreateNameTooLongReturns400(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodPost, "/admin/v1/pools/",
		`{"name":"`+strings.Repeat("a", maxAdminPoolNameRunes+1)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func invokeAdminPools(t *testing.T, store *adminPoolsStoreStub, auth AdminPoolsAuth, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/pools", func(r chi.Router) {
		r.Mount("/", NewAdminPoolsHandler(AdminPoolsDeps{Auth: auth, Store: store}))
	})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func poolOrDefault(pool db.PoolGroup, tenantID int64, name string) db.PoolGroup {
	if pool.ID != 0 {
		return pool
	}
	return db.PoolGroup{ID: 77, TenantID: tenantID, Name: name, Enabled: true}
}
