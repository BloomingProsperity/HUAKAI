package gatewayhttp

import (
	"context"
	"encoding/json"
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
	pool := poolOrDefault(s.pool, arg.TenantID, arg.Name)
	pool.TopKDefault = arg.TopKDefault
	pool.CapabilityDefault = arg.CapabilityDefault
	pool.AllowLastResort = arg.AllowLastResort
	s.pool = pool
	return pool, nil
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
	pool := poolOrDefault(s.pool, arg.TenantID, "primary")
	if arg.Name != nil {
		pool.Name = *arg.Name
	}
	if arg.TopKDefault != nil {
		pool.TopKDefault = *arg.TopKDefault
	}
	if arg.CapabilityDefault != nil {
		pool.CapabilityDefault = *arg.CapabilityDefault
	}
	if arg.AllowLastResort != nil {
		pool.AllowLastResort = *arg.AllowLastResort
	}
	if arg.Enabled != nil {
		pool.Enabled = *arg.Enabled
	}
	pool.ID = arg.ID
	pool.TenantID = arg.TenantID
	s.pool = pool
	return pool, nil
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
	if store.insert.TopKDefault != defaultAdminPoolTopKDefault ||
		store.insert.CapabilityDefault != defaultAdminPoolCapabilityDefault ||
		store.insert.AllowLastResort {
		t.Fatalf("insert defaults mismatch: %+v", store.insert)
	}
}

func TestAT_POOL_001_001_CreateFieldsPersistAndReadBack(t *testing.T) {
	store := &adminPoolsStoreStub{}
	createResp := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodPost, "/admin/v1/pools/",
		`{"name":"primary","top_k_default":4,"capability_default":"safe_equivalent_allowed","allow_last_resort":true}`)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	if store.insert == nil ||
		store.insert.TopKDefault != 4 ||
		store.insert.CapabilityDefault != "safe_equivalent_allowed" ||
		!store.insert.AllowLastResort {
		t.Fatalf("create did not pass persistence fields: %+v", store.insert)
	}

	getResp := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodGet, "/admin/v1/pools/77", "")
	if getResp.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getResp.Code, getResp.Body.String())
	}
	var got db.PoolGroup
	if err := json.Unmarshal(getResp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v body=%s", err, getResp.Body.String())
	}
	if got.TopKDefault != 4 || got.CapabilityDefault != "safe_equivalent_allowed" || !got.AllowLastResort {
		t.Fatalf("get response lost fields: %+v", got)
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
		`{"name":" updated ","enabled":false,"top_k_default":3,"capability_default":"safe_equivalent_allowed","allow_last_resort":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.update == nil || store.update.ID != 77 || store.update.TenantID != defaultAdminPoolsTenantID {
		t.Fatalf("update params mismatch: %+v", store.update)
	}
	if store.update.Name == nil || *store.update.Name != "updated" || store.update.Enabled == nil || *store.update.Enabled {
		t.Fatalf("update fields mismatch: %+v", store.update)
	}
	if store.update.TopKDefault == nil || *store.update.TopKDefault != 3 ||
		store.update.CapabilityDefault == nil || *store.update.CapabilityDefault != "safe_equivalent_allowed" ||
		store.update.AllowLastResort == nil || !*store.update.AllowLastResort {
		t.Fatalf("update persistence fields mismatch: %+v", store.update)
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
	return db.PoolGroup{
		ID:                77,
		TenantID:          tenantID,
		Name:              name,
		TopKDefault:       defaultAdminPoolTopKDefault,
		CapabilityDefault: defaultAdminPoolCapabilityDefault,
		Enabled:           true,
	}
}
