package claudecookiehttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/claudecookie"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

type authStub struct {
	identity admin.AdminIdentity
	err      error
}

func (s authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.identity, s.err
}

type serviceStub struct {
	convertInput claudecookie.ConvertInput
	planInput    claudecookie.PlanInput
	executeInput claudecookie.ExecuteInput
	convertCalls int
	planCalls    int
	executeCalls int
	convertErr   error
}

type capabilityStub struct {
	err        error
	tenantID   int64
	capability tenantcapability.Capability
	calls      int
}

func (s *capabilityStub) Require(_ context.Context, tenantID int64, capability tenantcapability.Capability) error {
	s.calls++
	s.tenantID = tenantID
	s.capability = capability
	return s.err
}

func (s *serviceStub) Convert(_ context.Context, input claudecookie.ConvertInput) (claudecookie.Session, error) {
	s.convertCalls++
	s.convertInput = input
	return claudecookie.Session{ID: "session-id", TenantID: input.TenantID, Status: claudecookie.StatusReady}, s.convertErr
}

func (s *serviceStub) Plan(_ context.Context, input claudecookie.PlanInput) (accountintake.PlanResult, error) {
	s.planCalls++
	s.planInput = input
	return accountintake.PlanResult{PlanHash: strings.Repeat("a", 64)}, nil
}

func (s *serviceStub) Execute(_ context.Context, input claudecookie.ExecuteInput) (accountintake.ExecutionResult, error) {
	s.executeCalls++
	s.executeInput = input
	return accountintake.ExecutionResult{PlanHash: input.PlanHash}, nil
}

func TestConvertAcceptsOnlyScopedTenantOperatorAndNeverEchoesCookie(t *testing.T) {
	service := &serviceStub{}
	handler := testHandler(authStub{identity: tenantOperator(7)}, service)
	secret := "session-cookie-secret"
	recorder := post(handler, "/admin/v1/credentials/claude-cookie/convert",
		`{"tenant_id":7,"session_key":"`+secret+`","organization_id":"org-1"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.convertCalls != 1 || service.convertInput.SessionKey != secret ||
		service.convertInput.ActorID != "admin_token:9" || service.convertInput.ActorRole != admin.RoleTenantOperator {
		t.Fatalf("input=%+v calls=%d", service.convertInput, service.convertCalls)
	}
	if strings.Contains(recorder.Body.String(), secret) {
		t.Fatalf("响应泄漏 Cookie：%s", recorder.Body.String())
	}
}

func TestConvertReturnsExplicitOrganizationChoicesWithoutEchoingCookie(t *testing.T) {
	service := &serviceStub{convertErr: &claudecookie.OrganizationSelectionError{Organizations: []claudecookie.Organization{
		{ID: "org-1", Name: "One"}, {ID: "org-2", Name: "Two"},
	}}}
	handler := testHandler(authStub{identity: tenantOperator(7)}, service)
	recorder := post(handler, "/admin/v1/credentials/claude-cookie/convert",
		`{"tenant_id":7,"session_key":"session-cookie-secret"}`)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "organization_selection_required") ||
		!strings.Contains(recorder.Body.String(), "org-1") || strings.Contains(recorder.Body.String(), "session-cookie-secret") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestClaudeCookieRoutesRejectPlatformSessionAndCrossTenantBeforeService(t *testing.T) {
	tests := []struct {
		name     string
		identity admin.AdminIdentity
		tenantID int
	}{
		{name: "平台部署者不能代租户", identity: admin.AdminIdentity{Source: admin.AdminSourceToken, TokenID: 9, Role: admin.RolePlatformAdmin}, tenantID: 7},
		{name: "浏览器会话不能导入", identity: admin.AdminIdentity{Source: admin.AdminSourceSession, UserID: 5, Role: admin.RoleTenantOperator, ScopeTenantID: 7}, tenantID: 7},
		{name: "租户令牌不能跨租户", identity: tenantOperator(8), tenantID: 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &serviceStub{}
			handler := testHandler(authStub{identity: test.identity}, service)
			recorder := post(handler, "/admin/v1/credentials/claude-cookie/convert",
				`{"tenant_id":7,"session_key":"secret"}`)
			if recorder.Code != http.StatusForbidden || service.convertCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.convertCalls, recorder.Body.String())
			}
		})
	}
}

func TestClaudeCookieRoutesStayTokenOnlyWithProductionStyleResolver(t *testing.T) {
	service := &serviceStub{}
	handler := testHandler(adminsessionauthtest.Resolver(), service)
	request := httptest.NewRequest(http.MethodPost, "/admin/v1/credentials/claude-cookie/convert", strings.NewReader(`{"tenant_id":7,"session_key":"secret"}`))
	request.Header.Set("Authorization", "Bearer "+adminsessionauthtest.SessionBearer)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || service.convertCalls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.convertCalls, recorder.Body.String())
	}
}

func TestClaudeCookieRequiresExplicitTenantCapabilityGrant(t *testing.T) {
	service := &serviceStub{}
	checker := &capabilityStub{err: tenantcapability.ErrDenied}
	handler := testHandlerWithCapabilities(authStub{identity: tenantOperator(7)}, service, checker)
	recorder := post(handler, "/admin/v1/credentials/claude-cookie/convert",
		`{"tenant_id":7,"session_key":"session-cookie-secret"}`)
	if recorder.Code != http.StatusForbidden || service.convertCalls != 0 || checker.calls != 1 ||
		checker.tenantID != 7 || checker.capability != tenantcapability.ClaudeCookie {
		t.Fatalf("status=%d service_calls=%d checker=%+v body=%s", recorder.Code, service.convertCalls, checker, recorder.Body.String())
	}
}

func TestPlanAndExecuteBindAuditActorAndRejectUnknownFields(t *testing.T) {
	service := &serviceStub{}
	handler := testHandler(authStub{identity: tenantOperator(7)}, service)
	account := `"account":{"provider_id":2,"channel_id":3,"name_prefix":"claude","account_type":"oauth"}`
	plan := post(handler, "/admin/v1/credentials/claude-cookie/plan",
		`{"tenant_id":7,"intake_session_id":"session-id",`+account+`}`)
	if plan.Code != http.StatusOK || service.planCalls != 1 || service.planInput.ActorID != "admin_token:9" {
		t.Fatalf("plan status=%d calls=%d input=%+v body=%s", plan.Code, service.planCalls, service.planInput, plan.Body.String())
	}
	execute := post(handler, "/admin/v1/credentials/claude-cookie/execute",
		`{"tenant_id":7,"intake_session_id":"session-id",`+account+`,"plan_hash":"`+strings.Repeat("a", 64)+`","reason":"导入"}`)
	if execute.Code != http.StatusOK || service.executeCalls != 1 ||
		service.executeInput.ActorID != "admin_token:9" || service.executeInput.ActorRole != admin.RoleTenantOperator {
		t.Fatalf("execute status=%d calls=%d input=%+v body=%s", execute.Code, service.executeCalls, service.executeInput, execute.Body.String())
	}
	invalid := post(handler, "/admin/v1/credentials/claude-cookie/plan",
		`{"tenant_id":7,"intake_session_id":"session-id",`+account+`,"unknown":true}`)
	if invalid.Code != http.StatusBadRequest || service.planCalls != 1 {
		t.Fatalf("unknown status=%d calls=%d body=%s", invalid.Code, service.planCalls, invalid.Body.String())
	}
}

func TestUpstreamFailureDoesNotExposeBackendDetails(t *testing.T) {
	service := &serviceStub{convertErr: errors.New("postgres password=internal-secret")}
	handler := testHandler(authStub{identity: tenantOperator(7)}, service)
	recorder := post(handler, "/admin/v1/credentials/claude-cookie/convert",
		`{"tenant_id":7,"session_key":"session-cookie-secret"}`)
	if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), "internal-secret") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func testHandler(auth AdminAuth, service Service) http.Handler {
	return testHandlerWithCapabilities(auth, service, &capabilityStub{})
}

func testHandlerWithCapabilities(auth AdminAuth, service Service, capabilities CapabilityChecker) http.Handler {
	router := chi.NewRouter()
	router.Route("/admin/v1/credentials", func(router chi.Router) {
		Mount(router, Deps{Auth: auth, Service: service, Capabilities: capabilities})
	})
	return router
}

func post(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func tenantOperator(tenantID int64) admin.AdminIdentity {
	return admin.AdminIdentity{Source: admin.AdminSourceToken, TokenID: 9, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID}
}
