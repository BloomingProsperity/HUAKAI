package modeldiscoveryhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

func TestRoutesRejectTenantOperatorBeforeStore(t *testing.T) {
	store := &storeStub{}
	router := mountTestRoutes(Deps{Auth: authStub{identity: tenantOperator(7)}, Store: store})
	for _, target := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/admin/v1/model-discoveries"},
		{method: http.MethodPost, path: "/admin/v1/model-discoveries/8/promote", body: `{"reason":"approve"}`},
		{method: http.MethodPost, path: "/admin/v1/model-discoveries/8/ignore", body: `{"reason":"ignore"}`},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(target.method, target.path, strings.NewReader(target.body)))
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s status=%d want 403 body=%s", target.method, target.path, recorder.Code, recorder.Body.String())
		}
	}
	if store.listCalls != 0 || store.promoteCalls != 0 || store.ignoreCalls != 0 {
		t.Fatalf("forbidden caller reached store: %+v", store)
	}
}

func TestListProjectsFiltersAndPage(t *testing.T) {
	next := int64(40)
	store := &storeStub{page: registry.ModelDiscoveryPage{
		Items: []registry.ModelDiscovery{{ID: 41, ProviderModelID: "gpt-new"}}, NextBeforeID: &next,
	}}
	router := mountTestRoutes(Deps{Auth: authStub{identity: platformAdmin()}, Store: store})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet,
		"/admin/v1/model-discoveries?vendor=openai&status=pending&search=gpt&before_id=50&limit=20", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", recorder.Code, recorder.Body.String())
	}
	if store.listCalls != 1 || store.lastList.Access.Role != admin.RolePlatformAdmin ||
		store.lastList.Vendor != "openai" || store.lastList.Status != "pending" ||
		store.lastList.Search != "gpt" || store.lastList.BeforeID != 50 || store.lastList.Limit != 20 {
		t.Fatalf("list params=%+v calls=%d", store.lastList, store.listCalls)
	}
	for _, want := range []string{`"object":"model_discovery_page"`, `"provider_model_id":"gpt-new"`, `"next_before_id":40`} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("body missing %s: %s", want, recorder.Body.String())
		}
	}
}

func TestPromoteUsesAuthenticatedActorAndStrictBody(t *testing.T) {
	store := &storeStub{decisionResult: registry.ModelDiscovery{ID: 8, Status: registry.ModelDiscoveryPromoted}}
	router := mountTestRoutes(Deps{Auth: authStub{identity: platformAdmin()}, Store: store})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/v1/model-discoveries/8/promote",
		strings.NewReader(`{"reason":"verified upstream"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", recorder.Code, recorder.Body.String())
	}
	if store.promoteCalls != 1 || store.lastDecision.ID != 8 ||
		store.lastDecision.Reason != "verified upstream" || store.lastDecision.Access.Actor != "admin_token:11" {
		t.Fatalf("decision=%+v calls=%d", store.lastDecision, store.promoteCalls)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/v1/model-discoveries/8/promote",
		strings.NewReader(`{"reason":"verified","unexpected":true}`)))
	if recorder.Code != http.StatusBadRequest || store.promoteCalls != 1 {
		t.Fatalf("unknown field status=%d calls=%d body=%s", recorder.Code, store.promoteCalls, recorder.Body.String())
	}
}

func TestConflictMapsTo409(t *testing.T) {
	store := &storeStub{decisionErr: registry.ErrModelDiscoveryConflict}
	router := mountTestRoutes(Deps{Auth: authStub{identity: platformAdmin()}, Store: store})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/v1/model-discoveries/9/ignore",
		strings.NewReader(`{"reason":"duplicate"}`)))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "model_discovery_conflict") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type authStub struct {
	identity admin.AdminIdentity
	err      error
}

func (s authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.identity, s.err
}

func platformAdmin() admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}
}

func tenantOperator(tenantID int64) admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 12, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID}
}

type storeStub struct {
	page           registry.ModelDiscoveryPage
	decisionResult registry.ModelDiscovery
	listErr        error
	decisionErr    error
	listCalls      int
	promoteCalls   int
	ignoreCalls    int
	lastList       registry.ModelDiscoveryListParams
	lastDecision   registry.ModelDiscoveryDecision
}

func (s *storeStub) ListModelDiscoveries(_ context.Context, params registry.ModelDiscoveryListParams) (registry.ModelDiscoveryPage, error) {
	s.listCalls++
	s.lastList = params
	return s.page, s.listErr
}

func (s *storeStub) PromoteModelDiscovery(_ context.Context, in registry.ModelDiscoveryDecision) (registry.ModelDiscovery, error) {
	s.promoteCalls++
	s.lastDecision = in
	return s.decisionResult, s.decisionErr
}

func (s *storeStub) IgnoreModelDiscovery(_ context.Context, in registry.ModelDiscoveryDecision) (registry.ModelDiscovery, error) {
	s.ignoreCalls++
	s.lastDecision = in
	return s.decisionResult, s.decisionErr
}

func mountTestRoutes(deps Deps) http.Handler {
	router := chi.NewRouter()
	router.Route("/admin/v1/model-discoveries", func(router chi.Router) {
		MountRoutes(router, deps)
	})
	return router
}
