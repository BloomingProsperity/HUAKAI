package pricingcataloghttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
)

func TestPricingRatioHandler_NonAdminIs403(t *testing.T) {
	store := &fakeRatioStore{}
	rec := doPricingRatioRequest(t, AdminPricingRatioDeps{
		Auth:  fakeAdminAuth{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Store: store,
	}, http.MethodPut, "/9?tenant_id=7", `{"ratio":"1.2"}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403", rec.Code, rec.Body.String())
	}
	if store.upsertCalled {
		t.Fatalf("tenant_operator write reached store")
	}
}

func TestPricingRatioHandler_ZeroRatioIs400(t *testing.T) {
	rec := doPricingRatioRequest(t, validPricingRatioDeps(), http.MethodPut, "/9?tenant_id=7", `{"ratio":"0"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "invalid_ratio")
}

func TestPricingRatioHandler_NegativeRatioIs400(t *testing.T) {
	rec := doPricingRatioRequest(t, validPricingRatioDeps(), http.MethodPut, "/9?tenant_id=7", `{"ratio":"-1.5"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "invalid_ratio")
}

func TestPricingRatioHandler_InvalidDecimalStringIs400(t *testing.T) {
	rec := doPricingRatioRequest(t, validPricingRatioDeps(), http.MethodPut, "/9?tenant_id=7", `{"ratio":"1.2.3"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "invalid_ratio")
}

func TestPricingRatioHandler_DeleteReturns404WhenMissing(t *testing.T) {
	rec := doPricingRatioRequest(t, AdminPricingRatioDeps{
		Auth:  fakeAdminAuth{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store: &fakeRatioStore{deleteErr: pricingcatalog.ErrNotFound},
	}, http.MethodDelete, "/9?tenant_id=7", "")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s want 404", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "pricing_ratio_not_found")
}

func TestPricingRatioHandler_TenantIsolationViaParseAdminCatalogPage(t *testing.T) {
	rec := doPricingRatioRequest(t, AdminPricingRatioDeps{
		Auth:  fakeAdminAuth{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store: &fakeRatioStore{},
	}, http.MethodGet, "/", "")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400 tenant_id_required", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "tenant_id_required")
}

func TestPricingRatioHandler_MoneyPrecisionRespondsWithExactDecimalString(t *testing.T) {
	const exact = "123456789.12345678"
	rec := doPricingRatioRequest(t, AdminPricingRatioDeps{
		Auth: fakeAdminAuth{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store: &fakeRatioStore{upsertRow: pricingcatalog.GroupPricingRatio{
			ID:          1,
			TenantID:    7,
			PoolGroupID: 9,
			Ratio:       decimal.RequireFromString(exact),
			RatioText:   exact,
		}},
	}, http.MethodPut, "/9?tenant_id=7", `{"ratio":"`+exact+`","public_ratio":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, ok := body["ratio"].(string); !ok || got != exact {
		t.Fatalf("ratio field=%#v want string %q", body["ratio"], exact)
	}
}

func validPricingRatioDeps() AdminPricingRatioDeps {
	return AdminPricingRatioDeps{
		Auth:  fakeAdminAuth{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store: &fakeRatioStore{},
	}
}

func doPricingRatioRequest(t *testing.T, deps AdminPricingRatioDeps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	MountPricingRatioRoutes(r, deps)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != want {
		t.Fatalf("error code=%q body=%s want %q", body.Error.Code, rec.Body.String(), want)
	}
}

type fakeAdminAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (f fakeAdminAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return f.ident, f.err
}

type fakeRatioStore struct {
	upsertRow    pricingcatalog.GroupPricingRatio
	upsertCalled bool
	deleteErr    error
}

func (s *fakeRatioStore) GetRatio(context.Context, int64, int64) (pricingcatalog.GroupPricingRatio, error) {
	return pricingcatalog.GroupPricingRatio{}, errors.New("unexpected get")
}

func (s *fakeRatioStore) ListRatios(context.Context, int64) ([]pricingcatalog.GroupPricingRatio, error) {
	return []pricingcatalog.GroupPricingRatio{}, nil
}

func (s *fakeRatioStore) UpsertRatio(_ context.Context, p pricingcatalog.UpsertRatioParams) (pricingcatalog.GroupPricingRatio, error) {
	s.upsertCalled = true
	if !p.Ratio.IsPositive() {
		return pricingcatalog.GroupPricingRatio{}, errors.New("test store received non-positive ratio")
	}
	if s.upsertRow.ID != 0 {
		return s.upsertRow, nil
	}
	return pricingcatalog.GroupPricingRatio{
		ID:          1,
		TenantID:    p.TenantID,
		PoolGroupID: p.PoolGroupID,
		Ratio:       p.Ratio,
		RatioText:   p.Ratio.String(),
		PublicRatio: p.PublicRatio,
	}, nil
}

func (s *fakeRatioStore) DeleteRatio(context.Context, int64, int64) error {
	return s.deleteErr
}
