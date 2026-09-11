package modelratehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/modelrate"
)

func TestListShowsOfficialWhenNoOverride(t *testing.T) {
	rec := doRequest(t, validDeps(), http.MethodGet, "/?vendor=openai", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body catalogListView
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Object != "model_rate_catalog" || body.Currency != "USD" || len(body.Items) != 1 {
		t.Fatalf("body=%+v", body)
	}
	item := body.Items[0]
	if item.Overridden || item.Source != modelrate.SourceOfficial || item.Official.Input != "4" || item.Effective.Input != "4" {
		t.Fatalf("item=%+v want official 4/4", item)
	}
	if item.Official.CacheCreation != "1.2" || item.Effective.CacheCreation != "1.2" {
		t.Fatalf("official write tier missing: %+v", item)
	}
}

func TestPutThenDeleteChangesEffectiveAndRestoresOfficial(t *testing.T) {
	deps := validDeps()
	rec := doRequest(t, deps, http.MethodPut, "/openai/gpt-5.6", `{"input_usd_per_million":"2.5","cache_read_usd_per_million":"0.1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", rec.Code, rec.Body.String())
	}
	var item catalogItemView
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode put: %v", err)
	}
	if !item.Overridden || item.Source != modelrate.SourceOverride || item.Effective.Input != "2.5" || item.Official.Input != "4" {
		t.Fatalf("after put item=%+v", item)
	}
	if item.Effective.Output != "20" || item.Official.Output != "20" {
		t.Fatalf("partial put must keep official output: %+v", item)
	}

	store := deps.Store.(*memoryStore)
	if store.lastUpsert.Actor != "admin_token:1" || store.lastUpsert.ActorRole != admin.RolePlatformAdmin {
		t.Fatalf("audit actor=%q role=%q", store.lastUpsert.Actor, store.lastUpsert.ActorRole)
	}

	rec = doRequest(t, deps, http.MethodDelete, "/openai/gpt-5.6", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, deps, http.MethodGet, "/openai/gpt-5.6", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get after delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if item.Overridden || item.Effective.Input != "4" {
		t.Fatalf("after delete item=%+v want official restored", item)
	}
}

func TestTenantOperatorCannotWriteButCanRead(t *testing.T) {
	deps := validDeps()
	deps.Auth = fakeAuth{ident: admin.AdminIdentity{TokenID: 2, Role: admin.RoleTenantOperator, ScopeTenantID: 7}}
	rec := doRequest(t, deps, http.MethodPut, "/openai/gpt-5.6", `{"input_usd_per_million":"1"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tenant write status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
	if deps.Store.(*memoryStore).upsertCalled {
		t.Fatal("tenant_operator write reached store")
	}
	rec = doRequest(t, deps, http.MethodGet, "/?vendor=openai", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant read status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFinalUserAndSpoofedBodyCannotWrite(t *testing.T) {
	deps := validDeps()
	deps.Auth = fakeAuth{ident: admin.AdminIdentity{TokenID: 3, Role: "user"}}
	rec := doRequest(t, deps, http.MethodPut, "/openai/gpt-5.6", `{"input_usd_per_million":"1","role":"platform_admin","actor":"evil"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user write status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
	if deps.Store.(*memoryStore).upsertCalled {
		t.Fatal("final user write reached store")
	}

	deps = validDeps()
	deps.Auth = fakeAuth{err: admin.ErrAdminUnauthorized}
	rec = doRequest(t, deps, http.MethodPut, "/openai/gpt-5.6", `{"input_usd_per_million":"1"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth write status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}

	deps = validDeps()
	rec = doRequest(t, deps, http.MethodPut, "/openai/gpt-5.6", `{"input_usd_per_million":"2","role":"tenant_operator","actor":"spoof"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("platform put status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := deps.Store.(*memoryStore).lastUpsert; got.Actor != "admin_token:1" || got.ActorRole != admin.RolePlatformAdmin {
		t.Fatalf("body spoof leaked into audit actor=%q role=%q", got.Actor, got.ActorRole)
	}
}

func TestRejectsNegativeAndOutOfRange(t *testing.T) {
	rec := doRequest(t, validDeps(), http.MethodPut, "/openai/gpt-5.6", `{"input_usd_per_million":"-1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("neg status=%d want 400", rec.Code)
	}
	assertErrorCode(t, rec, "invalid_model_rate")

	rec = doRequest(t, validDeps(), http.MethodPut, "/openai/gpt-5.6", `{"input_usd_per_million":"10001"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("max status=%d want 422", rec.Code)
	}
	assertErrorCode(t, rec, "model_rate_out_of_range")

	rec = doRequest(t, validDeps(), http.MethodPut, "/openai/gpt-5.6", `{"input_usd_per_million":"not-a-decimal"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad decimal status=%d want 400", rec.Code)
	}
	assertErrorCode(t, rec, "invalid_model_rate")

	rec = doRequest(t, validDeps(), http.MethodPut, "/1openai/gpt-5.6", `{"input_usd_per_million":"1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad vendor status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
}

func TestPutReplacesOmittedBucketsWithOfficial(t *testing.T) {
	deps := validDeps()
	rec := doRequest(t, deps, http.MethodPut, "/openai/gpt-5.6", `{"input_usd_per_million":"1","output_usd_per_million":"9"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("first put status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, deps, http.MethodPut, "/openai/gpt-5.6", `{"input_usd_per_million":"2.5"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second put status=%d body=%s", rec.Code, rec.Body.String())
	}
	var item catalogItemView
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if item.Effective.Input != "2.5" || item.Effective.Output != "20" || item.Official.Output != "20" {
		t.Fatalf("second put must drop previous output override: %+v", item)
	}
	store := deps.Store.(*memoryStore)
	row := store.rows[store.key("openai", "gpt-5.6")]
	if row.Rates.Output != nil {
		t.Fatalf("stored override still has output=%v; PUT is whole-row replace", row.Rates.Output)
	}
}

func TestTenantOperatorCannotVerifyAudit(t *testing.T) {
	deps := validDeps()
	deps.Auth = fakeAuth{ident: admin.AdminIdentity{TokenID: 2, Role: admin.RoleTenantOperator, ScopeTenantID: 7}}
	rec := doRequest(t, deps, http.MethodGet, "/audit/verify", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tenant audit verify status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteMissingIs404(t *testing.T) {
	rec := doRequest(t, validDeps(), http.MethodDelete, "/openai/missing-model", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "model_rate_not_found")
}

func TestWritesAreSessionSafe(t *testing.T) {
	deps := validDeps()
	deps.Auth = adminsessionauthtest.Resolver()
	router := chi.NewRouter()
	MountRoutes(router, deps)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		if status := adminsessionauthtest.Status(router, method, "/openai/gpt-5.6", adminsessionauthtest.SessionBearer); status == http.StatusUnauthorized {
			t.Fatalf("平台管理员浏览器会话执行 %s 不应被写分级门拒绝", method)
		}
	}
}

func validDeps() Deps {
	return Deps{
		Auth:  fakeAuth{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store: &memoryStore{},
		Official: officialStub{table: billing.RateTable{
			Version: "1.0",
			PricingData: json.RawMessage(`{
				"models":{"gpt-5.6":{"input_micro_usd":"4","output_micro_usd":"20","cache_read_micro_usd":"0.4","cache_creation_micro_usd":"1.2"}},
				"providers":{"openai":{"models":{"gpt-5.6":{"input_micro_usd":"4","output_micro_usd":"20","cache_read_micro_usd":"0.4","cache_creation_micro_usd":"1.2"}}}}
			}`),
		}},
		BillingPolicyVersion: "1.0",
	}
}

func doRequest(t *testing.T, deps Deps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	MountRoutes(r, deps)
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
		t.Fatalf("decode error: %v body=%s", err, rec.Body.String())
	}
	if body.Error.Code != want {
		t.Fatalf("code=%q want %q body=%s", body.Error.Code, want, rec.Body.String())
	}
}

type fakeAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (f fakeAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return f.ident, f.err
}

type officialStub struct {
	table billing.RateTable
	err   error
}

func (s officialStub) GetOfficialRateTable(context.Context, string) (billing.RateTable, error) {
	return s.table, s.err
}

type memoryStore struct {
	rows         map[string]modelrate.Override
	upsertCalled bool
	lastUpsert   modelrate.UpsertParams
}

func (s *memoryStore) key(vendor, model string) string {
	return strings.ToLower(vendor) + "\x00" + model
}

func (s *memoryStore) List(context.Context) ([]modelrate.Override, error) {
	out := make([]modelrate.Override, 0, len(s.rows))
	for _, row := range s.rows {
		out = append(out, row)
	}
	return out, nil
}

func (s *memoryStore) Get(_ context.Context, vendor, model string) (modelrate.Override, error) {
	if row, ok := s.rows[s.key(vendor, model)]; ok {
		return row, nil
	}
	return modelrate.Override{}, modelrate.ErrNotFound
}

func (s *memoryStore) Upsert(_ context.Context, p modelrate.UpsertParams) (modelrate.Override, error) {
	s.upsertCalled = true
	s.lastUpsert = p
	if err := modelrate.ValidateUpsert(p); err != nil {
		return modelrate.Override{}, err
	}
	if s.rows == nil {
		s.rows = map[string]modelrate.Override{}
	}
	row := modelrate.Override{Vendor: p.Vendor, Model: p.Model, Rates: p.Rates.Clone(), CreatedBy: p.Actor, UpdatedBy: p.Actor}
	s.rows[s.key(p.Vendor, p.Model)] = row
	return row, nil
}

func (s *memoryStore) Delete(_ context.Context, p modelrate.DeleteParams) error {
	key := s.key(p.Vendor, p.Model)
	if _, ok := s.rows[key]; !ok {
		return modelrate.ErrNotFound
	}
	delete(s.rows, key)
	return nil
}

func (s *memoryStore) VerifyChain(context.Context) (modelrate.VerifyResult, error) {
	return modelrate.VerifyResult{OK: true}, nil
}
